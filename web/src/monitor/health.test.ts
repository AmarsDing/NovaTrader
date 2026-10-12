import { describe, expect, it } from 'vitest'
import { computeHealth, healthTone } from './health'
import type { MonitorInput } from './types'

/**
 * 全部健康的输入：行情时钟贴近 now，总分 95。
 * 模拟盘的交易通道固定扣 5（§4.2），所以模拟盘满分就是 95——这是设计，不是缺陷。
 */
function healthyInput(): MonitorInput {
  return {
    now: 60_000,
    focusSymbol: '',
    account: { ok: true, book: 'SIM', equity: 100, cash: 100, dayPnl: 0, positions: [] },
    risk: { ok: true, mode: 'SIM', killActive: false, killReason: '', buysToday: 0, marketBreaker: '' },
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
    sentiment: null,
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
    expect(health.parts).toHaveLength(5)
    expect(health.parts.map((part) => part.key)).toEqual(['fresh', 'services', 'model', 'risk', 'channel'])
    const deductions = Object.fromEntries(health.parts.map((part) => [part.key, part.deduction]))
    expect(deductions).toEqual({ fresh: 0, services: 0, model: 0, risk: 0, channel: 5 })
  })

  it('数据新鲜度：每超 10s 扣 4，上限 20', () => {
    const base = healthyInput()
    const age = (seconds: number, now = 120_000) => {
      const input = structuredClone(base)
      input.sources!.items[0].lastAt = new Date(now - seconds * 1000).toISOString()
      return computeHealth(input, now).parts.find((part) => part.key === 'fresh')!
    }
    expect(age(9).deduction).toBe(0)
    expect(age(10).deduction).toBe(4)
    expect(age(35).deduction).toBe(12)
    expect(age(55).deduction).toBe(20)
    expect(age(900).deduction).toBe(20)
    expect(age(35).note).toContain('35')
  })

  it('数据新鲜度：没有任何行情时钟时记未判定而不是瞎扣', () => {
    const input = healthyInput()
    input.sources = { ok: true, items: [{ id: 'news', name: '新闻源', status: 'ok', detail: '', lastAt: '' }] }
    const fresh = computeHealth(input, 10_000).parts.find((part) => part.key === 'fresh')!
    expect(fresh.deduction).toBe(0)
    expect(fresh.measured).toBe(false)
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

  it('风控在线：读不到扣 15', () => {
    const input = healthyInput()
    input.risk = null
    const health = computeHealth(input, 60_000)
    expect(health.parts.find((item) => item.key === 'risk')!.deduction).toBe(15)
    expect(health.score).toBe(80)
  })

  it('交易通道：模拟盘扣 5，实盘且文件单探针未接入按不可达扣 25', () => {
    const sim = computeHealth(healthyInput(), 60_000).parts.find((item) => item.key === 'channel')!
    expect(sim.deduction).toBe(5)
    expect(sim.note).toContain('模拟')

    const live = healthyInput()
    live.risk!.mode = 'LIVE'
    const livePart = computeHealth(live, 60_000).parts.find((item) => item.key === 'channel')!
    expect(livePart.deduction).toBe(25)
    expect(livePart.note).toContain('broker-files')
  })

  it('总分是扣分之和，tone 按阈值分档', () => {
    const input = healthyInput()
    input.risk = null
    input.ops!.services = [
      { name: 'admin', status: 'ok', detail: '' },
      { name: 'nats', status: 'down', detail: '' },
      { name: 'redis', status: 'down', detail: '' },
      { name: 'trade', status: 'down', detail: '' },
    ]
    const health = computeHealth(input, 60_000)
    /* 95 - 风控 15 - 服务 18(3×6) = 62，落入 warn 档 */
    expect(health.score).toBe(62)
    expect(health.tone).toBe('warn')
  })

  it('最坏组合（实盘+全断）压到下限 15，不会再低', () => {
    const input = healthyInput()
    input.risk = { ok: true, mode: 'LIVE', killActive: false, killReason: '', buysToday: 0, marketBreaker: '' }
    input.ops!.services = Array.from({ length: 9 }, (_, index) => ({ name: `s${index}`, status: 'down', detail: '' }))
    input.brainStats!.tiers.push({ tier: 'small', model: 's', configured: true, circuitOpen: true, calls: 0, failures: 0 })
    input.brainStats!.tiers[0].p95Ms = 60_000
    input.sources!.items[0].lastAt = new Date(-3_600_000).toISOString()
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
