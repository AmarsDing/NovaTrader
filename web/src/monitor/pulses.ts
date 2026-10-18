import { onPush, subscribe, type WsPushMessage, type WsTopic } from '@/ws/hub'
import type { Identifier, Pulse } from './types'

/** 把一帧推送收成脉冲。没有标识符的帧返回 null，不进场景。 */
export function pulseFromPush(msg: { topic: string; data: unknown; metadata?: unknown }, at: number): Pulse | null {
  if (msg.topic === 'resume' || msg.topic === 'position') return null
  const data = msg.data && typeof msg.data === 'object' ? (msg.data as Record<string, unknown>) : {}
  const metadata = msg.metadata && typeof msg.metadata === 'object' ? (msg.metadata as Record<string, unknown>) : {}
  const event = {
    eventId: text(metadata, 'event_id'), traceId: text(metadata, 'trace_id'),
    subject: text(metadata, 'subject'), sourceTime: text(metadata, 'source_time'),
    receivedAt: text(metadata, 'received_at'),
  }
  const pulse = (from: string, to: string, identifier: Identifier) => ({ ...makePulse(from, to, identifier, at, event.eventId), ...event })
  if (msg.topic === 'brain') {
    const trace = event.traceId || text(data, 'traceId', 'trace_id')
    if (trace) return pulse('service:brain', 'post:model', { kind: 'trace', value: trace })
    const symbol = text(data, 'symbol')
    if (symbol) return pulse('service:brain', 'post:model', { kind: 'symbol', value: symbol })
    return null
  }
  if (msg.topic === 'signal') {
    const id = text(data, 'signal_id', 'signalId', 'id') || text(data, 'stock_code', 'stockCode', 'symbol')
    if (!id) return null
    return pulse('post:model', 'post:desk', { kind: 'signal', value: id })
  }
  if (msg.topic === 'market') {
    const symbol = text(data, 'symbol')
    if (symbol) return pulse('service:market', 'post:scan', { kind: 'symbol', value: symbol })
    // SnapshotMeta 是数据批次；只在真实批次身份存在时呈现，不能造出证券代码。
    const source = text(data, 'source')
    const seq = text(data, 'seq')
    const asOf = text(data, 'as_of', 'asOf')
    if (source && seq && asOf) return pulse('service:market', 'post:scan', { kind: 'source', value: `${source} · 批次 ${seq}` })
    return null
  }
  if (msg.topic === 'alert') {
    const kind = text(data, 'kind', 'type')
    if (!kind) return null
    return pulse('post:risk', 'book:center', { kind: 'rule', value: kind })
  }
  return null
}

/** 订阅行情与智脑，并把推送交给页面。返回退订函数。 */
export function bindPulses(onPulse: (pulse: Pulse) => void, onDomain: (topic: WsTopic) => void | Promise<void>): () => void {
  const offMarket = subscribe('market')
  const offBrain = subscribe('brain')
  const offPush = onPush((msg: WsPushMessage) => {
    if (!['position', 'signal', 'alert', 'market', 'brain'].includes(msg.topic)) return
    const next = pulseFromPush(msg, Date.now())
    if (next) onPulse(next)
    return onDomain(msg.topic as WsTopic)
  })
  return () => {
    offMarket()
    offBrain()
    offPush()
  }
}

function makePulse(from: string, to: string, identifier: Identifier, at: number, eventId = ''): Pulse {
  return { id: eventId ? `event:${eventId}` : `${from}>${to}:${identifier.kind}:${identifier.value}:${at}`, from, to, identifier, at }
}

function text(data: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = data[key]
    if (typeof value === 'string' && value.trim()) return value.trim()
    if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  }
  return ''
}
