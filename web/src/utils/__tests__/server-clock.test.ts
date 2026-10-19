/**
 * P2-10 服务端钟单测：DI 工厂打桩，不发网。
 * 覆盖：未校准直报本地钟 / offset 数学 / 高 RTT 丢样本 / 失败保旧 offset /
 * 合同违约拒绝 / 单调钳制（未校准不钳 + 挂回上限内冻结 + 超限跟随告警）/
 * start 周期+隐藏跳过+stop / sync 并发去重。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createServerClock, type ServerClockDeps } from '../server-clock'

interface Rig {
  deps: ServerClockDeps
  warnings: string[]
  advanceLocal: (ms: number) => void
  advancePerf: (ms: number) => void
  setEpoch: (ms: number | null) => void
}

function rig(): Rig {
  let local = 1_000_000
  let perf = 0
  let epoch: number | null = 0
  const warnings: string[] = []
  const fetchImpl: () => Promise<{ serverTime: string; epochMs: number }> = () =>
    Promise.resolve({ serverTime: new Date(epoch ?? 0).toISOString(), epochMs: epoch ?? NaN })
  const deps: ServerClockDeps = {
    fetchTime: () => fetchImpl(),
    localNow: () => local,
    perf: () => perf,
    warn: (message) => warnings.push(message),
    hidden: () => false,
  }
  return {
    deps,
    warnings,
    advanceLocal: (ms) => { local += ms },
    advancePerf: (ms) => { perf += ms },
    setEpoch: (ms) => { epoch = ms },
  }
}

describe('createServerClock', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('未校准时直报本地钟，health 未校准', () => {
    const r = rig()
    const clock = createServerClock(r.deps)
    expect(clock.serverNow()).toBe(1_000_000)
    r.advanceLocal(5)
    expect(clock.serverNow()).toBe(1_000_005)
    const health = clock.health()
    expect(health.calibrated).toBe(false)
    expect(health.offsetMs).toBe(0)
    expect(health.samples).toBe(0)
  })

  it('成功同步：offset = epochMs − 本地中点，后续读数 = 本地钟 + offset', async () => {
    const r = rig()
    const clock = createServerClock(r.deps)
    const local0 = 2_000_000
    r.advanceLocal(local0 - 1_000_000)
    // 响应时刻本地钟前进 40ms（往返 40ms → 中点 local0+20）
    r.setEpoch(2_000_500)
    const syncPromise = clock.sync()
    r.advanceLocal(40)
    r.advancePerf(40)
    expect(await syncPromise).toBe(true)
    expect(clock.health()).toMatchObject({ calibrated: true, offsetMs: 2_000_500 - (local0 + 20), rttMs: 40, samples: 1, failures: 0 })
    r.advanceLocal(100)
    expect(clock.serverNow()).toBe(local0 + 40 + 100 + (2_000_500 - (local0 + 20)))
  })

  it('RTT 超限丢弃样本并保留旧 offset', async () => {
    const r = rig()
    const clock = createServerClock(r.deps)
    // 先校准一次：offset = 500
    r.setEpoch(2_000_500)
    const first = clock.sync()
    r.advancePerf(40)
    expect(await first).toBe(true)
    const accepted = clock.health().offsetMs
    // 再来一次高 RTT 样本：perf 前进 1500ms > 1000ms 上限 → 丢弃
    r.setEpoch(9_999_999)
    const second = clock.sync()
    r.advanceLocal(1_500)
    r.advancePerf(1_500)
    expect(await second).toBe(false)
    const health = clock.health()
    expect(health.offsetMs).toBe(accepted)
    expect(health.failures).toBe(1)
    expect(r.warnings.some((m) => m.includes('往返超限'))).toBe(true)
  })

  it('拉取失败：旧 offset 保留；从未成功过则 offset 0 且不抛', async () => {
    const r = rig()
    let fail = false
    r.deps.fetchTime = () => (fail ? Promise.reject(new Error('boom')) : Promise.resolve({ serverTime: '', epochMs: 3_000_000 }))
    const clock = createServerClock(r.deps)
    // 首次失败：不抛，offset 0
    fail = true
    await expect(clock.sync()).resolves.toBe(false)
    expect(clock.health()).toMatchObject({ calibrated: false, offsetMs: 0, failures: 1 })
    expect(clock.serverNow()).toBe(1_000_000)
    // 成功一次后再失败：offset 保留
    fail = false
    r.advancePerf(10)
    await clock.sync()
    const accepted = clock.health().offsetMs
    fail = true
    await clock.sync()
    expect(clock.health()).toMatchObject({ calibrated: true, offsetMs: accepted, failures: 2 })
    expect(clock.serverNow()).toBe(r.deps.localNow() + accepted)
  })

  it('epoch_ms 非有限数按合同违约拒绝且告警', async () => {
    const r = rig()
    r.deps.fetchTime = () => Promise.resolve({ serverTime: '', epochMs: Number.NaN })
    const clock = createServerClock(r.deps)
    await expect(clock.sync()).resolves.toBe(false)
    expect(clock.health()).toMatchObject({ calibrated: false, offsetMs: 0, failures: 1 })
    expect(r.warnings.some((m) => m.includes('epoch_ms'))).toBe(true)
  })

  it('单调钳制：校准后小幅回拨（≤挂回上限）原地冻结等追平；未校准不钳', async () => {
    const r = rig()
    r.deps.fetchTime = () => Promise.resolve({ serverTime: '', epochMs: 5_000_000 })
    const clock = createServerClock(r.deps)
    // 本地钟回拨但未校准 → 直报（不冻结）
    expect(clock.serverNow()).toBe(1_000_000)
    r.advanceLocal(-500)
    expect(clock.serverNow()).toBe(999_500)
    // 校准：中点 (999_500+999_510)/2 = 999_505，offset = 5_000_000 − 999_505 = 4_000_495
    const s = clock.sync()
    r.advanceLocal(10)
    r.advancePerf(10)
    await s
    const high = clock.serverNow()
    expect(high).toBe(5_000_005)
    const offsetBefore = clock.health().offsetMs
    // 新样本 offset 小幅回拉：epoch 4_995_000 → 新 offset = 3_995_485，
    // 新读数 = high − 5_000（5s ≤ 30s 挂回上限，视为重校准噪声）→ 原地冻结
    r.deps.fetchTime = () => Promise.resolve({ serverTime: '', epochMs: 4_995_000 })
    const back = clock.sync()
    r.advanceLocal(10)
    r.advancePerf(10)
    await back
    expect(clock.health().offsetMs).toBeLessThan(offsetBefore)
    // 读数不随 offset 回退：原地挂起到本地钟追平
    expect(clock.serverNow()).toBe(high)
    r.advanceLocal(100)
    expect(clock.serverNow()).toBe(high)
    r.advanceLocal(5_001)
    expect(clock.serverNow()).toBeGreaterThan(high)
    expect(r.warnings.some((m) => m.includes('挂回上限'))).toBe(false)
  })

  it('单调钳制：回拨超过挂回上限按真实时钟事件一次性跟随并告警', async () => {
    const r = rig()
    r.deps.fetchTime = () => Promise.resolve({ serverTime: '', epochMs: 5_000_000 })
    const clock = createServerClock(r.deps)
    const s = clock.sync()
    r.advanceLocal(10)
    r.advancePerf(10)
    await s
    const high = clock.serverNow()
    // 新样本把读数整段拉回（epoch 1_000_100：新读数比 high 低约 4,000,000ms ≫ 30s 挂回上限）
    r.deps.fetchTime = () => Promise.resolve({ serverTime: '', epochMs: 1_000_100 })
    const back = clock.sync()
    r.advanceLocal(10)
    r.advancePerf(10)
    await back
    // 冻结只罩估算噪声；真实时钟事件（远未来样本/NTP 步进/休眠唤醒）一次性跟随，
    // 绝不无限冻结到刷新页面——过期闸门、推送未来守卫、底栏钟全部基于这个保证。
    const followed = clock.serverNow()
    expect(followed).toBeLessThan(high)
    expect(r.warnings.some((m) => m.includes('挂回上限'))).toBe(true)
    r.advanceLocal(100)
    expect(clock.serverNow()).toBe(followed + 100)
  })

  it('start 立即同步 + 周期重同步，页面隐藏跳过，stop 清干净', async () => {
    const r = rig()
    let hidden = false
    const remove = vi.fn()
    // 用数组递回调：TS 不会把闭包内的赋值纳入收窄分析，裸 let 会被推成 never。
    const visibleHandlers: Array<() => void> = []
    r.deps.hidden = () => hidden
    r.deps.onVisible = (handler) => {
      visibleHandlers.push(handler)
      return remove
    }
    let epochMs = 10_000
    r.deps.fetchTime = () => {
      epochMs += 1_000
      return Promise.resolve({ serverTime: '', epochMs })
    }
    const clock = createServerClock({ ...r.deps, resyncMs: 60_000 })
    clock.start()
    expect(clock.health().samples).toBe(0) // sync 进行期不写样本
    await vi.advanceTimersByTimeAsync(0)
    expect(clock.health().samples).toBe(1)
    // 隐藏时周期 tick 跳过
    hidden = true
    await vi.advanceTimersByTimeAsync(60_000)
    expect(clock.health().samples).toBe(1)
    // 重新可见：监听回调触发一次重同步
    hidden = false
    for (const handler of visibleHandlers) handler()
    await vi.advanceTimersByTimeAsync(0)
    expect(clock.health().samples).toBe(2)
    // 可见时周期照常
    await vi.advanceTimersByTimeAsync(60_000)
    expect(clock.health().samples).toBe(3)
    // stop：清 interval + 摘掉可见性监听
    clock.stop()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(clock.health().samples).toBe(3)
    expect(remove).toHaveBeenCalledOnce()
  })

  it('并发 sync 自动去重为一次拉取', async () => {
    const r = rig()
    let calls = 0
    r.deps.fetchTime = () => {
      calls += 1
      return Promise.resolve({ serverTime: '', epochMs: 42_000 })
    }
    const clock = createServerClock(r.deps)
    const [a, b] = await Promise.all([clock.sync(), clock.sync()])
    expect(a).toBe(true)
    expect(b).toBe(true)
    expect(calls).toBe(1)
    // 收尾后再 sync 是新一轮
    await clock.sync()
    expect(calls).toBe(2)
  })
})
