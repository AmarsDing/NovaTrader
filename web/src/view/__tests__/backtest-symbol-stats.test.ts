import { describe, expect, it } from 'vitest'
import type { BacktestTrade } from '@/api/types/backtest'
import { aggregateSymbolStats } from '../backtest-symbol-stats'

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

describe('aggregateSymbolStats', () => {
  it('笔数/买卖分计；pnl null 腿不进盈亏统计', () => {
    const stats = aggregateSymbolStats([
      trade({ symbol: 'A', side: 'buy' }),
      trade({ symbol: 'A', side: 'sell', pnl: 100 }),
      trade({ symbol: 'A', side: 'buy' }),
    ])
    const a = stats.find((s) => s.symbol === 'A')!
    expect(a.fills).toBe(3)
    expect(a.buys).toBe(2)
    expect(a.sells).toBe(1)
    expect(a.closedPnl).toBe(100)
    expect(a.winRate).toBe(1) // 分母=有 pnl 腿数=1，赢 1
    expect(a.avgPnl).toBe(100)
  })

  it('胜率分母=有 pnl 腿数；pnl=0 计入分母不计入赢', () => {
    const stats = aggregateSymbolStats([
      trade({ symbol: 'A', pnl: 10 }),
      trade({ symbol: 'A', pnl: 0 }),
      trade({ symbol: 'A', pnl: -5 }),
      trade({ symbol: 'A', pnl: null }),
    ])
    const a = stats[0]
    expect(a.closedPnl).toBe(5)
    expect(a.winRate).toBe(1 / 3)
    expect(a.avgPnl).toBeCloseTo(5 / 3)
  })

  it('全为建仓腿（无 pnl）→ 盈亏/胜率/均盈亏全 null（显示面出「—」，不编 0）', () => {
    const stats = aggregateSymbolStats([trade({ symbol: 'A' }), trade({ symbol: 'A', side: 'sell' })])
    expect(stats[0]).toMatchObject({ closedPnl: null, winRate: null, avgPnl: null, fills: 2 })
  })

  it('lastIndex 记该标的最后一笔下标（供定位最近一笔）', () => {
    const stats = aggregateSymbolStats([
      trade({ symbol: 'A' }),
      trade({ symbol: 'B' }),
      trade({ symbol: 'A', side: 'sell', pnl: 1 }),
    ])
    expect(stats.find((s) => s.symbol === 'A')!.lastIndex).toBe(2)
    expect(stats.find((s) => s.symbol === 'B')!.lastIndex).toBe(1)
  })

  it('行序：合计盈亏降序，无 pnl 沉底；空代码跳过', () => {
    const stats = aggregateSymbolStats([
      trade({ symbol: 'A', pnl: 50 }),
      trade({ symbol: 'B', pnl: 200 }),
      trade({ symbol: 'C' }),
      trade({ symbol: '' }),
    ])
    expect(stats.map((s) => s.symbol)).toEqual(['B', 'A', 'C'])
  })

  it('空输入 → 空数组', () => {
    expect(aggregateSymbolStats([])).toEqual([])
  })
})
