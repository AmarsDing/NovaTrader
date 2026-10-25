// kline-transform.ts：K 线复权/周期切换纯函数（W6c；UI 宪法 R-UI-50/51）。
//
// 纪律：
//   - 纯函数零副作用：不改入参、不读时钟，输出完全由输入决定（图形回归可 golden 比对）；
//   - 「没取到≠0」：复权因子非法（<=0/NaN）原样返回不复权；聚合空输入→[]、无数据桶不补零桶；
//   - 复权不动量：open/high/low/close 缩放，volume 原样（成交量不复权是 A 股口径惯例）。

export interface BarLike {
  time: number
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export type AdjustMode = 'none' | 'qfq' | 'hfq'

/**
 * 单根复权。factor 为复权因子（qfq 口径：最新交易日=1，历史 <1；hfq 口径：上市日=1，越近越大）。
 * qfq：价 × factor；hfq：价 ÷ factor；none/非法 factor：原样（缺失不复权，不回填 0/1 冒充）。
 */
export function adjustBar<T extends BarLike>(bar: T, factor: number, mode: AdjustMode): T {
  if (mode === 'none' || !Number.isFinite(factor) || factor <= 0) return bar
  const k = mode === 'qfq' ? factor : 1 / factor
  return {
    ...bar,
    open: bar.open * k,
    high: bar.high * k,
    low: bar.low * k,
    close: bar.close * k,
  }
}

export function adjustBars<T extends BarLike>(bars: readonly T[], factor: number, mode: AdjustMode): T[] {
  return bars.map((b) => adjustBar(b, factor, mode))
}

export type ResampleMinutes = 5 | 15 | 30 | 60

/**
 * 1 分钟线聚合到 target 分钟桶。桶起点 = floor(time/桶宽)×桶宽（整点对齐）；
 * OHLC 标准聚合（open=首 open、close=末 close、high=max、low=min、volume=求和）；
 * 输入按 time 升序为前提（乱序结果未定义，由调用方保证）；空输入→[]，无数据段不补零桶。
 */export function resampleBars(bars: readonly BarLike[], target: ResampleMinutes): BarLike[] {
  if (bars.length === 0) return []
  const width = target * 60
  const buckets = new Map<number, BarLike>()
  const order: number[] = []
  for (const b of bars) {
    const start = Math.floor(b.time / width) * width
    const cur = buckets.get(start)
    if (!cur) {
      buckets.set(start, { ...b, time: start })
      order.push(start)
    } else {
      if (b.high > cur.high) cur.high = b.high
      if (b.low < cur.low) cur.low = b.low
      cur.close = b.close
      cur.volume += b.volume
    }
  }
  // Map 保插入序 = 输入升序的桶序；仍按 start 排序兜底，防乱序输入漂桶。
  return order.sort((a, b) => a - b).map((s) => buckets.get(s)!)
}

/* ---------- W6a：持仓成本线并入参考线集合 ---------- */

export interface LevelLike {
  price: number
  color?: string
  title?: string
  lineStyle?: 'solid' | 'dashed' | 'dotted'
}

export interface CostLine {
  price: number
  title?: string
  color?: string
}

/**
 * 成本线并入参考线：未持仓（cost 为 null/价非正）原样返回 levels 不产出成本线（缺省不显线）；
 * 与既有 levels 同价时成本线替换占位且居首（成本语义压过止损止盈等关键价）；
 * 成本线默认实线，color 缺省由调用方传主题色。纯函数不改入参。
 */
export function withCostLine(
  levels: readonly LevelLike[],
  cost: CostLine | null | undefined,
  color?: string,
): LevelLike[] {
  if (!cost || !Number.isFinite(cost.price) || cost.price <= 0) return [...levels]
  const rest = levels.filter((l) => l.price !== cost.price)
  return [{ price: cost.price, title: cost.title ?? '成本', lineStyle: 'solid', color: cost.color ?? color }, ...rest]
}

/* ---------- W6b：逐笔 marker 采样聚合（防密集成交糊屏） ---------- */

export interface MarkerLike {
  time: number
  /** 方向语义（如 buy/sell）；缺省视为同组，不做方向分层。 */
  side?: string
}

/**
 * 密集成交采样：marks 数 ≤ maxCount 原样拷贝；超出则按时间等宽分桶（桶数≤maxCount），
 * 每桶每 side 保留时间最早一条（一进一出不互吞）；首末两条恒保留。
 * 输入按 time 升序为前提；纯函数不改入参、不读时钟。maxCount<=0 视为 1。
 */
export function aggregateMarkers<T extends MarkerLike>(marks: readonly T[], maxCount: number): T[] {
  if (marks.length === 0) return []
  const cap = Math.max(1, maxCount)
  if (marks.length <= cap) return [...marks]
  const first = marks[0]
  const last = marks[marks.length - 1]
  const span = last.time - first.time
  const bucketCount = Math.min(cap, marks.length)
  const width = span > 0 ? span / bucketCount : 1
  /** `${桶序}|${side}` → 该桶该方向的最早一条。 */
  const bucketPick = new Map<string, T>()
  for (const m of marks) {
    if (m === first || m === last) continue
    const idx = Math.min(bucketCount - 1, Math.floor((m.time - first.time) / width))
    const key = `${idx}|${m.side ?? ''}`
    const cur = bucketPick.get(key)
    if (!cur || m.time < cur.time) bucketPick.set(key, m)
  }
  const inner = [...bucketPick.values()].sort((a, b) => a.time - b.time)
  return [first, ...inner, last]
}
