/** M06 策略信号与扫描。经 admin 代理 /api/strategy/*。 */

export type SignalStatus =
  | 'risk_checking' | 'approved' | 'rejected' | 'executing'
  | 'done' | 'closed' | 'expired' | 'cancelled' | string

export interface Signal {
  id: string
  book: string
  signalTime: string
  tradeDate: string
  symbol: string
  name: string
  /** buy / sell */
  side: string
  strategy: string
  strategyVersionId: string
  entry: number | null
  entryLow: number | null
  entryHigh: number | null
  stopLoss: number | null
  takeProfit: number | null
  atr: number | null
  positionPct: number | null
  validUntil: string
  holdDaysMax: number | null
  ruleScore: number | null
  aiScore: number | null
  finalScore: number | null
  dims: Record<string, number | null>
  evidence: string[]
  aiDegraded: boolean
  highValue: boolean
  exitKind: string
  reason: string
  status: SignalStatus
  traceId: string
  createdAt: string
  updatedAt: string
  exitPrice: number | null
  pnlPct: number | null
  hasPnl: boolean
  closedAt: string
  holdingDays: number | null
}

export interface Candidate {
  id: string
  tradeDate: string
  strategy: string
  symbol: string
  name: string
  pool: string
  stage: string
  missReason: string
  ruleScore: number | null
  aiScore: number | null
  finalScore: number | null
  signalId: string
  refPrice: number | null
  retT1: number | null
  retT3: number | null
  retT5: number | null
  hasT1: boolean
  hasT3: boolean
  hasT5: boolean
}

/** listSignals 归一化后的展示态信号。 */
export interface SignalItem {
  id: string
  symbol: string
  name: string
  side: string
  strategy: string
  entry: number | null
  entryLow: number | null
  entryHigh: number | null
  stop: number | null
  take: number | null
  score: number | null
  ruleScore: number | null
  aiScore: number | null
  degraded: boolean
  highValue: boolean
  evidence: string[]
  reason: string
  status: string
  dims: Record<string, unknown>
  time: string
}

export interface ScanReply {
  traceId: string
  universe: number | null
  matched: number | null
  ranked: number | null
  aiCalls: number | null
  degraded: boolean
  signalIds: string[]
  sellSignalIds: string[]
  dropped: Record<string, number>
  misses: Record<string, string>
  killSwitch: boolean
  elapsedMs: number | null
}
