import { describe, expect, it } from 'vitest'
import { buildFunnel, funnelCountText, funnelRate, funnelWidths, FUNNEL_MAX_W, FUNNEL_MIN_W } from './funnel'

/** 中文口径测试桩：真实实现走 format.missLabel，这里只验分组与取标签逻辑 */
const labelOf = (code: string) => `#${code}`

function row(stage: string, missReason: string, aiScore: number | null) {
  return { stage, missReason, aiScore }
}

describe('buildFunnel 六层数字', () => {
  it('全市场层只读拿不到，恒为未返回（null），不 0 填充', () => {
    const funnel = buildFunnel({ candidates: { ok: true, items: [] } }, labelOf)
    expect(funnel.layers[0]).toEqual({ key: 'universe', name: '全市场', count: null })
  })

  it('初筛=行数，AI≥70=aiScore≥70，风控通过=stage selected，边界 70 算过线', () => {
    const items = [
      row('selected', '', 92),
      row('ai_scored', 'below_threshold', 70),
      row('ai_scored', 'below_threshold', 69.5),
      row('ranked', 'not_in_ai_top', null),
    ]
    const funnel = buildFunnel({ candidates: { ok: true, items } }, labelOf)
    expect(funnel.layers.map((layer) => layer.count)).toEqual([null, 4, 2, 1, null, null])
  })

  it('已下单/成交只数买单：sell/卖 不入漏斗，buy/买/买入 都算', () => {
    const funnel = buildFunnel(
      {
        candidates: { ok: true, items: [row('selected', '', 88)] },
        orders: { ok: true, items: [{ side: 'buy' }, { side: 'BUY' }, { side: '卖' }, { side: 'sell' }] },
        fills: { ok: true, items: [{ side: '买入' }, { side: '买' }, { side: 'sell' }] },
      },
      labelOf,
    )
    expect(funnel.layers[4]).toEqual({ key: 'ordered', name: '已下单', count: 2 })
    expect(funnel.layers[5]).toEqual({ key: 'filled', name: '成交', count: 2 })
  })

  it('接口失败（ok:false）对应层为 null 未返回，不猜数字', () => {
    const funnel = buildFunnel({ candidates: { ok: false, items: [] }, orders: { ok: false, items: [] }, fills: null }, labelOf)
    expect(funnel.layers.every((layer) => layer.count === null)).toBe(true)
  })

  it('候选完全缺席（undefined）时同样全 null', () => {
    const funnel = buildFunnel({}, labelOf)
    expect(funnel.layers.map((layer) => layer.count)).toEqual([null, null, null, null, null, null])
  })
})

describe('buildFunnel 侧漏', () => {
  it('初筛→AI 侧漏按 pre-AI 原因分组，标签取最大项', () => {
    const items = [
      row('ranked', 'no_quote', null),
      row('ranked', 'no_quote', null),
      row('ranked', 'held', null),
      row('ranked', 'cooldown', null),
    ]
    const funnel = buildFunnel({ candidates: { ok: true, items } }, labelOf)
    const leak = funnel.leaks.find((item) => item.at === 2)
    expect(leak?.count).toBe(4)
    expect(leak?.label).toBe('#no_quote')
    expect(leak?.reasons.map((reason) => reason.label)).toEqual(['#no_quote', '#held', '#cooldown'])
  })

  it('AI→风控侧漏只收规则层原因，两组各自独立', () => {
    const items = [row('ai_scored', 'below_threshold', 74), row('ai_scored', 'daily_cap', 81), row('ranked', 'no_quote', null)]
    const funnel = buildFunnel({ candidates: { ok: true, items } }, labelOf)
    const rule = funnel.leaks.find((item) => item.at === 3)
    expect(rule?.count).toBe(2)
    expect(funnel.leaks.find((item) => item.at === 2)?.count).toBe(1)
  })

  it('下单/成交侧漏用相邻层差值，差 ≤0 不画', () => {
    const both = buildFunnel(
      {
        candidates: { ok: true, items: [row('selected', '', 90), row('selected', '', 85), row('selected', '', 80)] },
        orders: { ok: true, items: [{ side: 'buy' }] },
        fills: { ok: true, items: [{ side: 'buy' }] },
      },
      labelOf,
    )
    expect(both.leaks.find((item) => item.at === 4)).toEqual({ at: 4, count: 2, label: '尚未下单', reasons: [] })
    expect(both.leaks.find((item) => item.at === 5)).toBeUndefined()

    const over = buildFunnel(
      {
        candidates: { ok: true, items: [row('selected', '', 90)] },
        orders: { ok: true, items: [{ side: 'buy' }, { side: 'buy' }] },
        fills: { ok: true, items: [{ side: 'buy' }, { side: 'buy' }] },
      },
      labelOf,
    )
    /* 手工单/历史单可能多于当日风控通过数：差为负不画侧漏，也不编原因 */
    expect(over.leaks.filter((item) => item.at >= 4)).toEqual([])
  })

  it('候选接口失败时不产原因侧漏（没有行就没有原因，不编）', () => {
    const funnel = buildFunnel({ candidates: { ok: false, items: [] } }, labelOf)
    expect(funnel.leaks).toEqual([])
  })

  it('命中接口默认上限 200 条时 note 提醒数字是下限', () => {
    const items = Array.from({ length: 200 }, () => row('ranked', 'no_quote', null))
    const funnel = buildFunnel({ candidates: { ok: true, items } }, labelOf)
    expect(funnel.note).toContain('200')
    const small = buildFunnel({ candidates: { ok: true, items: items.slice(0, 199) } }, labelOf)
    expect(small.note).toBe('')
  })
})

describe('funnelWidths：sqrt 映射不是线性', () => {
  it('最大层 96，其余按 sqrt 收缩，下限 12，null 原样透传', () => {
    const widths = funnelWidths([null, 5120, 486, 97, 23, 8])
    expect(widths[0]).toBeNull()
    expect(widths[1]).toBe(FUNNEL_MAX_W)
    /* 线性映射 486/5120 只有 ~9px（被下限钳成 12）；sqrt 映射后仍有 ~30px 可读厚度 */
    expect(widths[2]).toBeGreaterThanOrEqual(20)
    expect(widths[3]).toBeGreaterThanOrEqual(FUNNEL_MIN_W)
    expect(widths[4]).toBe(FUNNEL_MIN_W)
    for (const width of widths) {
      if (width != null) expect(width).toBeLessThanOrEqual(FUNNEL_MAX_W)
    }
  })

  it('全 0 或全缺失时不除零：已知层给最小厚', () => {
    expect(funnelWidths([0, 0, null])).toEqual([FUNNEL_MIN_W, FUNNEL_MIN_W, null])
    expect(funnelWidths([null, null])).toEqual([null, null])
  })

  it('单调不减：数量大的层不会比数量小的层薄', () => {
    const widths = funnelWidths([1000, 400, 90, 16, 4, 1])
    const known = widths.map((width) => width ?? 0)
    for (let index = 1; index < known.length; index += 1) expect(known[index]).toBeLessThanOrEqual(known[index - 1])
  })
})

describe('funnelRate / funnelCountText', () => {
  it('转化率一位小数；前层缺失或为 0 时不编率', () => {
    expect(funnelRate(486, 5120)).toBe('9.5%')
    expect(funnelRate(1, 3)).toBe('33.3%')
    expect(funnelRate(5, null)).toBeNull()
    expect(funnelRate(null, 5)).toBeNull()
    expect(funnelRate(5, 0)).toBeNull()
  })

  it('计数千分位；null 写「未返回」', () => {
    expect(funnelCountText(5120)).toBe('5,120')
    expect(funnelCountText(0)).toBe('0')
    expect(funnelCountText(null)).toBe('未返回')
  })
})
