import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'
import type { Identifier, Pulse } from './types'

/** 把一帧推送收成脉冲。没有标识符的帧返回 null，不进场景。 */
export function pulseFromPush(msg: { topic: string; data: unknown }, at: number): Pulse | null {
  if (msg.topic === 'resume' || msg.topic === 'position') return null
  const data = msg.data && typeof msg.data === 'object' ? (msg.data as Record<string, unknown>) : {}
  if (msg.topic === 'brain') {
    const trace = text(data, 'traceId', 'trace_id')
    if (trace) return makePulse('service:brain', 'post:model', { kind: 'trace', value: trace }, at)
    const symbol = text(data, 'symbol')
    if (symbol) return makePulse('service:brain', 'post:model', { kind: 'symbol', value: symbol }, at)
    return null
  }
  if (msg.topic === 'signal') {
    const id = text(data, 'id') || text(data, 'symbol')
    if (!id) return null
    return makePulse('post:model', 'post:desk', { kind: 'signal', value: id }, at)
  }
  if (msg.topic === 'market') {
    const symbol = text(data, 'symbol')
    if (!symbol) return null
    return makePulse('service:market', 'post:scan', { kind: 'symbol', value: symbol }, at)
  }
  if (msg.topic === 'alert') {
    const kind = text(data, 'kind', 'type')
    if (!kind) return null
    return makePulse('post:risk', 'book:center', { kind: 'rule', value: kind }, at)
  }
  return null
}

/** 订阅行情与智脑，并把推送交给页面。返回退订函数。 */
export function bindPulses(onPulse: (pulse: Pulse) => void, onPosition: () => void): () => void {
  const offMarket = subscribe('market')
  const offBrain = subscribe('brain')
  const offPush = onPush((msg: WsPushMessage) => {
    if (msg.topic === 'position') {
      onPosition()
      return
    }
    const next = pulseFromPush(msg, Date.now())
    if (next) onPulse(next)
  })
  return () => {
    offMarket()
    offBrain()
    offPush()
  }
}

function makePulse(from: string, to: string, identifier: Identifier, at: number): Pulse {
  return { id: `${from}>${to}:${identifier.kind}:${identifier.value}:${at}`, from, to, identifier, at }
}

function text(data: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = data[key]
    if (typeof value === 'string' && value.trim()) return value.trim()
    if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  }
  return ''
}
