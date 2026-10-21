/** M05 智脑：晨报 / 分析 / 统计（camelCase 归一化 DTO）。 */

/** 四维评分。 */
export interface DimensionScore {
  name: string
  score: number | null
  comment: string
}

/** POST /api/brain/analyze 结果。 */
export interface Analysis {
  symbol: string
  name: string
  score: number | null
  verdict: string
  dimensions: DimensionScore[]
  reasons: string[]
  risks: string[]
  generatedAt: string
  model: string
}

/** POST /api/brain/explain 结果。 */
export interface Explanation {
  symbol: string
  text: string
  reasons: string[]
  generatedAt: string
  model: string
}

/** POST /api/brain/ask 结果。 */
export interface AskReply {
  answer: string
  citations: string[]
  generatedAt: string
  model: string
}

/** 晨报条目（兼容展示模型：由真实字段派生，监控/overview 用）。 */
export interface BriefingItem {
  kind: string
  title: string
  body: string
  symbol: string
}

/** P1-5 数字三档核对留痕。sourced 事实包有出处 / recomputed 算式重算通过 / omitted 无出处已略。 */
export interface EvidenceItem {
  field: string
  span: string
  verdict: string
  match: string
}

/** 晨报关注池条目。 */
export interface WatchItemRow {
  symbol: string
  reason: string
  evidence: string[]
}

/** 晨报持仓看点。 */
export interface PositionViewRow {
  symbol: string
  view: string
  evidence: string[]
}

/** 晨报引用的原始情报。 */
export interface NewsLineRow {
  id: string
  title: string
  importance: number | null
  symbol: string
}

/** GET /api/brain/briefing/{date} 结果。 */
export interface Briefing {
  date: string
  degraded: boolean
  headline: string
  marketView: string
  risks: string[]
  watchlist: WatchItemRow[]
  positions: PositionViewRow[]
  news: NewsLineRow[]
  reason: string
  evidence: EvidenceItem[]
  /** 被替换为「（略※）」的数字个数；旧晨报/降级晨报为 null（未核对≠0）。 */
  omittedCount: number | null
  createdAt: string
  /* ---- 兼容派生（monitor/overview 沿用）：由上面真实字段折算，不另走后端字段。 ---- */
  summary: string
  items: BriefingItem[]
  generatedAt: string
  stale: boolean
}

/** 推理进度（brain-monitor 面板展示）。 */
export interface AnalyzeProgress {
  symbol: string
  stage: string
  done: number | null
  total: number | null
  message: string
  at: string
}

/** GET /api/brain/stats 每层级（tier）运行统计。 */
export interface TierStats {
  tier: string
  model: string
  configured: boolean
  calls: number | null
  failures: number | null
  p50Ms: number | null
  p95Ms: number | null
  inFlight: number | null
  queued: number | null
  circuitOpen: boolean
  kvCacheUsage: number | null
  running: number | null
  waiting: number | null
}
