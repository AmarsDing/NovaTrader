import { describe, expect, it } from 'vitest'
import type { BacktestTrade } from '@/api/types/backtest'
import { buildTradeNavItems, navStep, tradeCostLines, tradeMarkers, tradeWindow } from '../backtest-replay'

const COLORS = { up: '#up', down: '#down', neutral: '#neutral' }

function trade(over: Partial<BacktestTrade>): BacktestTrade {
  return {
    seq: null,
    symbol: '600519.SH',
    side: 'buy',
    time: '2026-01-05',
    price: 10,
    quantity: 100,
    amount: 1000,
    pnl: null,
    reason: '',
    ...over,
  }
}

describe('tradeWindow 窗口', () => {
  it('默认 ±45/20 自然日（含两端）', () => {
    expect(tradeWindow('2026-02-01')).toEqual({ start: '2025-12-18', end: '2026-02-21' })
  })

  it('可覆写前后跨度；非法日期 → null（含伪日期语义拦截）', () => {
    expect(tradeWindow('2026-02-01', 1, 1)).toEqual({ start: '2026-01-31', end: '2026-02-02' })
    expect(tradeWindow('')).toBeNull()
    expect(tradeWindow('2026/02/01')).toBeNull()
    // 伪日期：形如合法但语义非法，Date.parse 会归一化成 3/2——必须归 null（评审 P2-2 锁点）
    expect(tradeWindow('2026-02-30')).toBeNull()
    expect(tradeWindow('2026-13-01')).toBeNull()
    expect(tradeWindow('2026-01-32')).toBeNull()
  })
})

describe('buildTradeNavItems / navStep', () => {
  const trades = [trade({ seq: 1 }), trade({ seq: 2 }), trade({ seq: 3 })]
  it('保持传入顺序，索引即链位', () => {
    expect(buildTradeNavItems(trades).map((t) => t.index)).toEqual([0, 1, 2])
    expect(buildTradeNavItems(trades)[1].seq).toBe(2)
  })

  it('链内步进：越界 → null；空链 → null', () => {
    expect(navStep(1, 1, 3)).toBe(2)
    expect(navStep(1, -1, 3)).toBe(0)
    expect(navStep(0, -1, 3)).toBeNull()
    expect(navStep(2, 1, 3)).toBeNull()
    expect(navStep(0, 1, 0)).toBeNull()
  })
})

describe('tradeMarkers 现场还原', () => {
  it('只收同标的且在窗口内；时间不可解析的排除', () => {
    const cols = { symbol: '600519.SH', start: '2026-01-01', end: '2026-01-31', colors: COLORS }
    const marks = tradeMarkers(
      [
        trade({ time: '2026-01-05', side: 'buy' }),
        trade({ symbol: '000001.SZ', time: '2026-01-06', side: 'buy' }),
        trade({ time: '2025-12-31', side: 'sell' }),
        trade({ time: '坏时间', side: 'buy' }),
      ],
      cols,
    )
    expect(marks).toHaveLength(1)
    expect(marks[0].time).toBe('2026-01-05')
  })

  it('同日同向聚合 ×N；买卖颜色/位置/形状分派', () => {
    const cols = { symbol: '600519.SH', start: '2026-01-01', end: '2026-01-31', colors: COLORS }
    const marks = tradeMarkers(
      [
        trade({ time: '2026-01-05', side: 'buy' }),
        trade({ time: '2026-01-05', side: 'buy' }),
        trade({ time: '2026-01-05', side: 'sell' }),
      ],
      cols,
    )
    const buy = marks.find((m) => m.shape === 'arrowUp')!
    const sell = marks.find((m) => m.shape === 'arrowDown')!
    expect(buy).toMatchObject({ time: '2026-01-05', position: 'belowBar', color: '#up', text: '买 ×2' })
    expect(sell).toMatchObject({ position: 'aboveBar', color: '#down', text: '卖' })
  })

  it('方向未知走中性色圆点；输出按（日期,方向）升序', () => {
    const cols = { symbol: '600519.SH', start: '', end: '', colors: COLORS }
    const marks = tradeMarkers(
      [trade({ time: '2026-01-07', side: '' }), trade({ time: '2026-01-05', side: 'buy' })],
      cols,
    )
    expect(marks.map((m) => m.time)).toEqual(['2026-01-05', '2026-01-07'])
    expect(marks[1]).toMatchObject({ shape: 'circle', position: 'inBar', color: '#neutral', text: '·' })
  })

  it('空代码/无成交 → 空数组', () => {
    expect(tradeMarkers([trade({})], { symbol: '', start: '', end: '', colors: COLORS })).toEqual([])
  })
})

describe('tradeCostLines 成本线', () => {
  it('买 up 虚线 / 卖 down 虚线', () => {
    expect(tradeCostLines(trade({ side: 'buy', price: 12.34 }), COLORS)).toEqual([
      { price: 12.34, color: '#up', title: '买价', lineStyle: 'dashed' },
    ])
    expect(tradeCostLines(trade({ side: 'sell', price: 9.5 }), COLORS)[0]).toMatchObject({ color: '#down', title: '卖价' })
  })

  it('价缺失/非正/NaN → 不画线', () => {
    expect(tradeCostLines(trade({ price: null }), COLORS)).toEqual([])
    expect(tradeCostLines(trade({ price: 0 }), COLORS)).toEqual([])
    expect(tradeCostLines(trade({ price: Number.NaN }), COLORS)).toEqual([])
  })
})
