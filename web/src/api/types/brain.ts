/** M05 本地大模型智脑。经 admin 代理 /api/brain/*。 */

export interface DimensionScore {
  dim: string
  score: number | null
  reasons: string[]
  risks: string[]
  /** model / rule / failed */
  source: string
  model: string
}

export interface Analysis {
  decisionId: string
  traceId: string
  symbol: string
  dimensions: DimensionScore[]
  compositeScore: number | null
  weightedScore: number | null
  phase: string
  phaseCoef: number | null
  summary: string
  summaryEvidence: string
  aiDegraded: boolean
  discarded: boolean
  reason: string
  latencyMs: number | null
  ruleScore: number | null
}

export interface WatchItem {
  symbol: string
  name: string
  reason: string
  score: number | null
}

export interface PositionView {
  symbol: string
  name: string
  advice: string
  risk: string
}

export interface NewsLine {
  title: string
  source: string
  sentiment: string
  time: string
}

export interface Briefing {
  date: string
  degraded: boolean
  headline: string
  marketView: string
  risks: string[]
  watchlist: WatchItem[]
  positions: PositionView[]
  news: NewsLine[]
  decisionId: string
  createdAt: string
  reason: string
}

export interface Explanation {
  decisionId: string
  summary: string
  impact: string
  risks: string[]
  evidence: string
  aiDegraded: boolean
  discarded: boolean
  reason: string
}

export interface AskReply {
  decisionId: string
  answer: string
  evidence: string
  aiDegraded: boolean
  discarded: boolean
  reason: string
}

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
  /** -1 表示未知 */
  kvCacheUsage: number | null
  running: number | null
  waiting: number | null
}
