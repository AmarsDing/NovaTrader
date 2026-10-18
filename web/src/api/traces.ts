import { request } from './http'
import { asList, pick } from '@/utils/format'
export interface TraceEvent {
  eventId: string
  traceId: string
  causationId: string
  entityId: string
  source: string
  subject: string
  formedAt: string
  sourceTime: string
  publishedAt: string
  state: string
  attempts: number | null
  errorClass: string
}
export interface TraceSnapshot {
  available: boolean
  asOf: string
  events: TraceEvent[]
  pending: number | null
  leased: number | null
  deadLetters: number | null
  oldestPendingAt: string
  truncated: boolean
  missingParents: string[]
  note: string
}
function count(v: unknown) {
  const n = typeof v === 'string' && /^\d+$/.test(v) ? Number(v) : v
  return typeof n === 'number' && Number.isSafeInteger(n) && n >= 0 ? n : null
}
export async function getTrace(traceId = '', signal?: AbortSignal): Promise<TraceSnapshot> {
  const raw = await request('/api/ops/traces', { query: { trace_id: traceId, limit: 200 }, signal })
  if (pick(raw, 'schemaVersion', 'schema_version') !== 1 || pick(raw, 'scope') !== 'durable_outbox_history')
    throw new Error('追踪合同不可用')
  return {
    available: pick(raw, 'available') === true,
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
    pending: count(pick(raw, 'pending')),
    leased: count(pick(raw, 'leased')),
    deadLetters: count(pick(raw, 'deadLetters', 'dead_letters')),
    oldestPendingAt: pick<string>(raw, 'oldestPendingAt', 'oldest_pending_at') || '',
    truncated: pick(raw, 'truncated') === true,
    missingParents: (pick<unknown[]>(raw, 'missingParentIds', 'missing_parent_ids') || []).filter(
      (v): v is string => typeof v === 'string',
    ),
    note: pick<string>(raw, 'note') || '',
    events: asList(raw, 'events').map((v) => ({
      eventId: pick<string>(v, 'eventId', 'event_id') || '',
      traceId: pick<string>(v, 'traceId', 'trace_id') || '',
      causationId: pick<string>(v, 'causationId', 'causation_id') || '',
      entityId: pick<string>(v, 'entityId', 'entity_id') || '',
      source: pick<string>(v, 'source') || '',
      subject: pick<string>(v, 'subject') || '',
      formedAt: pick<string>(v, 'formedAt', 'formed_at') || '',
      sourceTime: pick<string>(v, 'sourceTime', 'source_time') || '',
      publishedAt: pick<string>(v, 'publishedAt', 'published_at') || '',
      state: pick<string>(v, 'state') || '',
      attempts: count(pick(v, 'attempts')),
      errorClass: pick<string>(v, 'errorClass', 'error_class') || '',
    })),
  }
}
