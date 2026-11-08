/**
 * 回测交易回放纯函数（TSP TradeKlineModal / useListNav 移植，研究报告 §3.1）。
 *
 * 数据纪律（CLAUDE.md §8-6）：
 * - NT 的 BacktestTrade 是**逐笔成交**（side/price/time/pnl per row），不是 TSP 的回合制，
 *   所以回放只做「被点成交 + 同标的同窗全部成交」的现场还原，**不伪造回合配对**；
 * - 时间不可解析的成交不放上 K 线（宁缺不摆错位）；色值由调用方注入主题色，纯函数不依赖主题。
 */
import type { BacktestTrade } from '@/api/types/backtest'
import type { KlineLevel, KlineMarker } from '@/viz/NtKline.vue'
import { formatDate } from '@/utils/time'
import { fillSideTone, type FillSideTone } from './market-markers'

/** 纯函数配色注入（避免本模块反向依赖主题单例，便于单测）。 */
export interface ReplayColors {
  up: string
  down: string
  neutral: string
}

function toneColor(tone: FillSideTone, colors: ReplayColors): string {
  return tone === 'up' ? colors.up : tone === 'down' ? colors.down : colors.neutral
}

/** 图表窗口：被点交易日 ±before/after 个自然日 → {start,end}（'YYYY-MM-DD'，含两端）；日期非法 → null。 */
export function tradeWindow(date: string, before = 45, after = 20): { start: string; end: string } | null {
  // 形如之外还要语义合法：'2026-02-30' 这类伪日期会被 Date.parse 归一化成 3/2，必须回读拦截（评审 P2-2）。
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!m) return null
  const y = Number(m[1])
  const mo = Number(m[2])
  const d = Number(m[3])
  const t = Date.UTC(y, mo - 1, d)
  const dt = new Date(t)
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() !== mo - 1 || dt.getUTCDate() !== d) return null
  const day = 86_400_000
  return {
    start: new Date(t - before * day).toISOString().slice(0, 10),
    end: new Date(t + after * day).toISOString().slice(0, 10),
  }
}

/** 导航链条目：表格同序（索引即链位），不重排。 */
export interface TradeNavItem extends BacktestTrade {
  index: number
}

/** 导航链构建：保持传入顺序（与交易明细表默认序一致），索引即链位。 */
export function buildTradeNavItems(trades: readonly BacktestTrade[]): TradeNavItem[] {
  return trades.map((trade, index) => ({ ...trade, index }))
}

/** 链内步进：越界返回 null（由调用方决定是否提示「已是第一笔/最后一笔」）。 */
export function navStep(current: number, delta: number, total: number): number | null {
  if (total <= 0) return null
  const next = current + delta
  return next < 0 || next >= total ? null : next
}

/**
 * 同窗同标的成交 → K 线标记：买 arrowUp/belowBar、卖 arrowDown/aboveBar、方向未知 circle/inBar（中性色）。
 * 同日同向聚合为一条并缀「×N」（密集成交不糊图）；按（交易日, 方向）升序输出。
 * 时间不可解析的成交被排除（不摆错位）。
 */
export function tradeMarkers(
  trades: readonly BacktestTrade[],
  opts: { symbol: string; start: string; end: string; colors: ReplayColors },
): KlineMarker[] {
  const code = opts.symbol
  if (!code) return []
  const groups = new Map<string, { time: string; side: string; count: number }>()
  for (const trade of trades) {
    if (trade.symbol !== code) continue
    // 与 K 线落轴同口径（chartTime 日线=上海历 'YYYY-MM-DD'）；不可解析的成交排除，不摆错位。
    const date = formatDate(trade.time)
    if (!date) continue
    if (opts.start && date < opts.start) continue
    if (opts.end && date > opts.end) continue
    const key = `${date}|${trade.side}`
    const hit = groups.get(key)
    if (hit) hit.count += 1
    else groups.set(key, { time: date, side: trade.side, count: 1 })
  }
  const label = (side: string) => (side === 'buy' ? '买' : side === 'sell' ? '卖' : '·')
  return [...groups.values()]
    .sort((a, b) => (a.time === b.time ? a.side.localeCompare(b.side) : a.time.localeCompare(b.time)))
    .map((g) => {
      const tone = fillSideTone(g.side)
      const isBuy = g.side === 'buy'
      const isSell = g.side === 'sell'
      return {
        time: g.time,
        position: isSell ? 'aboveBar' : isBuy ? 'belowBar' : 'inBar',
        color: toneColor(tone, opts.colors),
        shape: isSell ? 'arrowDown' : isBuy ? 'arrowUp' : 'circle',
        text: g.count > 1 ? `${label(g.side)} ×${g.count}` : label(g.side),
      }
    })
}

/** 被点成交价水平线：买 up 色虚线 / 卖 down 色虚线 / 方向未知中性；价非法或 ≤0 → 不画（缺省不显线）。 */
export function tradeCostLines(trade: BacktestTrade, colors: ReplayColors): KlineLevel[] {
  const p = trade.price
  if (p == null || !Number.isFinite(p) || p <= 0) return []
  const title = trade.side === 'buy' ? '买价' : trade.side === 'sell' ? '卖价' : '成交价'
  return [{ price: p, color: toneColor(fillSideTone(trade.side), colors), title, lineStyle: 'dashed' }]
}
