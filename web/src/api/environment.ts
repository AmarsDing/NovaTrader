import { request } from './http'
import { pick } from '@/utils/format'

type Obj = Record<string, unknown>
function obj(v: unknown): Obj {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Obj) : {}
}
function field(v: unknown, snake: string, camel = snake.replace(/_([a-z])/g, (_, c: string) => c.toUpperCase())) {
  return pick(v, snake, camel)
}
function str(v: unknown) {
  return typeof v === 'string' ? v : ''
}
function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : null
}
function counter(v: unknown): string | null {
  if (typeof v === 'number') return Number.isSafeInteger(v) && v >= 0 ? String(v) : null
  return typeof v === 'string' && /^\d{1,20}$/.test(v) ? v : null
}
function texts(v: unknown) {
  return Array.isArray(v) ? v.filter((s): s is string => typeof s === 'string') : []
}
export interface EnvironmentOperation {
  key: string
  kind: string
  input: string
  completion: string
  inflight: string | null
  received: string | null
  queued: string | null
  coalesced: string | null
  attempted: string | null
  succeeded: string | null
  failed: string | null
  rejected: string | null
  cancelled: string | null
  skipped: string | null
  p95UpperMs: number | null
  lastCompletedAt: string
}
export interface ProcessObservation {
  instanceId: string
  host: string
  os: string
  arch: string
  sampledAt: string
  startedAt: string
  windowSeconds: number | null
  observedSeconds: number | null
  windowComplete: boolean
  windowBasis: string
  goroutines: string | null
  heapBytes: string | null
  operations: EnvironmentOperation[]
  unsupported: string[]
}
export interface EnvironmentService {
  service: string
  state: string
  checkedAt: string
  probeDurationMs: number | null
  note: string
  observation: ProcessObservation | null
}
export interface EnvironmentSnapshot {
  sampledAt: string
  cacheExpiresAt: string
  cached: boolean
  note: string
  unsupported: string[]
  services: EnvironmentService[]
}
function observation(v: unknown, service: string): ProcessObservation {
  if (
    field(v, 'schema_version') !== 1 ||
    field(v, 'scope') !== 'process_consumer_operations' ||
    field(v, 'service') !== service
  )
    throw new Error('进程观测合同或服务身份不匹配')
  const ops = field(v, 'operations')
  if (!Array.isArray(ops) || ops.length > 64) throw new Error('进程操作目录无效')
  return {
    instanceId: str(field(v, 'instance_id')),
    host: str(field(v, 'host')),
    os: str(field(v, 'os')),
    arch: str(field(v, 'arch')),
    sampledAt: str(field(v, 'sampled_at')),
    startedAt: str(field(v, 'started_at')),
    windowSeconds: num(field(v, 'window_seconds')),
    observedSeconds: num(field(v, 'window_observed_seconds')),
    windowComplete: field(v, 'window_complete') === true,
    windowBasis: str(field(v, 'window_basis')),
    goroutines: counter(field(v, 'goroutines')),
    heapBytes: counter(field(v, 'heap_alloc_bytes')),
    unsupported: texts(field(v, 'unsupported')),
    operations: ops.map((op) => {
      const w = obj(field(op, 'window'))
      return {
        key: str(field(op, 'key')),
        kind: str(field(op, 'kind')),
        input: str(field(op, 'input')),
        completion: str(field(op, 'completion_scope')),
        inflight: counter(field(op, 'inflight')),
        received: counter(field(w, 'received')),
        queued: counter(field(w, 'queued')),
        coalesced: counter(field(w, 'coalesced')),
        attempted: counter(field(w, 'attempted')),
        succeeded: counter(field(w, 'succeeded')),
        failed: counter(field(w, 'failed')),
        rejected: counter(field(w, 'rejected')),
        cancelled: counter(field(w, 'cancelled')),
        skipped: counter(field(w, 'skipped')),
        p95UpperMs: num(field(w, 'duration_p95_upper_ms')),
        lastCompletedAt: str(field(op, 'last_completed_at')),
      }
    }),
  }
}
export function parseEnvironment(v: unknown): EnvironmentSnapshot {
  if (field(v, 'schema_version') !== 1 || field(v, 'scope') !== 'configured_service_processes')
    throw new Error('环境观测合同未核验')
  const rows = field(v, 'services')
  if (!Array.isArray(rows) || rows.length > 32) throw new Error('环境服务目录无效')
  const names = new Set<string>()
  const services = rows.map((r): EnvironmentService => {
    const service = str(field(r, 'service')),
      state = str(field(r, 'state'))
    if (
      !service ||
      names.has(service) ||
      !['observed', 'offline', 'unsupported', 'unconfigured', 'invalid', 'unknown'].includes(state)
    )
      throw new Error('环境服务身份或状态无效')
    names.add(service)
    return {
      service,
      state,
      checkedAt: str(field(r, 'checked_at')),
      probeDurationMs: num(field(r, 'probe_duration_ms')),
      note: str(field(r, 'note')),
      observation: state === 'observed' ? observation(field(r, 'observation'), service) : null,
    }
  })
  return {
    services,
    sampledAt: str(field(v, 'sampled_at')),
    cacheExpiresAt: str(field(v, 'cache_expires_at')),
    cached: field(v, 'cached') === true,
    note: str(field(v, 'note')),
    unsupported: texts(field(v, 'unsupported')),
  }
}
export async function getEnvironment(signal?: AbortSignal) {
  return parseEnvironment(await request('/api/ops/environment', { signal }))
}
