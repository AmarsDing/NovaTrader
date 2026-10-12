import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { DataSource, QualityIssue } from './types/datahub'

export type { BackfillTask, DataSource, QualityIssue } from './types/datahub'

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

/**
 * 数据质量问题清单（ListQualityIssuesReply.issues）。
 * query 支持 trade_date / domain / limit；返回的是逐条问题，不是按域汇总。
 */
export async function getQuality(query: Record<string, unknown> = {}): Promise<QualityIssue[]> {
  const data = await request('/api/datahub/quality', { query })
  return asList(data, 'issues', 'items', 'reports').map((raw) => ({
    id: String(pick(raw, 'id') ?? ''),
    domain: pick<string>(raw, 'domain') || '',
    checkName: pick<string>(raw, 'checkName', 'check_name') || '',
    symbol: pick<string>(raw, 'symbol') || '',
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    severity: pick<string>(raw, 'severity') || '',
    source: pick<string>(raw, 'source') || '',
    otherSource: pick<string>(raw, 'otherSource', 'other_source') || '',
    message: pick<string>(raw, 'message', 'detail') || '',
    hits: num(pick(raw, 'hits')),
    lastSeen: pick<string>(raw, 'lastSeen', 'last_seen') || '',
  }))
}

function optionalNum(raw: RawRecord, ...keys: string[]): number | null {
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(raw, key) && raw[key] != null && raw[key] !== '') return num(raw[key])
  }
  return null
}

/**
 * SourceStatus → DataSource。成功时间与失败时间分开映射：
 * 混在一个 `lastAt` 里会让「最近成功」在只失败过的源上显示成失败时刻。
 */
function normalizeSource(raw: RawRecord): DataSource {
  const source = pick<string>(raw, 'source', 'name', 'id') || ''
  const domain = pick<string>(raw, 'domain', 'kind', 'type') || ''
  const state = pick<string>(raw, 'state', 'status') || ''
  const lastError = pick<string>(raw, 'lastError', 'last_error') || ''
  return {
    id: domain ? `${domain}:${source}` : source,
    name: source,
    kind: domain,
    enabled: raw.enabled === true,
    status: state,
    detail: lastError || state,
    successRate: optionalNum(raw, 'successRate', 'success_rate'),
    latencyMs: optionalNum(raw, 'lastLatencyMs', 'last_latency_ms'),
    avgLatencyMs: optionalNum(raw, 'avgLatencyMs', 'avg_latency_ms'),
    quotaUsed: optionalNum(raw, 'quotaUsed', 'quota_used'),
    quotaLimit: optionalNum(raw, 'quotaLimit', 'quota_limit'),
    active: raw.active === true,
    priority: optionalNum(raw, 'priority'),
    official: raw.official === true,
    consecutiveFailures: optionalNum(raw, 'consecutiveFailures', 'consecutive_failures'),
    successCount: optionalNum(raw, 'successCount', 'success_count'),
    failureCount: optionalNum(raw, 'failureCount', 'failure_count'),
    lastAt: pick<string>(raw, 'lastSuccessAt', 'last_success_at', 'lastAt', 'last_at') || '',
    lastFailureAt: pick<string>(raw, 'lastFailureAt', 'last_failure_at') || '',
    lastError,
  }
}
