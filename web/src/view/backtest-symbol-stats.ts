/**
 * 回测分标的聚合（TSP picks 表移植，研究报告 §3.1）。
 *
 * 数据纪律（CLAUDE.md §8-6）：
 * - NT 的 BacktestTrade.pnl 仅**平仓腿**携带，建仓腿为 null —— null 腿不进盈亏统计（不冒充 0 盈亏）；
 * - 胜率分母 = 有 pnl 的腿数，分母 0 → null（显示面出「—」，不编 0% 也不编 100%）；
 * - 行序确定：合计盈亏降序（无 pnl 的标的沉底），同值按成交笔数降序，再按代码升序。
 */
import type { BacktestTrade } from '@/api/types/backtest'

export interface SymbolStat {
  symbol: string
  /** 全部成交笔数（含建仓腿）。 */
  fills: number
  buys: number
  sells: number
  /** 有 pnl 腿的盈亏合计；无 → null。 */
  closedPnl: number | null
  /** 有 pnl 腿中 pnl>0 的占比；分母 0 → null。 */
  winRate: number | null
  /** closedPnl / 有 pnl 腿数；分母 0 → null。 */
  avgPnl: number | null
  /** 该标的在入参数组中的最后一笔下标（供「定位最近一笔」跳转）；无 → -1。 */
  lastIndex: number
}

export function aggregateSymbolStats(trades: readonly BacktestTrade[]): SymbolStat[] {
  interface Acc {
    symbol: string
    fills: number
    buys: number
    sells: number
    closedPnl: number
    wins: number
    closedLegs: number
    lastIndex: number
  }
  const map = new Map<string, Acc>()
  trades.forEach((trade, index) => {
    const symbol = trade.symbol
    if (!symbol) return
    let acc = map.get(symbol)
    if (!acc) {
      acc = { symbol, fills: 0, buys: 0, sells: 0, closedPnl: 0, wins: 0, closedLegs: 0, lastIndex: -1 }
      map.set(symbol, acc)
    }
    acc.fills += 1
    if (trade.side === 'buy') acc.buys += 1
    else if (trade.side === 'sell') acc.sells += 1
    if (trade.pnl != null && Number.isFinite(trade.pnl)) {
      acc.closedPnl += trade.pnl
      acc.closedLegs += 1
      if (trade.pnl > 0) acc.wins += 1
    }
    acc.lastIndex = index
  })
  return [...map.values()]
    .map<SymbolStat>((acc) => ({
      symbol: acc.symbol,
      fills: acc.fills,
      buys: acc.buys,
      sells: acc.sells,
      closedPnl: acc.closedLegs > 0 ? acc.closedPnl : null,
      winRate: acc.closedLegs > 0 ? acc.wins / acc.closedLegs : null,
      avgPnl: acc.closedLegs > 0 ? acc.closedPnl / acc.closedLegs : null,
      lastIndex: acc.lastIndex,
    }))
    .sort((a, b) => {
      const pa = a.closedPnl
      const pb = b.closedPnl
      if (pa == null && pb == null) return b.fills - a.fills || a.symbol.localeCompare(b.symbol)
      if (pa == null) return 1
      if (pb == null) return -1
      if (pb !== pa) return pb - pa
      return b.fills - a.fills || a.symbol.localeCompare(b.symbol)
    })
}
