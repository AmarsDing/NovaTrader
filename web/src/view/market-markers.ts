/**
 * K 线买卖标记调色的唯一来源（双色制定案）：买=up 红、卖=down 绿、方向未知=中性 text3。
 * 与全站红涨绿跌同一 token 对（--nt-up/--nt-down 四主题块钉档，color-vision 硬门禁管辖），
 * 不另起 buy/sell 别名防第二来源；方向未知不默认红（对齐 GalleryView:1127「认不出来用中性色」口径）。
 * 管辖范围=markers 调色板键（PALETTE key）；确认单/总览/信号页的方向角标走 CSS class（up/down，
 * 未知='' 继承正文色）的类名约定，与本映射并行不悖，双方均遵守「未知方向不默认红」。
 */
import type { KlineMarker } from '@/viz/NtKline.vue'
import { aggregateMarkers, type CostLine, type LevelLike, withCostLine } from '@/utils/kline-transform'

export type FillSideTone = 'up' | 'down' | 'text3'

export function fillSideTone(side: string | null | undefined): FillSideTone {
  if (side === 'sell') return 'down'
  if (side === 'buy') return 'up'
  return 'text3'
}

/* ---------- W6b 接线：密集成交 marker 采样 + 摊薄成本线入 cost 槽 ---------- */

/**
 * K 线上 marker 分桶采样上限=80 桶（见 kline-transform.aggregateMarkers）；
 * 实际产出上限不超「2*CAP + 2 首末 + 消息数」（每桶每 side 最早一条 + 首末恒留 + 消息不采样）。
 */
export const KLINE_MARKER_CAP = 80

/** marker 时间秒数归一：epoch 数/RFC3339 串/Date 串均取毫秒转秒；NaN/缺省返回 null（该 marker 不参与采样）。 */
function markerEpochSec(m: KlineMarker): number | null {
  const t = m.time
  if (typeof t === 'number' && Number.isFinite(t)) return t > 1e12 ? t / 1000 : t
  if (typeof t === 'string' && t) {
    const p = Date.parse(t)
    if (Number.isFinite(p)) return p / 1000
    const n = Number(t)
    if (Number.isFinite(n)) return n > 1e12 ? n / 1000 : n
  }
  return null
}

/** 买卖标记采样聚合：条数 ≤时间桶上限 原序返回不采样。
 *  超出后：成交（arrowUp/arrowDown）按 epoch 秒升序+分桶每 side 取最早一条+真实首末恒留（不依赖入参顺序）；
 *  消息/未知型一律不采样原样接过（消息无方向、shape 不属于双 buy/sell 词，抽稀会造成语义错位）；
 *  时间缺（Date.parse 不合法）的 marker 走「不参与采样原样接过」族——不丢记录不造伪桶；
 *  出口保留入参原 time 形态（不覆写为 epoch 数值），既保留 RFC3339 原串语义，也让未归一字段不被伪造。 */
export function aggregateKlineMarkers(marks: readonly KlineMarker[]): KlineMarker[] {
  if (marks.length <= KLINE_MARKER_CAP) return [...marks]
  type FillRec = { m: KlineMarker; epoch: number; side: 'buy' | 'sell' }
  const fills: FillRec[] = []
  const other: KlineMarker[] = []
  for (const m of marks) {
    const isFill = m.shape === 'arrowUp' || m.shape === 'arrowDown'
    const epoch = markerEpochSec(m)
    if (isFill && epoch != null) {
      fills.push({ m, epoch, side: m.shape === 'arrowDown' ? 'sell' : 'buy' })
    } else other.push(m)
  }
  /* aggregateMarkers 对入参要求升序且首末锚取入参首末（kline-transform.ts 108-115）——
     /fills 是 Desc created_at 逆序，故先把 fills 按 epoch 升序排序，再在开头/结尾锚定真实最早/最新，
     避免逆序时被首末错位。 */
  const sorted = [...fills].sort((a, b) => a.epoch - b.epoch)
  if (sorted.length === 0) return [...other]
  const first = sorted[0]!
  const last = sorted[sorted.length - 1]!
  const inner = sorted.slice(1, -1)
  /* 入参带 side 键防方向未知同桶互吞，time 用 epoch 满足 aggregateMarkers 升序分桶判据。 */
  const sampledInner = aggregateMarkers(
    inner.map(({ epoch, side }) => ({ time: epoch, side })),
    Math.max(1, KLINE_MARKER_CAP - 2),
  )
  /* 按 epoch+side 反查回原 marker——保留原 time 形态（RFC3339 字符串/number 原貌），不做覆写伪造。 */
  const innerMap = new Map<string, KlineMarker>()
  for (const f of inner) innerMap.set(`${f.epoch}|${f.side}`, f.m)
  const restored = sampledInner.map((s) => innerMap.get(`${s.time}|${s.side}`)!)
  return [first.m, ...restored, last.m, ...other].sort((a, b) => (markerEpochSec(a) ?? 0) - (markerEpochSec(b) ?? 0))
}

/** 摊薄成本线入 cost 槽：净持仓>0 且净额>0 才给 cost，未持仓/净额亏光/数据残缺一律不显线（不伪造 0）。
 *  未知方向（side 非 'buy'/'sell'）跳过不计——不允许伪造空方压持仓对面（对齐「方向未知不默认」纪律）。
 *  levels 内同价条目已由 withCostLine 去重（成本语义压过关键价）。 */
export function withFillsCostLine(levels: readonly LevelLike[], fills: readonly { symbol: string; side: string; qty: number | null; price: number | null; amount: number | null }[], code: string): { levels: LevelLike[]; cost: CostLine | null } {
  if (!code) return { levels: [...levels], cost: null }
  let qty = 0
  let amt = 0
  for (const fill of fills) {
    if (fill.symbol !== code || fill.qty == null || fill.qty <= 0) continue
    const a = fill.amount ?? (fill.price != null ? fill.price * fill.qty : null)
    if (a == null) continue
    if (fill.side === 'buy') { qty += fill.qty; amt += a } else if (fill.side === 'sell') { qty -= fill.qty; amt -= a }
    /* side 未知跳过不计——不伪造空方（对齐未知不默认 red/text3 的纪律）。 */
  }
  if (qty <= 0 || amt <= 0) return { levels: [...levels], cost: null }
  return { levels: withCostLine(levels, { price: amt / qty, title: '摊薄成本' }), cost: { price: amt / qty, title: '摊薄成本' } }
}
