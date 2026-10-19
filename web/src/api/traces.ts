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

export interface Disposition {
  id: number
  eventId: string
  traceId: string
  action: string
  note: string
  actor: string
  createdAt: string
}

/** 处置留痕动作封闭集合（后端同款）：只标注，不含回放/重启等后果性动词 */
export const DISPOSITION_ACTIONS: ReadonlyArray<readonly [string, string]> = [
  ['acknowledged', '已知晓'],
  ['investigating', '核查中'],
  ['resolved', '已处置'],
  ['ignored', '忽略（注明原因）'],
  ['escalated', '升级'],
]

const allowedActions = new Set(DISPOSITION_ACTIONS.map(([v]) => v))

export function dispositionLabel(action: string): string {
  return DISPOSITION_ACTIONS.find(([v]) => v === action)?.[1] || `未知动作(${action || '空'})`
}

export async function listDispositions(
  q: { eventId?: string; traceId?: string },
  signal?: AbortSignal,
): Promise<Disposition[]> {
  const raw = await request('/api/ops/dispositions', {
    query: { event_id: q.eventId || undefined, trace_id: q.traceId || undefined, limit: 100 },
    signal,
  })
  return asList(raw, 'items').map((v) => ({
    id: count(pick(v, 'id')) ?? 0,
    eventId: pick<string>(v, 'eventId', 'event_id') || '',
    traceId: pick<string>(v, 'traceId', 'trace_id') || '',
    action: pick<string>(v, 'action') || '',
    note: pick<string>(v, 'note') || '',
    actor: pick<string>(v, 'actor') || '',
    createdAt: pick<string>(v, 'createdAt', 'created_at') || '',
  }))
}

export async function writeDisposition(payload: {
  eventId: string
  traceId?: string
  action: string
  note?: string
}): Promise<void> {
  if (!allowedActions.has(payload.action)) throw new Error('处置动作不在允许集合')
  await request('/api/ops/dispositions', {
    method: 'POST',
    json: { event_id: payload.eventId, trace_id: payload.traceId || '', action: payload.action, note: payload.note || '' },
  })
}

/** 全域因果覆盖口径（N2-02）：窗口内持久域事件的因果形状分布。计数解析不出为 null，绝不补 0。 */
export interface SourceCoverage {
  source: string
  total: number | null
  withTrace: number | null
  withoutTrace: number | null
  children: number | null
  orphanParents: number | null
}
export interface TraceCoverage {
  available: boolean
  scope: string
  asOf: string
  note: string
  windowLimit: number | null
  windowRows: number | null
  oldestInWindow: string
  totals: SourceCoverage
  bySource: SourceCoverage[]
}
export async function getTraceCoverage(signal?: AbortSignal): Promise<TraceCoverage> {
  const raw = await request('/api/ops/trace-coverage', { signal })
  if (pick(raw, 'schemaVersion', 'schema_version') !== 1 || pick(raw, 'scope') !== 'durable_outbox_history')
    throw new Error('因果覆盖合同不可用')
  const row = (v: unknown): SourceCoverage => ({
    source: pick<string>(v, 'source') || '',
    total: count(pick(v, 'total')),
    withTrace: count(pick(v, 'withTrace', 'with_trace')),
    withoutTrace: count(pick(v, 'withoutTrace', 'without_trace')),
    children: count(pick(v, 'children')),
    orphanParents: count(pick(v, 'orphanParents', 'orphan_parents')),
  })
  return {
    available: pick(raw, 'available') === true,
    scope: pick<string>(raw, 'scope') || '',
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
    note: pick<string>(raw, 'note') || '',
    windowLimit: count(pick(raw, 'windowLimit', 'window_limit')),
    windowRows: count(pick(raw, 'windowRows', 'window_rows')),
    oldestInWindow: pick<string>(raw, 'oldestInWindow', 'oldest_in_window') || '',
    totals: row(pick(raw, 'totals')),
    bySource: asList(raw, 'bySource', 'by_source').map(row),
  }
}
