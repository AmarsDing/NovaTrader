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
  /** 成本/口径假设清单（pkg/backtest.Report.Assumptions；空=未声明，前端灰显不补 0）。 */
  assumptions: string[]
}

/**
 * W8a/W8c 信任条 12 判据位（契约锚 backend api/backtest/v1 TrustBar；R-UI-20/23）。
 * 线上零值语义在适配层折成缺失：barsTotal/tradingDays 0=未统计 → null（没取到）；
 * fee/slippage 0=未声明 → null；tradeCount 保留 0（0 笔=「样本不足」结论位，是有信息的事实）；
 * verified false=未验证（绝不渲染已验证绿）；字符串空=未声明。
 */
export interface TrustBar {
  barsTotal: number | null
  tradingDays: number | null
  tradeCount: number | null
  windowStart: string
  windowEnd: string
  costModel: string
  feeBps: number | null
  slippageBps: number | null
  blindVerified: boolean
  pboVerified: boolean
  datasetManifestId: string
  datasetHash: string
}

/** 任务详情读口（GetTask）：列表不付判据，信任条与原始参数随详情下发（缺失照缺位显）。 */
export interface BacktestTaskDetail extends BacktestTask {
  trustBar: TrustBar
  /** task.config 原始提交参数（Struct→对象）；无参数体为 null。 */
  config: Record<string, unknown> | null
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

/* 影子统计/自省/进化提案/模拟盘准入的 DTO 随适配器定义在 `../backtest.ts`
   （ShadowStats / Introspection / Proposal / Admission），这里不再重复，避免两份口径漂移。 */
