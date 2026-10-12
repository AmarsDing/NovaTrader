export type Ring = 'source' | 'service' | 'post' | 'book'

export type NodeStatus = 'live' | 'idle' | 'gap' | 'down' | 'attention'

export interface Identifier {
  /** F 因子 / N 情报 / trace 链路 / symbol 代码 / signal 信号 / rule 风控号 / source 数据源 */
  kind: 'F' | 'N' | 'trace' | 'symbol' | 'signal' | 'rule' | 'source'
  value: string
}

export type CardRank = 'ambient' | 'brief' | 'focus'

export interface SceneNode {
  id: string
  ring: Ring
  title: string
  status: NodeStatus
  /** 给人读的一行。点开后的子牌用，牌面不直接贴全文 */
  detail: string
  /** 远看的档。视图只认这个字段 */
  rank: CardRank
  /** 牌面上的主词。ambient 且正常时为空串 */
  face: string
  /** 牌面上的第二行。没有则为空串 */
  sub: string
  tone: 'up' | 'down' | 'flat'
  inputs: Identifier[]
  outputs: Identifier[]
  /** S2 右上角新鲜度角标。没拿到可信时间戳就不给，不编 */
  fresh?: { text: string; tone: 'ok' | 'warn' | 'danger' }
  /** S2 底缘活动条：60 格，true 有活动 / false 静默 / null 没观测到 */
  activity?: (boolean | null)[]
}

export interface ChildCard {
  id: string
  title: string
  face: string
  sub: string
  tone: 'up' | 'down' | 'flat'
}

export interface SceneEdge {
  id: string
  from: string
  to: string
}

export interface Pulse {
  id: string
  from: string
  to: string
  identifier: Identifier
  at: number
}

export interface NewsRow {
  id: string
  title: string
  source: string
  time: string
  symbol: string
}

export interface TerminalRow {
  label: string
  value: string
  tone: 'up' | 'down' | 'flat'
}

export interface TerminalModel {
  id: 'term:positions' | 'term:sectors' | 'term:radar' | 'term:heat' | 'term:equity' | 'term:risk'
  title: string
  rows: TerminalRow[]
  empty: string
  caption: string
}

export interface SceneGraph {
  nodes: SceneNode[]
  edges: SceneEdge[]
  pulses: Pulse[]
  news: NewsRow[]
  terminals: TerminalModel[]
  /** 检视栏用。无焦点时为空串 */
  focusId: string
  truncated: boolean
  /** S3 漏斗光带（§4.4.2）：六层数字与侧漏，scene-model 构建时算好 */
  funnel?: import('./funnel').FunnelModel
}

export interface MonitorAccount {
  ok: boolean
  book: string
  equity: number | null
  cash: number | null
  dayPnl: number | null
  /** 开户以来累计已实现（trade 的 RollDay 不清零，所以它不是「当日已实现」） */
  realized?: number | null
  /** 持仓浮动盈亏合计（含往日建仓的部分，不是「当日浮动」） */
  unrealized?: number | null
  paperDays?: number | null
  /** 对账不一致时停止开仓 */
  openHalted?: boolean
  haltReason?: string
  positions: {
    symbol: string
    quantity: number | null
    available?: number | null
    avgCost: number | null
    lastPrice: number | null
  }[]
  /** 缺省视为持仓列表可信。显式 false 表示持仓接口失败，账户数字仍可用。 */
  positionsOk?: boolean
}

/**
 * 信号（只读展示态）。S5 起补齐 SignalItem 的全部字段——详情窗要按 §5.2 出
 * 三价依据、规则分对比、同类历史；都是可选，老夹具与 freshness 测试不受影响。
 */
export interface MonitorSignal {
  id: string
  symbol: string
  name: string
  score: number | null
  highValue: boolean
  degraded: boolean
  status: string
  evidence: string[]
  dims: Record<string, unknown>
  entry: number | null
  stop: number | null
  take: number | null
  time: string
  side?: string
  strategy?: string
  ruleScore?: number | null
  aiScore?: number | null
  reason?: string
  entryLow?: number | null
  entryHigh?: number | null
  atr?: number | null
  positionPct?: number | null
  validUntil?: string
  holdDaysMax?: number | null
  exitKind?: string
  exitPrice?: number | null
  pnlPct?: number | null
  hasPnl?: boolean
  holdingDays?: number | null
  traceId?: string
  tradeDate?: string
  closedAt?: string
}

export interface MonitorInput {
  now: number
  focusSymbol: string
  account: MonitorAccount | null
  risk: {
    ok: boolean
    mode: string
    killActive: boolean
    killReason: string
    buysToday: number | null
    marketBreaker: string
    /** S5：急停来源与触发时刻、风控参数、账户熔断、行情/快照时刻（详情窗 §5.3 用） */
    killSource?: string
    killAt?: string
    params?: Record<string, string>
    breakers?: Record<string, string>
    accountAsOf?: Record<string, string>
    lastQuoteAt?: string
  } | null
  ops: {
    ok: boolean
    services: { name: string; status: string; detail: string }[]
    sources: { name: string; status: string; detail: string }[]
    resources?: { name: string; status: string; detail: string }[]
  } | null
  sources: {
    ok: boolean
    items: {
      id: string
      name: string
      status: string
      detail: string
      /** 最近成功时间（SourceStatus.last_success_at）；失败时刻另给 lastFailureAt，不混用 */
      lastAt: string
      /** S5：§5.4 数据源牌要按字段出，不再只读拼好的 detail 字符串 */
      kind?: string
      enabled?: boolean
      active?: boolean
      successRate?: number | null
      latencyMs?: number | null
      quotaUsed?: number | null
      quotaLimit?: number | null
      priority?: number | null
      official?: boolean
      consecutiveFailures?: number | null
      successCount?: number | null
      failureCount?: number | null
      avgLatencyMs?: number | null
      lastFailureAt?: string
      lastError?: string
    }[]
  } | null
  sentiment: {
    ok: boolean
    phase: string
    up: number | null
    down: number | null
    asOf: string
    stale: boolean
    brokenRate?: number | null
    maxHeight?: number | null
    amount?: number | null
  } | null
  sectors: {
    ok: boolean
    rows: { name: string; avgPct: number | null; overseas: number | null; leader: string }[]
  } | null
  limits: {
    ok: boolean
    rows: { symbol: string; consecutive: number | null; status: string }[]
  } | null
  signals: { ok: boolean; items: MonitorSignal[] } | null
  briefing: {
    ok: boolean
    generatedAt: string
    stale: boolean
    items: { title: string; body: string; symbol: string; kind: string }[]
  } | null
  hotWords: { ok: boolean; items: { type: string; target: string; count: number | null }[] } | null
  timeline: {
    ok: boolean
    symbol: string
    items: { id: string; title: string; time: string; source: string }[]
  } | null
  brainStats: {
    ok: boolean
    tiers: {
      tier: string
      model: string
      configured: boolean
      circuitOpen: boolean
      calls?: number | null
      failures: number | null
      p50Ms?: number | null
      p95Ms?: number | null
      inFlight?: number | null
      queued?: number | null
      kvCacheUsage?: number | null
    }[]
  } | null
  equityCurve: {
    ok: boolean
    points: { day: string; equity: number | null }[]
  } | null
  orders?: {
    ok: boolean
    items: {
      id: string
      symbol: string
      side: string
      status: string
      price: number | null
      volume: number | null
      /** 已成交数量；滑点 bp 要用它与 fills 的成交均价对齐（§5.1） */
      filled?: number | null
      reason: string
    }[]
  } | null
  fills?: { ok: boolean; items: { id: string; symbol: string; side: string; price: number | null; qty: number | null; time: string }[] } | null
  candidates?: {
    ok: boolean
    items: {
      symbol: string
      name: string
      strategy: string
      stage: string
      missReason: string
      ruleScore: number | null
      aiScore: number | null
      finalScore: number | null
      /** S5：§5.6 盘后漏选复盘只做事实陈述——这几个是该股此后的实际涨跌，不是策略收益 */
      pool?: string
      refPrice?: number | null
      retT1?: number | null
      retT3?: number | null
      retT5?: number | null
      hasT1?: boolean
      hasT3?: boolean
      hasT5?: boolean
    }[]
  } | null
  /**
   * 数据质量问题（GET /api/datahub/quality → ListQualityIssuesReply.issues）。
   * 后端给的是**逐条问题**（域/检查项/代码/严重度/来源/条数），不是按域汇总的
   * 缺数/过期计数——S5 起按真实形状出，§5.4「该源覆盖域的缺数条数」按 hits 合计。
   */
  quality?: {
    ok: boolean
    items: {
      id: string
      domain: string
      checkName: string
      symbol: string
      tradeDate: string
      severity: string
      source: string
      otherSource: string
      message: string
      hits: number | null
      lastSeen: string
    }[]
  } | null
  admission?: { ok: boolean; ready: boolean; tradingDays: number | null; requiredDays: number | null; items: { label: string; status: string; detail: string }[] } | null
  shadow?: { ok: boolean; items: { name: string; live: number | null; shadow: number | null }[] } | null
  tasks?: { ok: boolean; items: { id: string; name: string; status: string; message: string }[] } | null
  notices?: { ok: boolean; items: { id: string; title: string; level: string; time: string }[] } | null
  audit?: { ok: boolean; items: { id: string; action: string; target: string; time: string }[] } | null
  factors?: { ok: boolean; symbol: string; items: { name: string; value: number | null }[] } | null
  /** 风控日报（GET /api/risk/report，当日）。S5 补齐计数、规则命中与风控事件（§5.3 规则明细与急停历史） */
  riskReport?: {
    ok: boolean
    date?: string
    summary: string
    checks?: number | null
    approved?: number | null
    rejected?: number | null
    adjusted?: number | null
    p99Ms?: number | null
    rejects?: { rule: string; name: string; count: number | null }[]
    events?: { kind: string; active: boolean; mode: string; source: string; reason: string; operator: string; at: string }[]
  } | null
  /** S7：脑服务只读聚合（GET /api/brain/usage + latency-histogram）。拉不到时 ok=false，界面仍写「未接入」。 */
  brainP1?: {
    ok: boolean
    usage?: {
      window: string
      since: string
      total: { promptTokens: number; completionTokens: number; totalTokens: number; calls: number; failures: number }
      tiers: { tier: string; model: string; promptTokens: number; completionTokens: number; totalTokens: number; calls: number; failures: number }[]
    } | null
    latency?: {
      windowMinutes: number
      total: number
      p50Ms: number
      p95Ms: number
      buckets: { leMs: number; count: number }[]
    } | null
  } | null
  pulses: Pulse[]
}
