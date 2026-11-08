/**
 * 行情关键价位纯函数（TSP AnalysisKChart LEVEL_GROUPS 移植，方案 B2 §D4/D6）。
 *
 * - 全部从 bars 纯计算（毫秒级，无请求）；返回四组 KlineLevel（颜色留给调用方按组注入主题色，
 *   与 withCostLine「色由调用方传主题色」先例一致）；
 * - 缺腿守卫：open/high/low/close 任一缺失的 K 线跳过该根，不编价（数据纪律）；
 * - 涨跌色纪律：价位线不用 up/down（up/down 仅价格涨跌语义）——组色见 MarketView 注入。
 */
import type { Bar } from '@/api/types/market'
import { formatDate } from '@/utils/time'
import type { LevelLike } from '@/utils/kline-transform'

export interface KeyLevelOptions {
  /** 前高前低长窗（默认 60 根）。 */
  prevWindow?: number
  /** 前高前低短窗（默认 20 根）。 */
  shortWindow?: number
  /** 缺口回看根数（默认 60）。 */
  gapLookback?: number
  /** 缺口幅度阈值（默认 0.005 = ±0.5%）。 */
  gapThreshold?: number
  /** 未回补缺口最多输出条数（默认 5，取最近）。 */
  gapMax?: number
  /** 整数关口上下各档数（默认 3，共 6 条线）。 */
  roundSpan?: number
}

export interface KeyLevelGroups {
  prevHL: LevelLike[]
  pivots: LevelLike[]
  gaps: LevelLike[]
  round: LevelLike[]
}

/** 四价齐全的 K 线投影；缺腿（任一 null/NaN）整根跳过。 */
interface OhlcPoint {
  time: string
  open: number
  high: number
  low: number
  close: number
}

function ohlc(bars: readonly Bar[]): OhlcPoint[] {
  const out: OhlcPoint[] = []
  for (const bar of bars) {
    const { open, high, low, close } = bar
    if (open == null || high == null || low == null || close == null) continue
    if (![open, high, low, close].every(Number.isFinite)) continue
    out.push({ time: bar.time, open, high, low, close })
  }
  return out
}

/** 前高前低：长窗（默认 60 根）+ 短窗（默认 20 根）各一对；同价去重（长窗口径优先）。 */
export function prevHighLow(bars: readonly Bar[], prevWindow = 60, shortWindow = 20): LevelLike[] {
  const pts = ohlc(bars)
  if (!pts.length) return []
  const win = pts.slice(-Math.max(1, prevWindow))
  const hi = Math.max(...win.map((p) => p.high))
  const lo = Math.min(...win.map((p) => p.low))
  const out: LevelLike[] = [
    { price: hi, title: `${prevWindow}日高`, lineStyle: 'dashed' },
    { price: lo, title: `${prevWindow}日低`, lineStyle: 'dashed' },
  ]
  const shortN = Math.max(1, Math.min(shortWindow, prevWindow))
  if (shortN < prevWindow && pts.length > shortN) {
    const short = pts.slice(-shortN)
    const hi2 = Math.max(...short.map((p) => p.high))
    const lo2 = Math.min(...short.map((p) => p.low))
    if (hi2 !== hi) out.push({ price: hi2, title: `${shortWindow}日高`, lineStyle: 'dotted' })
    if (lo2 !== lo) out.push({ price: lo2, title: `${shortWindow}日低`, lineStyle: 'dotted' })
  }
  return out
}

/** 经典枢轴：取最后一根四价齐全的日 K 的 H/L/C → P=(H+L+C)/3、R1=2P−L、S1=2P−H。 */
export function classicPivots(bars: readonly Bar[]): LevelLike[] {
  const pts = ohlc(bars)
  const ref = pts[pts.length - 1]
  if (!ref) return []
  const p = (ref.high + ref.low + ref.close) / 3
  return [
    { price: p, title: '枢轴P', lineStyle: 'solid' },
    { price: 2 * p - ref.low, title: '枢轴R1', lineStyle: 'dotted' },
    { price: 2 * p - ref.high, title: '枢轴S1', lineStyle: 'dotted' },
  ]
}

/**
 * 缺口：今开 vs 昨收幅度超阈值（默认 ±0.5%）记缺口；回看窗内（默认 60 根）宽松口径判回补——
 * 后续任一根 K 线价格区 [low,high] 覆盖缺口区即回补；取最近未回补 ≤gapMax 条。
 * 价位取缺口「守边」：上跳缺口守下沿（昨收），下跳缺口守上沿（昨收）。
 */
export function unfilledGaps(
  bars: readonly Bar[],
  lookback = 60,
  threshold = 0.005,
  max = 5,
): LevelLike[] {
  const pts = ohlc(bars).slice(-Math.max(2, lookback + 1))
  const out: LevelLike[] = []
  for (let i = 1; i < pts.length; i += 1) {
    const prev = pts[i - 1]
    const cur = pts[i]
    if (!prev.close) continue
    const up = cur.open > prev.close * (1 + threshold)
    const down = cur.open < prev.close * (1 - threshold)
    if (!up && !down) continue
    const zoneLo = up ? prev.close : cur.open
    const zoneHi = up ? cur.open : prev.close
    const filled = pts.slice(i + 1).some((b) => b.low <= zoneLo && b.high >= zoneHi)
    if (filled) continue
    out.push({
      price: prev.close,
      title: `缺口 ${formatDate(cur.time) || '—'}`,
      lineStyle: 'dotted',
    })
  }
  return out.slice(-Math.max(1, max))
}

/** 整数关口步长自适应：1/5/10/50/100 档（按现价位阶）。 */
export function roundStep(price: number): number {
  const p = Math.abs(price)
  if (p >= 500) return 100
  if (p >= 100) return 50
  if (p >= 50) return 10
  if (p >= 10) return 5
  return 1
}

/** 整数关口：以最近一根收盘价为锚，上下各 span 档（默认 ±3，共 6 条），不含锚所在关口本身。 */
export function roundLevels(bars: readonly Bar[], span = 3): LevelLike[] {
  const pts = ohlc(bars)
  const ref = pts[pts.length - 1]
  if (!ref || !(ref.close > 0)) return []
  const step = roundStep(ref.close)
  const anchor = Math.round(ref.close / step)
  const out: LevelLike[] = []
  for (let k = -Math.max(1, span); k <= Math.max(1, span); k += 1) {
    if (k === 0) continue
    const price = (anchor + k) * step
    if (price <= 0) continue
    out.push({ price, title: `关口 ${price}`, lineStyle: 'dotted' })
  }
  return out
}

/** 四组汇总（跨组同价去重：prevHL → pivots → gaps → round 优先级）。 */
export function computeKeyLevels(bars: readonly Bar[], opts: KeyLevelOptions = {}): KeyLevelGroups {
  const groups: KeyLevelGroups = {
    prevHL: prevHighLow(bars, opts.prevWindow ?? 60, opts.shortWindow ?? 20),
    pivots: classicPivots(bars),
    gaps: unfilledGaps(bars, opts.gapLookback ?? 60, opts.gapThreshold ?? 0.005, opts.gapMax ?? 5),
    round: roundLevels(bars, opts.roundSpan ?? 3),
  }
  const seen = new Set<number>()
  for (const key of ['prevHL', 'pivots', 'gaps', 'round'] as const) {
    groups[key] = groups[key].filter((level) => {
      if (seen.has(level.price)) return false
      seen.add(level.price)
      return true
    })
  }
  return groups
}
