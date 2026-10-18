import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'

export interface ObservationRoute {
  subject: string
  topic: string
  durable: string
  transport: string
  observation: string
  bindingState: string
  bindingUpdatedAt: string | null
}
export interface ObservationEdge {
  subject: string
  source: string
  topic: string
  receivedTotal: number | null
  acceptedTotal: number | null
  bytesTotal: number | null
  errorsTotal: number | null
  windowCount: number | null
  windowBytes: number | null
  windowErrors: number | null
  lastReceivedAt: string | null
  lastEventId: string
  lastTraceId: string
}
export interface ObservationSample {
  receivedAt: string
  subject: string
  source: string
  topic: string
  eventId: string
  traceId: string
  schemaVersion: number | null
  causationId: string
  correlationId: string
  entityId: string
  entityVersion: number | null
  eventTime: string | null
  sourceTime: string | null
  publisherReceivedAt: string | null
  publishedAt: string | null
  payload: Record<string, string>
  payloadStatus: string
}
export interface ObservabilitySnapshot {
  schemaVersion: number | null
  available: boolean
  scope: string
  serverEpoch: string
  asOf: string
  startedAt: string | null
  windowStart: string | null
  windowSeconds: number | null
  windowBasis: string
  windowObservedSeconds: number | null
  windowComplete: boolean
  natsConnected: boolean
  registeredRoutes: ObservationRoute[]
  edges: ObservationEdge[]
  samples: ObservationSample[]
  errorsTotal: number | null
  samplesTruncated: boolean
  sampleCapacity: number | null
  counterCapacity: number | null
  counterOverflowTotal: number | null
  statisticsScope: string
  unsupported: string[]
  note: string
}
export interface ObservabilityQuery {
  subject?: string
  traceId?: string
  limit?: number
  signal?: AbortSignal
}
const text = (raw: RawRecord, ...keys: string[]): string => {
  const value = pick<unknown>(raw, ...keys)
  return typeof value === 'string' ? value : ''
}
const count = (raw: RawRecord, ...keys: string[]): number | null => {
  const original = pick<unknown>(raw, ...keys)
  if (typeof original !== 'number' && (typeof original !== 'string' || !/^[0-9]+$/.test(original))) return null
  const value = num(original)
  return value != null && Number.isSafeInteger(value) && value >= 0 ? value : null
}
const at = (raw: RawRecord, ...keys: string[]): string | null => text(raw, ...keys) || null
const projectionFields = new Set([
  'symbol',
  'stock_code',
  'code',
  'signal_id',
  'order_id',
  'client_order_id',
  'fill_id',
  'plan_id',
  'status',
  'side',
  'kind',
  'stage',
  'level',
  'account_type',
  'count',
  'seq',
  'stale',
  'freq',
  'as_of',
])
function projection(raw: RawRecord): Record<string, string> {
  const value = pick<unknown>(raw, 'payload')
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return Object.fromEntries(
    Object.entries(value).filter(
      ([key, item]) => projectionFields.has(key) && typeof item === 'string' && item.length <= 128,
    ),
  ) as Record<string, string>
}
/** Unknown or missing metrics stay null; counters are ingress receipts, never consumer acknowledgements. */
export function normalizeObservability(value: unknown): ObservabilitySnapshot {
  const raw = (value && typeof value === 'object' ? value : {}) as RawRecord
  const schemaVersion = count(raw, 'schemaVersion', 'schema_version')
  const scope = text(raw, 'scope')
  const supported = schemaVersion === 1 && scope === 'admin_gateway_ingress'
  const out: ObservabilitySnapshot = {
    schemaVersion,
    available: supported && raw.available === true,
    scope,
    serverEpoch: text(raw, 'serverEpoch', 'server_epoch'),
    asOf: text(raw, 'asOf', 'as_of'),
    startedAt: at(raw, 'startedAt', 'started_at'),
    windowStart: at(raw, 'windowStart', 'window_start'),
    windowSeconds: count(raw, 'windowSeconds', 'window_seconds'),
    windowBasis: text(raw, 'windowBasis', 'window_basis'),
    windowObservedSeconds: (() => {
      const value = pick<unknown>(raw, 'windowObservedSeconds', 'window_observed_seconds')
      const seconds = typeof value === 'number' ? value : null
      return seconds != null && Number.isFinite(seconds) && seconds >= 0 && seconds <= 60 ? seconds : null
    })(),
    windowComplete: raw.windowComplete === true || raw.window_complete === true,
    natsConnected: raw.natsConnected === true || raw.nats_connected === true,
    registeredRoutes: asList<RawRecord>(raw, 'registeredRoutes', 'registered_routes').map((row) => ({
      subject: text(row, 'subject'),
      topic: text(row, 'topic'),
      durable: text(row, 'durable'),
      transport: text(row, 'transport'),
      observation: text(row, 'observation'),
      bindingState: text(row, 'bindingState', 'binding_state'),
      bindingUpdatedAt: at(row, 'bindingUpdatedAt', 'binding_updated_at'),
    })),
    edges: asList<RawRecord>(raw, 'edges').map((row) => ({
      subject: text(row, 'subject'),
      source: text(row, 'source'),
      topic: text(row, 'topic'),
      receivedTotal: count(row, 'receivedTotal', 'received_total'),
      acceptedTotal: count(row, 'acceptedTotal', 'accepted_total'),
      bytesTotal: count(row, 'bytesTotal', 'bytes_total'),
      errorsTotal: count(row, 'errorsTotal', 'errors_total'),
      windowCount: count(row, 'windowCount', 'window_count'),
      windowBytes: count(row, 'windowBytes', 'window_bytes'),
      windowErrors: count(row, 'windowErrors', 'window_errors'),
      lastReceivedAt: at(row, 'lastReceivedAt', 'last_received_at'),
      lastEventId: text(row, 'lastEventId', 'last_event_id'),
      lastTraceId: text(row, 'lastTraceId', 'last_trace_id'),
    })),
    samples: asList<RawRecord>(raw, 'samples').map((row) => ({
      receivedAt: text(row, 'receivedAt', 'received_at'),
      subject: text(row, 'subject'),
      source: text(row, 'source'),
      topic: text(row, 'topic'),
      eventId: text(row, 'eventId', 'event_id'),
      traceId: text(row, 'traceId', 'trace_id'),
      schemaVersion: count(row, 'schemaVersion', 'schema_version'),
      causationId: text(row, 'causationId', 'causation_id'),
      correlationId: text(row, 'correlationId', 'correlation_id'),
      entityId: text(row, 'entityId', 'entity_id'),
      entityVersion: count(row, 'entityVersion', 'entity_version'),
      eventTime: at(row, 'eventTime', 'event_time'),
      sourceTime: at(row, 'sourceTime', 'source_time'),
      publisherReceivedAt: at(row, 'publisherReceivedAt', 'publisher_received_at'),
      publishedAt: at(row, 'publishedAt', 'published_at'),
      payload: projection(row),
      payloadStatus: text(row, 'payloadStatus', 'payload_status'),
    })),
    errorsTotal: count(raw, 'errorsTotal', 'errors_total'),
    samplesTruncated: raw.samplesTruncated === true || raw.samples_truncated === true,
    sampleCapacity: count(raw, 'sampleCapacity', 'sample_capacity'),
    counterCapacity: count(raw, 'counterCapacity', 'counter_capacity'),
    counterOverflowTotal: count(raw, 'counterOverflowTotal', 'counter_overflow_total'),
    statisticsScope: text(raw, 'statisticsScope', 'statistics_scope'),
    unsupported: asList<unknown>(raw, 'unsupported').filter((item): item is string => typeof item === 'string'),
    note: supported ? text(raw, 'note') : '观测合同缺失或版本不支持，不能判断真实流量',
  }
  if (!out.available) {
    out.edges = []
    out.samples = []
    out.registeredRoutes = []
  }
  return out
}

export async function getObservability(query: ObservabilityQuery = {}): Promise<ObservabilitySnapshot> {
  const value = await request('/api/ops/observability', {
    signal: query.signal,
    query: { subject: query.subject, trace_id: query.traceId, limit: query.limit },
  })
  return normalizeObservability(value)
}
