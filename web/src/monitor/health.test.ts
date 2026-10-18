import { describe, expect, it } from 'vitest'
import { computeHealth, healthTone, runAuthorizationText } from './health'
import type { MonitorInput } from './types'

/**
 * 已观测的情绪结果生成时间贴近 now，总分 95；不代表全市场报价都新鲜。
 * 模拟盘的交易通道固定扣 5（§4.2），所以模拟盘满分就是 95——这是设计，不是缺陷。
 */
function healthyInput(): MonitorInput {
  return {
    now: 60_000,
    focusSymbol: '',
    account: { ok: true, book: 'SIM', equity: 100, cash: 100, dayPnl: 0, positions: [] },
    risk: { ok: true, mode: 'L1', killActive: false, killReason: '', buysToday: 0, marketBreaker: '' },
    ops: {
      ok: true,
      services: [
        { name: 'admin', status: 'ok', detail: '' },
        { name: 'postgres', status: 'up', detail: '' },
        { name: 'nats', status: 'ok', detail: '' },
      ],
      sources: [],
    },
    sources: { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'ok', detail: '', lastAt: new Date(60_000 - 5_000).toISOString() }] },
    sentiment: { ok: true, phase: 'WARM', up: 10, down: 10, asOf: new Date(60_000 - 5_000).toISOString(), stale: false },
    sectors: null,
    limits: null,
    signals: null,
    briefing: null,
    hotWords: null,
    timeline: null,
    brainStats: {
      ok: true,
      tiers: [{ tier: 'large', model: 'm', configured: true, circuitOpen: false, calls: 3, failures: 0, p95Ms: 1200 }],
    },
    equityCurve: null,
    pulses: [],
  }
}

describe('computeHealth', () => {
  it('全健康时 95（模拟盘通道固定扣 5），五项都可解释', () => {
    const health = computeHealth(healthyInput(), 60_000)
    expect(health.score).toBe(95)
    expect(health.tone).toBe('accent')
    expect(health.measuredCount).toBe(5)
    expect(health.totalCount).toBe(5)
    expect(health.parts).toHaveLength(5)
    expect(health.parts.map((part) => part.key)).toEqual(['fresh', 'services', 'model', 'risk', 'channel'])
    const deductions = Object.fromEntries(health.parts.map((part) => [part.key, part.deduction]))
    expect(deductions).toEqual({ fresh: 0, services: 0, model: 0, risk: 0, channel: 5 })
  })

  it('市场结果生成时效：每超 10s 扣 4，上限 20', () => {
    const base = healthyInput()
    const age = (seconds: number, now = 120_000) => {
      const input = structuredClone(base)
      input.sentiment!.asOf = new Date(now - seconds * 1000).toISOString()
      return computeHealth(input, now).parts.find((part) => part.key === 'fresh')!
    }
    expect(age(9).deduction).toBe(0)
    expect(age(10).deduction).toBe(4)
    expect(age(35).deduction).toBe(12)
    expect(age(55).deduction).toBe(20)
    expect(age(900).deduction).toBe(20)
    expect(age(35).note).toContain('35')
  })

  it('只有采集成功时间没有业务结果时间时仍未判定', () => {
    const input = healthyInput()
    input.sentiment = null
    const fresh = computeHealth(input, 60_000).parts.find((part) => part.key === 'fresh')!
    expect(fresh.deduction).toBe(0)
    expect(fresh.measured).toBe(false)
    expect(fresh.note).toContain('源数据时钟未接入')
    expect(computeHealth(input, 60_000).score).toBeNull()
  })

  it('采集刚成功不能掩盖旧情绪结果，不宣称逐证券报价都新鲜', () => {
    const input = healthyInput()
    input.sources!.items[0].lastAt = new Date(60_000).toISOString()
    input.sentiment!.asOf = new Date(60_000 - 35_000).toISOString()
    const fresh = computeHealth(input, 60_000).parts.find((part) => part.key === 'fresh')!
    expect(fresh).toMatchObject({ label: '市场结果时效', measured: true, deduction: 12 })
    expect(fresh.note).toContain('结果生成距今 35s')
    expect(fresh.note).toContain('不代表逐证券报价时效')
  })

  it('结果 asOf 很近但 stale=true 时仍按明确陈旧扣分', () => {
    const input = healthyInput()
    input.sentiment!.stale = true
    const fresh = computeHealth(input, 60_000).parts.find((part) => part.key === 'fresh')!
    expect(fresh).toMatchObject({ measured: true, deduction: 20 })
    expect(fresh.note).toContain('已标记陈旧')
  })

  it('无效业务时间不回退采集或页面采样时钟', () => {
    const input = healthyInput()
    input.sampledAt = 60_000
    input.sentiment!.asOf = '不是业务时间'
    expect(computeHealth(input, 60_000).parts.find((part) => part.key === 'fresh')?.measured).toBe(false)
  })

  it('服务存活：每个非健康服务扣 6，上限 20', () => {
    const input = healthyInput()
    input.ops!.services = [
      { name: 'admin', status: 'ok', detail: '' },
      { name: 'nats', status: 'down', detail: '' },
      { name: 'redis', status: 'error', detail: '' },
      { name: 'trade', status: 'unavailable', detail: '' },
      { name: 'notify', status: 'stopped', detail: '' },
    ]
    const part = computeHealth(input, 10_000).parts.find((item) => item.key === 'services')!
    expect(part.deduction).toBe(20)
    expect(part.note).toContain('nats')
    expect(part.note).toContain('trade')
  })

  it('服务存活：运维状态整体没返回时记未判定', () => {
    const input = healthyInput()
    input.ops = null
    const part = computeHealth(input, 10_000).parts.find((item) => item.key === 'services')!
    expect(part.deduction).toBe(0)
    expect(part.measured).toBe(false)
  })

  it('模型可用：熔断扣 12，P95 每超 1s 扣 2，合计上限 20', () => {
    const base = healthyInput()
    const withCircuit = structuredClone(base)
    withCircuit.brainStats!.tiers.push({ tier: 'small', model: 's', configured: true, circuitOpen: true, calls: 0, failures: 0 })
    const circuitPart = computeHealth(withCircuit, 10_000).parts.find((item) => item.key === 'model')!
    expect(circuitPart.deduction).toBe(12)
    expect(circuitPart.note).toContain('熔断')

    const slow = structuredClone(base)
    slow.brainStats!.tiers[0].p95Ms = 8000
    const slowPart = computeHealth(slow, 10_000).parts.find((item) => item.key === 'model')!
    expect(slowPart.deduction).toBe(6)

    const both = structuredClone(base)
    both.brainStats!.tiers[0].p95Ms = 9000
    both.brainStats!.tiers.push({ tier: 'small', model: 's', configured: true, circuitOpen: true, calls: 0, failures: 0 })
    expect(computeHealth(both, 10_000).parts.find((item) => item.key === 'model')!.deduction).toBe(20)
  })

  it('风控状态未返回时标记缺测，不再把缺失当成总分 80', () => {
    const input = healthyInput()
    input.risk = null
    const health = computeHealth(input, 60_000)
    expect(health.parts.find((item) => item.key === 'risk')!.measured).toBe(false)
    expect(health.score).toBeNull()
    expect(health.measuredCount).toBe(4)
    expect(health.tone).toBe('warn')
  })

  it('交易通道使用账户 SIM/LIVE，L1/L2 运行授权不能代替账本身份', () => {
    const sim = computeHealth(healthyInput(), 60_000).parts.find((item) => item.key === 'channel')!
    expect(sim.deduction).toBe(5)
    expect(sim.note).toContain('模拟')

    const live = healthyInput()
    live.account!.book = 'LIVE'
    live.risk!.mode = 'L2'
    live.opsP1 = { ok: true, brokerFiles: { available: false, note: 'broker-gw 尚未部署', pending: null, written: null, consumed: null, leftover: null, lastOk: '' } }
    const livePart = computeHealth(live, 60_000).parts.find((item) => item.key === 'channel')!
    expect(livePart.deduction).toBe(25)
    expect(livePart.note).toContain('未接入')
    expect(livePart.note).not.toContain('模拟')
  })

  it('实盘探针可达与未返回分开：空探针不能当成模拟盘或已确认断线', () => {
    const live = healthyInput()
    live.account!.book = 'live'
    live.risk!.mode = 'L2'
    const unknown = computeHealth(live, 60_000)
    expect(unknown.score).toBeNull()
    expect(unknown.parts.find((part) => part.key === 'channel')).toMatchObject({ measured: false, deduction: 0 })

    live.opsP1 = { ok: true, brokerFiles: { available: true, note: '', pending: 0, written: 2, consumed: 2, leftover: 0, lastOk: '' } }
    const available = computeHealth(live, 60_000)
    expect(available.score).toBe(100)
    expect(available.parts.find((part) => part.key === 'channel')?.note).toContain('探针可达')
    expect(available.parts.find((part) => part.key === 'channel')?.note).toContain('真实回报')
  })

  it('交易服务明确 down 时才报告服务失联，探针可达也不能掩盖该故障', () => {
    const live = healthyInput()
    live.account!.book = 'LIVE'
    live.ops!.services.push({ name: 'trade', status: 'down', detail: 'health check failed' })
    live.opsP1 = { ok: true, brokerFiles: { available: true, note: '', pending: 0, written: 0, consumed: 0, leftover: 0, lastOk: '' } }
    expect(computeHealth(live, 60_000).parts.find((part) => part.key === 'channel')).toMatchObject({ measured: true, deduction: 25, note: expect.stringContaining('失联') })
  })

  it('全接口离线时没有健康总分，观测覆盖为 0/5', () => {
    const input = healthyInput()
    input.account = null
    input.risk = null
    input.ops = null
    input.sources = null
    input.sentiment = null
    input.brainStats = null
    const health = computeHealth(input, 60_000)
    expect(health.score).toBeNull()
    expect(health.measuredCount).toBe(0)
    expect(health.parts.every((part) => !part.measured)).toBe(true)
    expect(['text', 'accent']).not.toContain(health.tone)
  })

  it('运维/模型返回空列表仍然未观测，不冒充零故障', () => {
    const input = healthyInput()
    input.ops!.services = []
    input.brainStats!.tiers = []
    const health = computeHealth(input, 60_000)
    expect(health.score).toBeNull()
    expect(health.measuredCount).toBe(3)
  })

  it('账户身份缺失不能通过 L2 猜实盘，也不能按 SIM 兜底扣分', () => {
    const input = healthyInput()
    input.account = null
    input.risk!.mode = 'L2'
    expect(computeHealth(input, 60_000).parts.find((part) => part.key === 'channel')).toMatchObject({ measured: false, deduction: 0 })
  })

  it('总分是扣分之和，tone 按阈值分档', () => {
    const input = healthyInput()
    input.sentiment!.asOf = new Date(60_000 - 35_000).toISOString()
    input.ops!.services = [
      { name: 'admin', status: 'ok', detail: '' },
      { name: 'nats', status: 'down', detail: '' },
      { name: 'redis', status: 'down', detail: '' },
      { name: 'trade', status: 'down', detail: '' },
    ]
    const health = computeHealth(input, 60_000)
    /* 95 - 新鲜度 12 - 服务 18(3×6) = 65，落入 warn 档 */
    expect(health.score).toBe(65)
    expect(health.tone).toBe('warn')
  })

  it('最坏组合（实盘+全断）压到下限 15，不会再低', () => {
    const input = healthyInput()
    input.account!.book = 'LIVE'
    input.risk!.mode = 'L2'
    input.opsP1 = { ok: true, brokerFiles: { available: false, note: '未部署', pending: null, written: null, consumed: null, leftover: null, lastOk: '' } }
    input.ops!.services = Array.from({ length: 9 }, (_, index) => ({ name: `s${index}`, status: 'down', detail: '' }))
    input.brainStats!.tiers.push({ tier: 'small', model: 's', configured: true, circuitOpen: true, calls: 0, failures: 0 })
    input.brainStats!.tiers[0].p95Ms = 60_000
    input.sentiment!.asOf = new Date(-3_600_000).toISOString()
    /* 新鲜 20 + 服务 20 + 模型 20 + 通道 25 = 85，总分截断在 15 */
    expect(computeHealth(input, 0).score).toBe(15)
    expect(computeHealth(input, 0).tone).toBe('danger')
  })
})

describe('healthTone', () => {
  it('90/70/50 三档边界', () => {
    expect(healthTone(95)).toBe('accent')
    expect(healthTone(90)).toBe('accent')
    expect(healthTone(89)).toBe('text')
    expect(healthTone(70)).toBe('text')
    expect(healthTone(69)).toBe('warn')
    expect(healthTone(50)).toBe('warn')
    expect(healthTone(49)).toBe('danger')
    expect(healthTone(0)).toBe('danger')
  })
})

describe('runAuthorizationText', () => {
  it('L3 明确未开放且按 L2 人工确认；SIM/LIVE 不解释成运行授权', () => {
    expect(runAuthorizationText('L3')).toBe('L3 未开放（按 L2 人工确认）')
    expect(runAuthorizationText('L2')).toBe('L2 实盘人工确认')
    expect(runAuthorizationText('SIM')).toContain('未知运行授权')
    expect(runAuthorizationText('')).toBe('未返回')
  })
})
