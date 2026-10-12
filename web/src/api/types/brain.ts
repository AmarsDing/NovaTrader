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

/** 晨报条目（开盘前必读）。 */
export interface BriefingItem {
  kind: string
  title: string
  body: string
  symbol: string
}

/** GET /api/brain/briefing/{date} 结果。 */
export interface Briefing {
  date: string
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
