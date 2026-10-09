/**
 * WebSocket 主题负载类型（admin 2003 WS 网关）。
 * 信封统一为 { topic, event?, seq, data | payload }。
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
  [key: string]: unknown
}

/** 派发给 onPush 订阅者的消息。topic='resume' 为重连提示帧。 */
export interface WsPushMessage {
  topic: string
  data: unknown
  seq?: number | null
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
