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
}

export interface MonitorAccount {
  ok: boolean
  book: string
  equity: number | null
  cash: number | null
  dayPnl: number | null
  positions: { symbol: string; quantity: number | null; avgCost: number | null; lastPrice: number | null }[]
  /** 缺省视为持仓列表可信。显式 false 表示持仓接口失败，账户数字仍可用。 */
  positionsOk?: boolean
}

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
  } | null
  ops: {
    ok: boolean
    services: { name: string; status: string; detail: string }[]
    sources: { name: string; status: string; detail: string }[]
    resources?: { name: string; status: string; detail: string }[]
  } | null
  sources: {
    ok: boolean
    items: { id: string; name: string; status: string; detail: string; lastAt: string }[]
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
  orders?: { ok: boolean; items: { id: string; symbol: string; side: string; status: string; price: number | null; volume: number | null; reason: string }[] } | null
  fills?: { ok: boolean; items: { id: string; symbol: string; side: string; price: number | null; qty: number | null; time: string }[] } | null
  candidates?: { ok: boolean; items: { symbol: string; name: string; strategy: string; missReason: string; finalScore: number | null }[] } | null
  quality?: { ok: boolean; items: { domain: string; tradeDate: string; missing: number | null; stale: number | null; detail: string }[] } | null
  admission?: { ok: boolean; ready: boolean; tradingDays: number | null; requiredDays: number | null; items: { label: string; status: string; detail: string }[] } | null
  shadow?: { ok: boolean; items: { name: string; live: number | null; shadow: number | null }[] } | null
  tasks?: { ok: boolean; items: { id: string; name: string; status: string; message: string }[] } | null
  notices?: { ok: boolean; items: { id: string; title: string; level: string; time: string }[] } | null
  audit?: { ok: boolean; items: { id: string; action: string; target: string; time: string }[] } | null
  factors?: { ok: boolean; symbol: string; items: { name: string; value: number | null }[] } | null
  riskReport?: { ok: boolean; summary: string } | null
  pulses: Pulse[]
}
