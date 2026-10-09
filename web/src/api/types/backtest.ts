/** M06/M07 回测、影子、自省、进化提案、模拟盘准入。经 admin 代理 /api/backtest/*。 */

export interface StrategyMeta {
  name: string
  title: string
}

export interface BacktestTask {
  id: string
  name: string
  kind: string
  strategy: string
  freq: string
  status: string
  progress: number | null
  message: string
  start: string
  end: string
  totalReturn: number | null
  maxDrawdown: number | null
  winRate: number | null
  hasReport: boolean
}

export interface ReportMetrics {
  totalReturn: number | null
  annualReturn: number | null
  maxDrawdown: number | null
  sharpe: number | null
  winRate: number | null
  profitFactor: number | null
  rounds: number | null
}

export interface EquityPoint {
  day: string
  equity: number | null
  benchmark: number | null
  drawdown: number | null
}

export interface BacktestReport {
  metrics: ReportMetrics
  equity: EquityPoint[]
  warnings: string[]
}

export interface BacktestTrade {
  seq: number | null
  symbol: string
  side: string
  time: string
  price: number | null
  quantity: number | null
  amount: number | null
  pnl: number | null
  reason: string
}

export interface Introspection {
  id: string
  date: string
  strategy: string
  content: string
  createdAt: string
  [key: string]: unknown
}

export interface Proposal {
  id: string
  strategy: string
  title: string
  detail: string
  status: string
  createdAt: string
  [key: string]: unknown
}

export interface ShadowStats {
  strategy: string
  trades: number | null
  winRate: number | null
  pnl: number | null
  [key: string]: unknown
}

export interface AdmissionStatus {
  eligible: boolean
  paperDays: number | null
  required: number | null
  checks: Record<string, unknown>
  [key: string]: unknown
}
