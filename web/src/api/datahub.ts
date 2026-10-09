import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { BackfillTask, DataSource, QualityReport } from './types/datahub'

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

function normalizeSource(raw: RawRecord): DataSource {
  return {
    id: pick<string>(raw, 'id', 'name') || '',
    name: pick<string>(raw, 'name') || '',
    kind: pick<string>(raw, 'kind', 'type') || '',
    enabled: raw.enabled !== false,
    status: pick<string>(raw, 'status') || '',
    detail: pick<string>(raw, 'detail', 'message') || '',
    lastAt: pick<string>(raw, 'lastAt', 'last_at', 'lastCollectAt', 'last_collect_at') || '',
  }
}
