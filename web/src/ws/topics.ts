/**
 * WebSocket 主题负载类型（admin 2003 WS 网关）。
 * Cursor = server_epoch + topic_seq；seq为兼容别名，metadata保留事件外壳。
 */

export type WsTopic = 'signal' | 'position' | 'alert' | 'market' | 'brain'

/** 信令/位置/预警为常驻基础主题；行情与智脑为按需订阅主题。 */
export const BASE_TOPICS: readonly WsTopic[] = ['signal', 'position', 'alert']

export const ALL_TOPICS: readonly WsTopic[] = ['signal', 'position', 'alert', 'market', 'brain']

export interface WsEnvelope {
  topic?: string
  event?: string
  seq?: number | null
  data?: unknown
  payload?: unknown
  server_epoch?: string
  topic_seq?: number
  request_id?: number
  metadata?: WsEventMetadata
  replayed?: boolean
  [key: string]: unknown
}

export interface WsEventMetadata extends Record<string, unknown> {
  schema_version?: number
  event_id?: string
  trace_id?: string
  causation_id?: string
  correlation_id?: string
  entity_id?: string
  entity_version?: number
  source?: string
  subject?: string
  time?: string
  source_time?: string
  received_at?: string
  published_at?: string
  gateway_received_at?: string
}

export interface WsResyncContext {
  serverEpoch: string
  topics: WsTopic[]
  watermarks: Record<string, number>
  reason: string
  signal: AbortSignal
}

/** 派发给 onPush 订阅者的消息。topic='resume' 为重连提示帧。 */
export interface WsPushMessage {
  topic: string
  data: unknown
  seq?: number | null
  serverEpoch?: string
  metadata?: WsEventMetadata
  /** Historical replay is a read-model invalidation, never a new execution authorization. */
  replayed?: boolean
}

export interface SignalPushPayload extends Record<string, unknown> {
  id?: string
  symbol?: string
  side?: string
  status?: string
  signalTime?: string
  signal_time?: string
}

export interface PositionPushPayload extends Record<string, unknown> {
  account?: string
  accountType?: string
  account_type?: string
  symbol?: string
  cash?: number
  equity?: number
  dayPnl?: number
  day_pnl?: number
  positions?: unknown[]
}

export interface AlertPayload extends Record<string, unknown> {
  kind?: string
  type?: string
  account?: string
  accountType?: string
  account_type?: string
  needConfirm?: boolean
  need_confirm?: boolean
  title?: string
  subject?: string
  body?: string
  message?: string
  reason?: string
}

export interface MarketPushPayload extends Record<string, unknown> {
  symbol?: string
  freq?: string
  bar?: Record<string, unknown>
  bars?: Record<string, unknown>[]
  tick?: Record<string, unknown>
  asOf?: string
  as_of?: string
}

/** 行情快照批次信息（datahub 每轮快照写完后发，即后端 SnapshotMeta）。 */
export interface MarketSnapshotMeta extends Record<string, unknown> {
  asOf?: string
  as_of?: string
  source?: string
  count?: number
  latencyMs?: number
  latency_ms?: number
  seq?: number
  stale?: boolean
}

export interface BrainPushPayload extends Record<string, unknown> {
  kind?: string
  traceId?: string
  trace_id?: string
  symbol?: string
  stage?: string
  message?: string
  progress?: number
}

export interface WsTopicMap {
  signal: SignalPushPayload
  position: PositionPushPayload
  alert: AlertPayload
  market: MarketPushPayload
  brain: BrainPushPayload
}

export type PushHandler<T extends WsTopic> = (data: WsTopicMap[T]) => void
