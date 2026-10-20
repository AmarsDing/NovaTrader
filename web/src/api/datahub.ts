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

/**
 * 源目录（GET /api/datahub/source-catalog）：「每个源能提供什么数据」的单一事实源。
 * 费用档/凭证枚举缺失留 ''（前端显「—」），不用默认值冒充（没取到≠0）。
 */
export interface SourceManifest {
  id: string
  /** false = 已登记键位/已建未启用（静态表 declared 域透传，源墙可见可配）。 */
  registered: boolean
  domains: string[]
  feeTier: 'free' | 'freemium' | 'paid' | ''
  feeNote: string
  official: boolean
  requires: 'none' | 'token' | 'key' | 'sidecar' | ''
  via: string
  notes: string
  riskNote: string
}

/** 域降级链画像：yaml 默认链（运行态覆盖由 /api/config 兜出）+ 全部可挂该域的候选源。 */
export interface DomainChainInfo {
  name: string
  defaultChain: string[]
  candidates: string[]
}

export interface SourceCatalog {
  version: number | null
  generatedAt: string
  servicesNote: string
  sources: SourceManifest[]
  domains: DomainChainInfo[]
}

function strList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : []
}

function normalizeManifest(raw: RawRecord): SourceManifest {
  const feeTier = pick<string>(raw, 'fee_tier', 'feeTier') || ''
  const requires = pick<string>(raw, 'requires') || ''
  return {
    id: pick<string>(raw, 'name', 'id') || '',
    registered: raw.registered === true,
    domains: strList(pick(raw, 'domains')),
    feeTier: feeTier === 'free' || feeTier === 'freemium' || feeTier === 'paid' ? feeTier : '',
    feeNote: pick<string>(raw, 'fee_note', 'feeNote') || '',
    official: raw.official === true,
    requires: requires === 'none' || requires === 'token' || requires === 'key' || requires === 'sidecar' ? requires : '',
    via: pick<string>(raw, 'via') || '',
    notes: pick<string>(raw, 'notes') || '',
    riskNote: pick<string>(raw, 'risk_note', 'riskNote') || '',
  }
}

export async function getSourceCatalog(): Promise<SourceCatalog> {
  const data = await request('/api/datahub/source-catalog')
  const raw = (data && typeof data === 'object' ? data : {}) as RawRecord
  return {
    version: num(pick(raw, 'version')),
    generatedAt: pick<string>(raw, 'generated_at', 'generatedAt') || '',
    servicesNote: pick<string>(raw, 'services_note', 'servicesNote') || '',
    sources: asList(raw, 'sources').map(normalizeManifest),
    domains: asList(raw, 'domains').map((entry) => ({
      name: pick<string>(entry, 'name') || '',
      defaultChain: strList(pick(entry, 'default_chain', 'defaultChain')),
      candidates: strList(pick(entry, 'candidates')),
    })),
  }
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
