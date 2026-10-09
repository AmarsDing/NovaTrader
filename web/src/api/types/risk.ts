/** M08 风控。经 admin 代理 /api/risk/*。 */

export type RunMode = 'L0' | 'L1' | 'L2' | 'L3' | string

export interface KillSwitchState {
  active: boolean
  source: string
  reason: string
  at: string
}

export interface RiskState {
  mode: RunMode
  kill: KillSwitchState
  params: Record<string, unknown>
  buysToday: number | null
  marketBreaker: string
}

export interface RiskCheckRequest {
  account: string
  symbol: string
  side: string
  price?: number
  volume?: number
  [key: string]: unknown
}

export interface RiskRuleResult {
  code: string
  name?: string
  pass?: boolean
  detail?: string
  [key: string]: unknown
}

export interface RiskCheckReply {
  pass?: boolean
  /** 规则链逐条结果（若后端返回）。 */
  rules?: RiskRuleResult[]
  reason?: string
  [key: string]: unknown
}

export interface RiskReport {
  date?: string
  sections?: Record<string, unknown>
  [key: string]: unknown
}
