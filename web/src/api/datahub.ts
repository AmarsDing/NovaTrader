import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { DataSource, QualityReport } from './types/datahub'

export type { BackfillTask, DataSource, QualityReport } from './types/datahub'

export async function listSources(): Promise<DataSource[]> {
  const data = await request('/api/datahub/sources')
  return asList(data, 'items', 'sources').map(normalizeSource)
}

export function setSourceEnabled(source: string, enabled: boolean): Promise<unknown> {
  return request(`/api/datahub/sources/${encodeURIComponent(source)}/enabled`, {
    method: 'POST',
    json: { enabled },
  })
}

export function triggerBackfill(body: Record<string, unknown>): Promise<unknown> {
  return request('/api/datahub/backfill', { method: 'POST', json: body })
}

export function collectDomain(domain: string, body: Record<string, unknown> = {}): Promise<unknown> {
  return request(`/api/datahub/collect/${encodeURIComponent(domain)}`, { method: 'POST', json: body })
}

export async function getQuality(query: Record<string, unknown> = {}): Promise<QualityReport[]> {
  const data = await request('/api/datahub/quality', { query })
  return asList(data, 'items', 'reports').map((raw) => ({
    domain: pick<string>(raw, 'domain') || '',
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    total: num(pick(raw, 'total')),
    ok: num(pick(raw, 'ok', 'okCount', 'ok_count')),
    missing: num(pick(raw, 'missing')),
    stale: num(pick(raw, 'stale')),
    detail: pick<string>(raw, 'detail', 'message') || '',
  }))
}

function optionalNum(raw: RawRecord, ...keys: string[]): number | null {
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(raw, key) && raw[key] != null && raw[key] !== '') return num(raw[key])
  }
  return null
}

function normalizeSource(raw: RawRecord): DataSource {
  const source = pick<string>(raw, 'source', 'name', 'id') || ''
  const domain = pick<string>(raw, 'domain', 'kind', 'type') || ''
  const state = pick<string>(raw, 'state', 'status') || ''
  const lastError = pick<string>(raw, 'lastError', 'last_error', 'detail', 'message') || ''
  return {
    id: domain ? `${domain}:${source}` : source,
    name: source,
    kind: domain,
    enabled: raw.enabled === true,
    status: state,
    detail: lastError || state,
    successRate: optionalNum(raw, 'successRate', 'success_rate'),
    latencyMs: optionalNum(raw, 'lastLatencyMs', 'last_latency_ms', 'avgLatencyMs', 'avg_latency_ms'),
    quotaUsed: optionalNum(raw, 'quotaUsed', 'quota_used'),
    quotaLimit: optionalNum(raw, 'quotaLimit', 'quota_limit'),
    active: raw.active === true,
    lastAt:
      pick<string>(raw, 'lastSuccessAt', 'last_success_at', 'lastAt', 'last_at', 'lastFailureAt', 'last_failure_at') ||
      '',
  }
}
