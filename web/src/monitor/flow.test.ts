import { describe, expect, it } from 'vitest'
import {
  AMBIENT_FAST_MS,
  AMBIENT_FIXED_MS,
  AMBIENT_SLOW_MS,
  CounterFlow,
  FLOW_FIXED_WIDTH,
  FLOW_WINDOW_MS,
  HIGH_SCORE,
  KILL_EDGE_ID,
  MODEL_EDGE_KEYS,
  PulseFlow,
  ambientPeriod,
  flowWidth,
  tierFor,
} from './flow'

const T0 = 1_700_000_000_000

describe('flowWidth（§3.2 束线流量映射）', () => {
  it('没有观测 → 固定 1.3px，不猜', () => {
    expect(flowWidth(null)).toBe(FLOW_FIXED_WIDTH)
    expect(flowWidth(Number.NaN)).toBe(FLOW_FIXED_WIDTH)
  })

  it('0 条/分 → 0.8px（log10(1)=0）', () => {
    expect(flowWidth(0)).toBeCloseTo(0.8, 10)
    expect(flowWidth(-5)).toBeCloseTo(0.8, 10)
  })

  it('按 log10 映射：9 → 1.7，99 → 2.6', () => {
    expect(flowWidth(9)).toBeCloseTo(1.7, 10)
    expect(flowWidth(99)).toBeCloseTo(2.6, 10)
  })

  it('封顶 0.8+2.4=3.2px', () => {
    expect(flowWidth(999)).toBeCloseTo(3.2, 10)
    expect(flowWidth(1e9)).toBeCloseTo(3.2, 10)
  })

  it('单调不减', () => {
    let prev = flowWidth(0)
    for (const rate of [1, 5, 20, 100, 500, 5000]) {
      const next = flowWidth(rate)
      expect(next).toBeGreaterThanOrEqual(prev)
      prev = next
    }
  })
})

describe('ambientPeriod（§3.2 周期由速率映射 1200–8000ms）', () => {
  it('估不出 → 固定 5200ms（§11 行 6），不外推', () => {
    expect(ambientPeriod(null)).toBe(AMBIENT_FIXED_MS)
    expect(ambientPeriod(Number.NaN)).toBe(AMBIENT_FIXED_MS)
  })

  it('0 条/分 → 最慢 8000ms；三个数量级 → 最快 1200ms', () => {
    expect(ambientPeriod(0)).toBe(AMBIENT_SLOW_MS)
    expect(ambientPeriod(999)).toBeCloseTo(AMBIENT_FAST_MS, 6)
  })

  it('再快也钳在 1200ms', () => {
    expect(ambientPeriod(1e6)).toBe(AMBIENT_FAST_MS)
  })

  it('速率越高周期越短', () => {
    expect(ambientPeriod(10)).toBeLessThan(ambientPeriod(1))
    expect(ambientPeriod(1000)).toBeLessThan(ambientPeriod(10))
  })
})

describe('CounterFlow（累计计数器差分）', () => {
  it('没有样本 / 只有一颗样本 → null', () => {
    const flow = new CounterFlow()
    expect(flow.rate('a', T0)).toBeNull()
    flow.sample('a', 100, T0)
    expect(flow.rate('a', T0 + 30_000)).toBeNull()
  })

  it('跨度不足 20s → null（一拍抖动不放大成速率）', () => {
    const flow = new CounterFlow()
    flow.sample('a', 100, T0)
    flow.sample('a', 160, T0 + 10_000)
    expect(flow.rate('a', T0 + 10_000)).toBeNull()
  })

  it('差分除以分钟数 = 每分钟条数', () => {
    const flow = new CounterFlow()
    flow.sample('a', 100, T0)
    flow.sample('a', 130, T0 + 15_000)
    flow.sample('a', 160, T0 + 30_000)
    /* 60 条 / 0.5 分钟 = 120 条/分 */
    expect(flow.rate('a', T0 + 30_000)).toBeCloseTo(120, 6)
  })

  it('计数没动 → 0（断流的判据）', () => {
    const flow = new CounterFlow()
    flow.sample('a', 42, T0)
    flow.sample('a', 42, T0 + 30_000)
    expect(flow.rate('a', T0 + 30_000)).toBe(0)
  })

  it('计数回退（后端重启）→ 旧差分作废，重新攒够才给数', () => {
    const flow = new CounterFlow()
    flow.sample('a', 500, T0)
    flow.sample('a', 620, T0 + 30_000)
    expect(flow.rate('a', T0 + 30_000)).toBeCloseTo(240, 6)
    flow.sample('a', 3, T0 + 40_000)
    expect(flow.rate('a', T0 + 40_000)).toBeNull()
    flow.sample('a', 30, T0 + 70_000)
    expect(flow.rate('a', T0 + 70_000)).toBeCloseTo(54, 6)
  })

  it('太久没喂新样本（页面藏起/轮询停了）→ null，不拿旧速率冒充现在', () => {
    const flow = new CounterFlow()
    flow.sample('a', 100, T0)
    flow.sample('a', 200, T0 + 30_000)
    expect(flow.rate('a', T0 + 30_000)).toBeCloseTo(200, 6)
    expect(flow.rate('a', T0 + 30_000 + FLOW_WINDOW_MS * 2 + 1)).toBeNull()
  })

  it('只留窗口内样本：10s 一拍喂 12 拍，速率按最近 60s 算', () => {
    const flow = new CounterFlow()
    for (let i = 0; i <= 12; i += 1) flow.sample('a', 1000 + i * 10, T0 + i * 10_000)
    const at = T0 + 120_000
    /* 窗口里首末样本差 60s、计数差 60 条 → 60 条/分 */
    expect(flow.rate('a', at)).toBeCloseTo(60, 6)
  })

  it('null 样本直接丢弃，不进环', () => {
    const flow = new CounterFlow()
    flow.sample('a', null, T0)
    flow.sample('a', Number.NaN, T0 + 10_000)
    flow.sample('a', 7, T0 + 20_000)
    expect(flow.rate('a', T0 + 20_000)).toBeNull()
  })
})

describe('PulseFlow（WS 脉冲计数）', () => {
  it('没声明观测的边 → null，不拿 0 冒充', () => {
    const flow = new PulseFlow()
    flow.record('x', T0)
    expect(flow.rate('x', T0 + FLOW_WINDOW_MS + 1)).toBeNull()
  })

  it('窗口没攒满 → null，不拿半窗外推', () => {
    const flow = new PulseFlow()
    flow.observe(KILL_EDGE_ID, T0)
    flow.record(KILL_EDGE_ID, T0 + 1000)
    expect(flow.rate(KILL_EDGE_ID, T0 + 30_000)).toBeNull()
  })

  it('窗口满了 → 数窗口内条数（每分钟）', () => {
    const flow = new PulseFlow()
    flow.observe(KILL_EDGE_ID, T0)
    flow.record(KILL_EDGE_ID, T0 + 61_000)
    flow.record(KILL_EDGE_ID, T0 + 62_000)
    flow.record(KILL_EDGE_ID, T0 + 63_000)
    expect(flow.rate(KILL_EDGE_ID, T0 + 64_000)).toBeCloseTo(3, 6)
  })

  it('窗口外的旧脉冲不计入', () => {
    const flow = new PulseFlow()
    flow.observe(KILL_EDGE_ID, T0)
    flow.record(KILL_EDGE_ID, T0 + 1000)
    const at = T0 + FLOW_WINDOW_MS + 5000
    expect(flow.rate(KILL_EDGE_ID, at)).toBe(0)
  })

  it('观测过但一条没来 → 0（诚实的零，不是 null）', () => {
    const flow = new PulseFlow()
    flow.observe(KILL_EDGE_ID, T0)
    expect(flow.rate(KILL_EDGE_ID, T0 + FLOW_WINDOW_MS + 1)).toBe(0)
  })
})

describe('模型档映射', () => {
  const tiers = [
    { tier: 'large', circuitOpen: false, calls: 12 },
    { tier: 'small', circuitOpen: true, calls: 200 },
  ]

  it('按 tier 名挑档', () => {
    expect(tierFor(tiers, 'large')?.calls).toBe(12)
    expect(tierFor(tiers, 'small')?.circuitOpen).toBe(true)
  })

  it('名字对不上按位置兜底（与 scene-model 同口径）', () => {
    const odd = [{ tier: 'a', calls: 1 }, { tier: 'b', calls: 2 }]
    expect(tierFor(odd, 'large')?.calls).toBe(1)
    expect(tierFor(odd, 'small')?.calls).toBe(2)
  })

  it('空表 → undefined，不猜', () => {
    expect(tierFor([], 'large')).toBeUndefined()
    expect(tierFor([], 'small')).toBeUndefined()
  })

  it('模型边键与 canvas-stage MODEL_LINKS 同口径；急停边 = 风控→账户', () => {
    expect(MODEL_EDGE_KEYS.small).toEqual(['model:small>dim:tech', 'model:small>dim:capital'])
    expect(MODEL_EDGE_KEYS.large).toEqual(['model:large>dim:news', 'model:large>dim:overseas', 'model:large>dim:synth'])
    expect(KILL_EDGE_ID).toBe('post:risk>book:center')
    expect(HIGH_SCORE).toBe(85)
  })
})
