/**
 * P2-10 服务端时钟（单例 + 漂移补偿）。
 * 只服务「交易时刻语义」的显示与判定：有效期倒计时、竞价/时段带、提交新鲜度闸门、底栏时段钟。
 * 动画节拍与推送节流（performance.now / 纯时长语义）不走这里。
 *
 * 语义：
 * - offset = 服务端 epoch_ms − 本地中点时刻（RTT/2 口径），不做速率级漂移估计（量级远超需要）。
 * - 失败降级照 TradingView datafeed.js:304-325 纪律：warn 留痕 + 回退本地钟，不抛给消费方；
 *   曾有有效样本时保留旧 offset（校准过的本地钟），从未成功过则 offset 0（本地钟）。
 * - 高 RTT（>1s）样本与合同违约样本（epoch_ms 非有限）直接丢弃。
 * - 单调钳制：serverNow 不因估算抖动回退——重校准把 offset 往回收（RTT 中点假设的秒级以内
 *   噪声）时，读数原地挂起等本地钟追平；保护 45s/90s/+5s/30s 新鲜度闸门不被重校准瞬时误伤。
 *   钳制有界（30s 挂回上限）：远端发了一个远未来样本、或系统钟整段回跳（NTP 步进/休眠唤醒），
 *   属于真实时钟事件而非估算噪声——读数一次性跟随到新现实，绝不无限冻结到刷新页面。
 *   （评审 major：无界冻结会让信号永不过期、推送未来守卫误杀、底栏钟与现实脱节。）
 * 启动：main.ts 调 startServerClock()（显式启动，不做懒拉取，测试面不发网）。
 */
import { fetchServerTime, type ServerTime } from '@/api/system'

/** 重校准周期。offset 模型下更长的间隔徒增累计误差，更短只是白折腾（量级秒以下）。 */
export const CLOCK_RESYNC_MS = 300_000
/** 样本往返上限：超过即视为不可信（睡眠唤醒/代理停顿），丢弃保留旧 offset。 */
export const CLOCK_MAX_RTT_MS = 1_000
/** 单调钳制的挂回上限：±RTT 中点假设噪声（秒级）内才冻结读数；超出即视为真实时钟事件，读数跟随。 */
export const CLOCK_MAX_HOLDBACK_MS = 30_000

export interface ServerClockDeps {
  fetchTime: () => Promise<ServerTime>
  localNow: () => number
  perf: () => number
  warn: (message: string, error?: unknown) => void
  hidden: () => boolean
  resyncMs?: number
  onVisible?: (handler: () => void) => () => void
}

export interface ServerClockHealth {
  calibrated: boolean
  offsetMs: number
  /** 最近一次有效样本的响应落点（perf 单调钟口径）。 */
  sampledPerf: number | null
  rttMs: number | null
  samples: number
  failures: number
}

export interface ServerClock {
  /** 校准后的当前时刻（epoch 毫秒）。未校准时等于本地钟。 */
  serverNow: () => number
  health: () => ServerClockHealth
  /** 立即校准一次；并发自动去重。返回值 = 本次样本是否被采纳。 */
  sync: () => Promise<boolean>
  start: () => void
  stop: () => void
}

export function createServerClock(deps: ServerClockDeps): ServerClock {
  const resyncMs = deps.resyncMs ?? CLOCK_RESYNC_MS
  let offset = 0
  let calibrated = false
  let sampledPerf: number | null = null
  let rttMs: number | null = null
  let samples = 0
  let failures = 0
  let lastReported = Number.NEGATIVE_INFINITY
  let inflight: Promise<boolean> | null = null
  let timer: ReturnType<typeof setInterval> | undefined
  let removeVisible: (() => void) | undefined

  function adopt(epochMs: number, mid: number, elapsed: number): boolean {
    if (!Number.isFinite(epochMs)) {
      failures += 1
      deps.warn('[clock] 服务端时钟响应缺少合法 epoch_ms，回退本地钟')
      return false
    }
    if (elapsed > CLOCK_MAX_RTT_MS) {
      failures += 1
      deps.warn('[clock] 服务端时钟往返超限（' + Math.round(elapsed) + 'ms），丢弃样本保留旧补偿')
      return false
    }
    offset = epochMs - mid
    calibrated = true
    rttMs = elapsed
    samples += 1
    sampledPerf = deps.perf()
    return true
  }

  function sync(): Promise<boolean> {
    if (inflight) return inflight
    const t0 = deps.perf()
    const local0 = deps.localNow()
    inflight = deps
      .fetchTime()
      .then((reply) => {
        const elapsed = deps.perf() - t0
        return adopt(reply.epochMs, (local0 + deps.localNow()) / 2, elapsed)
      })
      .catch((error: unknown) => {
        failures += 1
        deps.warn('[clock] 服务端时钟同步失败，回退本地钟', error)
        return false
      })
      .finally(() => {
        inflight = null
      })
    return inflight
  }

  function serverNow(): number {
    const next = deps.localNow() + offset
    /* 单调钳制只在校准后生效：未校准 = 纯本地钟，原样直报（系统时间回拨跟随显示，
     * 测试里的时钟回放也不会被钳住）；校准后 offset 回拉时才原地挂起保护闸门。
     * 挂回有界（评审 major 收口）：回拨在挂回上限内视为重校准噪声原地冻结；超出即
     * 真实时钟事件（远未来样本/NTP 步进/休眠唤醒），warn 留痕后跟随，绝不无限冻结。 */
    if (!calibrated) return next
    if (next < lastReported) {
      if (lastReported - next <= CLOCK_MAX_HOLDBACK_MS) return lastReported
      deps.warn('[clock] 读数回拨超过挂回上限（' + Math.round(lastReported - next) + 'ms），按真实时钟事件跟随')
    }
    lastReported = next
    return next
  }

  function tick(): void {
    if (deps.hidden()) return
    void sync()
  }

  function start(): void {
    if (timer !== undefined) return
    void sync()
    timer = setInterval(tick, resyncMs)
    if (deps.onVisible) {
      removeVisible = deps.onVisible(() => tick())
    }
  }

  function stop(): void {
    if (timer !== undefined) {
      clearInterval(timer)
      timer = undefined
    }
    if (removeVisible) {
      removeVisible()
      removeVisible = undefined
    }
  }

  return {
    serverNow,
    health: () => ({ calibrated, offsetMs: offset, sampledPerf, rttMs, samples, failures }),
    sync,
    start,
    stop,
  }
}

const live = createServerClock({
  fetchTime: fetchServerTime,
  localNow: () => Date.now(),
  perf: () => (typeof performance !== 'undefined' ? performance.now() : Date.now()),
  warn: (message, error) => console.warn(message, error ?? ''),
  hidden: () => typeof document !== 'undefined' && document.hidden,
  onVisible: (handler) => {
    if (typeof document === 'undefined') return () => {}
    const listener = () => {
      if (!document.hidden) handler()
    }
    document.addEventListener('visibilitychange', listener)
    return () => document.removeEventListener('visibilitychange', listener)
  },
})

/** 校准后的当前时刻（epoch 毫秒）；未启动/未成功过 = 本地钟。交易时刻语义一律走这里。 */
export function serverNow(): number {
  return live.serverNow()
}

/** serverNow 的 Date 形态（喂 sessionOf / detectBand 这类吃 Date 的判定）。 */
export function serverDate(): Date {
  return new Date(live.serverNow())
}

export function startServerClock(): void {
  live.start()
}
