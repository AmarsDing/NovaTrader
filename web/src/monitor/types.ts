import type { BreadthSummary, KlineGapsSummary, PremiumSummary } from '@/api/market'
import type { OpsDb, OpsNats } from '@/api/ops'
import type { ObservabilitySnapshot } from '@/api/observability'

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
  /** 来自真实事件外壳；缺失保持未知，页面接收时间 at 与源时间分别解释。 */
  eventId?: string
  traceId?: string
  subject?: string
  sourceTime?: string
  receivedAt?: string
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
  /** 实测网关收入口径，不能冒充所有服务的业务消费确认。 */
  observability?: ObservabilitySnapshot | null
  /** 最近一次 REST 聚合完成时间；兼容旧调用方，不能由 WS 推送刷新。 */
  now: number
  /** REST 采样时钟。0 表示尚未完成首次采样，与数据自身时间分别展示。 */
  sampledAt?: number
  /** 最近收到的监控 WS 脉冲时间，仅描述推送活动。 */
  lastPush?: number
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
  /** S7：风控限额上限 × 当前用量（GET /api/risk/limits，引擎内存口径）。拉不到为 null，界面仍走前端估算。 */
  riskLimits?: {
    ok: boolean
    items: { key: string; name: string; unit: string; limit: number; used: number; ratio: number; note: string }[]
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
      /** 「没取到≠0」（P3-10c）：total=0 空窗时分位为 null，界面显「样本不足」。 */
      p50Ms: number | null
      p95Ms: number | null
      buckets: { leMs: number; count: number }[]
    } | null
  } | null
  /** S7：策略绩效与评分-收益（GET /api/strategy/performance + score-vs-pnl）。sample=0 由界面写「样本不足」。 */
  strategyP1?: {
    ok: boolean
    performance?: {
      sample: number
      winRate: number
      plRatio: number
      maxDrawdown: number
      avgReturn: number
      attributions: { attribution: string; count: number }[]
    } | null
    scatter?: {
      points: { signalId: string; symbol: string; score: number; actualReturn: number; tradeDate: string; strategy: string }[]
      pearson: number
      sample: number
    } | null
  } | null
  /** S7：竞价截面与连板梯队（GET /api/market/auction + ladder）。auction.found=false 表示当日无竞价因子。 */
  marketP1?: {
    ok: boolean
    auction?: {
      tradeDate: string
      found: boolean
      totalAmount: number
      limitUpCount: number
      // 「没取到≠0」（P3-10c）：auctionPct/VolRatio/Turnover/Unmatched 源缺键为 null（0=平开等真值）；auctionAmount 键恒在。
      limitUps: { symbol: string; auctionPct: number | null; auctionAmount: number; auctionVolRatio: number | null; auctionTurnover: number | null; auctionLimitUp: boolean; auctionUnmatched: number | null }[]
      topRush: { symbol: string; auctionPct: number | null; auctionAmount: number; auctionVolRatio: number | null; auctionTurnover: number | null; auctionLimitUp: boolean; auctionUnmatched: number | null }[]
      asOf: string
    } | null
    ladder?: {
      days: { tradeDate: string; steps: { fromHeight: number; sample: number; succeeded: number; rate: number }[] }[]
      total: { fromHeight: number; sample: number; succeeded: number; rate: number }[]
    } | null
  } | null
  /** S7：GPU 与文件单目录（GET /api/ops/gpu + /api/ops/broker-files）。gpu_available=false 表示没有 nvidia-smi。 */
  opsP1?: {
    ok: boolean
    gpu?: {
      gpus: { name: string; memUsedMiB: number; memTotalMiB: number; utilPct: number; tempC: number; powerW: number }[]
      gpuAvailable: boolean
      gpuNote: string
      brainInFlight: number | null
      brainQueued: number | null
      brainTokensPerHour: number | null
      brainCallsToday: number | null
    } | null
    brokerFiles?: {
      available: boolean
      note: string
      pending: number | null
      written: number | null
      consumed: number | null
      leftover: number | null
      lastOk: string
    } | null
  } | null
  /**
   * P2：昨涨停今溢价 / 20 日均线上占比 / K 线连续性
   * （GET /api/market/limit-premium + breadth + kline-gaps，M02 只读口 + limit_premium/market_breadth 两张快照表）。
   * premium.found=false 表示基准日没有涨停名单（不是接口坏了）；live=true 表示快照表还没落、按 market_data 实时兜底。
   */
  marketP2?: {
    ok: boolean
    premium?: PremiumSummary | null
    breadth?: BreadthSummary | null
    klineGaps?: KlineGapsSummary | null
  } | null
  /**
   * P2：PostgreSQL 连接池/库体积/慢查询与 NATS 积压（GET /api/ops/db + /api/ops/nats）。
   * db.slowAvailable=false 表示 pg_stat_statements 没装（照实写缺口）；nats.connected=false 表示总线断连。
   */
  opsP2?: {
    ok: boolean
    db?: OpsDb | null
    nats?: OpsNats | null
  } | null
  pulses: Pulse[]
}
