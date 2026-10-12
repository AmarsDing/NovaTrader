/**
 * S3 漏斗光带：六层数字与各层侧漏的纯函数推导（监控驾驶舱业务监控方案 §4.4.2 / §5.6）。
 *
 * 口径（全部来自只读接口，绝不触发 POST /api/strategy/scan）：
 * - 全市场：只出现在扫描执行完的 ScanReply 里，监控页只读拿不到 → 恒为「未返回」；
 * - 初筛：候选行数。listCandidates 默认当日、上限 200 条，数字是下限，命中上限时 note 提醒；
 * - AI≥70：候选里 aiScore ≥ 70 的行（模型给过分的。不过线的原因也落在本层与下层的侧漏里）；
 * - 风控通过：stage === 'selected' 的候选行（后端建信号即通过信号层规则）；
 * - 已下单 / 成交：买单委托 / 买单成交笔数（listOrders / listFills，卖单不属于这条漏斗）。
 *
 * 侧漏：各层过滤原因按 missReason 分组计数，标签取最大项（missLabel 中文口径）；
 * 下单/成交两层候选表里没有原因行，侧漏用相邻两层数字之差。
 * 缺失层 count=null 画虚线「未返回」，不用 0 填充、不跳过。
 */

export interface FunnelLayer {
  key: string
  name: string
  count: number | null
}

export interface FunnelLeak {
  /** 从哪一段交界处分流：layer[at-1] → layer[at]（at ∈ 1..5） */
  at: number
  count: number | null
  label: string
  /** 该交界处的过滤原因分布（按只数降序，详情窗横条榜用） */
  reasons: { label: string; count: number }[]
}

export interface FunnelModel {
  layers: FunnelLayer[]
  leaks: FunnelLeak[]
  /** 候选接口默认上限命中时的提醒（各层数字是下限，不是全量） */
  note: string
}

/** 主干厚度：sqrt(数量) 映射到 12..96px（用 sqrt 不用线性，否则第一层把后面压成一根线） */
export const FUNNEL_MAX_W = 96
export const FUNNEL_MIN_W = 12

export interface FunnelCandidateRow {
  stage: string
  missReason: string
  aiScore: number | null
}

/** 初筛→AI 之间的过滤原因（还没轮到模型或模型结果作废） */
const PRE_AI_MISS = new Set(['no_quote', 'held', 'cooldown', 'not_in_ai_top', 'ai_invalid', 'killswitch', 'after_cutoff'])
/** AI→风控通过之间的过滤原因（过了模型但被信号层规则拦下） */
const RULE_MISS = new Set(['below_threshold', 'daily_cap', 'price_invalid'])

/** 过滤原因的中文口径走 format.missLabel；这里不 import，避免监控纯函数被 format 牵连，调用方注入 */
export type MissLabelFn = (code: string) => string

export function buildFunnel(input: {
  candidates?: { ok: boolean; items: FunnelCandidateRow[] } | null
  orders?: { ok: boolean; items: { side: string }[] } | null
  fills?: { ok: boolean; items: { side: string }[] } | null
}, labelOf: MissLabelFn): FunnelModel {
  const cands = input.candidates
  const rows = cands?.ok ? cands.items : []
  const screened = cands?.ok ? rows.length : null
  const ai = cands?.ok ? rows.filter((row) => row.aiScore != null && row.aiScore >= 70).length : null
  const risk = cands?.ok ? rows.filter((row) => row.stage === 'selected').length : null
  const ordered = input.orders?.ok ? input.orders.items.filter((item) => isBuy(item.side)).length : null
  const filled = input.fills?.ok ? input.fills.items.filter((item) => isBuy(item.side)).length : null

  const layers: FunnelLayer[] = [
    { key: 'universe', name: '全市场', count: null },
    { key: 'screened', name: '初筛', count: screened },
    { key: 'ai', name: 'AI≥70', count: ai },
    { key: 'risk', name: '风控通过', count: risk },
    { key: 'ordered', name: '已下单', count: ordered },
    { key: 'filled', name: '成交', count: filled },
  ]

  const leaks: FunnelLeak[] = []
  /* 全市场→初筛：全市场数字没有只读出口，这层侧漏不画，不编 */
  const preAI = leakFromRows(rows, PRE_AI_MISS, labelOf)
  if (preAI) leaks.push({ ...preAI, at: 2 })
  const rule = leakFromRows(rows, RULE_MISS, labelOf)
  if (rule) leaks.push({ ...rule, at: 3 })
  if (risk != null && ordered != null && risk - ordered > 0) {
    leaks.push({ at: 4, count: risk - ordered, label: '尚未下单', reasons: [] })
  }
  if (ordered != null && filled != null && ordered - filled > 0) {
    leaks.push({ at: 5, count: ordered - filled, label: '未成交', reasons: [] })
  }

  const note = cands?.ok && rows.length >= 200 ? '候选列表命中接口默认上限 200 条，各层数字是下限' : ''
  return { layers, leaks, note }
}

function leakFromRows(rows: FunnelCandidateRow[], group: Set<string>, labelOf: MissLabelFn): { count: number; label: string; reasons: { label: string; count: number }[] } | null {
  const tally = new Map<string, number>()
  for (const row of rows) {
    if (!row.missReason || !group.has(row.missReason)) continue
    tally.set(row.missReason, (tally.get(row.missReason) ?? 0) + 1)
  }
  if (tally.size === 0) return null
  const reasons = [...tally.entries()]
    .map(([code, count]) => ({ label: labelOf(code), count }))
    .sort((a, b) => b.count - a.count)
  const count = reasons.reduce((sum, item) => sum + item.count, 0)
  return { count, label: reasons[0].label, reasons }
}

/** 每层厚度：sqrt 映射，最大层 96px、下限 12px；缺失层返回 null（几何层画固定虚线段） */
export function funnelWidths(counts: (number | null)[]): (number | null)[] {
  const known = counts.filter((count): count is number => count != null)
  const cMax = known.length > 0 ? Math.max(...known) : 0
  return counts.map((count) => {
    if (count == null) return null
    if (cMax <= 0) return FUNNEL_MIN_W
    return Math.min(FUNNEL_MAX_W, Math.max(FUNNEL_MIN_W, Math.round(FUNNEL_MAX_W * Math.sqrt(count / cMax))))
  })
}

/** 交界处的转化率文本：后层 / 前层，一位小数；前层缺失时不编率 */
export function funnelRate(count: number | null, prev: number | null): string | null {
  if (count == null || prev == null || prev <= 0) return null
  return `${((count / prev) * 100).toFixed(1)}%`
}

/** 计数显示：千分位（方案示例「5,120」），未返回交给调用方写「未返回」 */
export function funnelCountText(count: number | null): string {
  if (count == null) return '未返回'
  return String(Math.round(count)).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

function isBuy(side: string): boolean {
  const key = side.trim().toLowerCase()
  return key === 'buy' || key === '买' || key === '买入'
}
