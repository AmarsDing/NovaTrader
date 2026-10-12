import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'

export interface RiskStateView {
  mode: string
  killActive: boolean
  killSource: string
  killReason: string
  killAt: string
  /** 风控参数（后端 map<string,string>，值都是字符串形式的数字/布尔） */
  params: Record<string, string>
  buysToday: number | null
  marketBreaker: string
  /** 账户类型 → 当日锁存的熔断原因（StateReply.breakers） */
  breakers: Record<string, string>
  /** 账户类型 → 最近快照时刻（StateReply.account_as_of） */
  accountAsOf: Record<string, string>
  /** 风控看到的最后一笔行情时刻（StateReply.last_quote_at） */
  lastQuoteAt: string
}

export interface KillBody {
  reason?: string
  source?: string
  operator?: string
  [key: string]: unknown
}

export interface ModeBody {
  mode: string
  operator?: string
  [key: string]: unknown
}

export interface ParamBody {
  key: string
  value: unknown
  operator?: string
  [key: string]: unknown
}

export async function getRiskState(): Promise<RiskStateView> {
  const raw = await request('/api/risk/state')
  const record = (raw && typeof raw === 'object' ? raw : {}) as RawRecord
  const kill = (record.killSwitch || record.kill_switch || {}) as RawRecord
  return {
    mode: pick<string>(record, 'mode') || '',
    killActive: !!kill.active,
    killSource: pick<string>(kill, 'source') || '',
    killReason: pick<string>(kill, 'reason') || '',
    killAt: pick<string>(kill, 'at') || '',
    params: stringMap(record.params),
    buysToday: num(pick(record, 'buysToday', 'buys_today')),
    marketBreaker: pick<string>(record, 'marketBreaker', 'market_breaker') || '',
    breakers: stringMap(record.breakers),
    accountAsOf: stringMap(record.accountAsOf ?? record.account_as_of),
    lastQuoteAt: pick<string>(record, 'lastQuoteAt', 'last_quote_at') || '',
  }
}

/** proto 的 map<string,string>：只收字符串值，别的类型丢掉，不猜 */
function stringMap(value: unknown): Record<string, string> {
  const out: Record<string, string> = {}
  if (!value || typeof value !== 'object') return out
  for (const [key, item] of Object.entries(value as RawRecord)) {
    if (typeof item === 'string') out[key] = item
    else if (typeof item === 'number' || typeof item === 'boolean') out[key] = String(item)
  }
  return out
}

export function triggerKillSwitch(body: KillBody): Promise<unknown> {
  return request('/api/risk/killswitch', { method: 'POST', json: body })
}

/* ---------- R01–R15 规则链预检（M08） ---------- */

export interface RuleHit {
  code: string
  name: string
  pass: boolean
  detail: string
}

export interface CheckReply {
  pass: boolean
  rule: string
  detail: string
  hits: RuleHit[]
  traceId: string
}

export interface CheckBody {
  symbol?: string
  side?: string
  price?: number
  qty?: number
  account?: string
  [key: string]: unknown
}

export async function checkOrder(body: CheckBody): Promise<CheckReply> {
  const raw = (await request('/api/risk/check', { method: 'POST', json: body })) as RawRecord
  const record = (raw && typeof raw === 'object' ? raw : {}) as RawRecord
  const hits = Array.isArray(record.results)
    ? record.results
    : Array.isArray(record.hits)
      ? record.hits
      : Array.isArray(record.rules)
        ? record.rules
        : []
  return {
    pass: !!(record.approved ?? record.pass ?? record.ok ?? record.allowed),
    rule: pick<string>(record, 'rule', 'blockedBy', 'blocked_by') || '',
    detail: pick<string>(record, 'reason', 'detail', 'message') || '',
    hits: (hits as RawRecord[]).map((h) => ({
      code: pick<string>(h, 'rule', 'code', 'id') || '',
      name: pick<string>(h, 'name', 'title') || '',
      pass: !!(h.pass ?? h.ok),
      detail: pick<string>(h, 'note', 'detail', 'reason', 'message') || '',
    })),
    traceId: pick<string>(record, 'traceId', 'trace_id') || '',
  }
}

/* ---------- 风控日报（M08） ---------- */

export interface RiskReportRow {
  metric: string
  value: string
  note: string
}

/** 规则命中次数（DailyReportReply.rejects） */
export interface RuleCount {
  rule: string
  name: string
  count: number | null
}

/** 风控事件（DailyReportReply.events）：kind = killswitch / mode / breaker / reject_rate */
export interface RiskEventItem {
  kind: string
  active: boolean
  mode: string
  source: string
  reason: string
  operator: string
  at: string
}

export interface RiskReport {
  date: string
  rows: RiskReportRow[]
  summary: string
  generatedAt: string
  checks: number | null
  approved: number | null
  rejected: number | null
  /** 放行但被缩量的笔数 */
  adjusted: number | null
  p99Ms: number | null
  rejects: RuleCount[]
  events: RiskEventItem[]
}

/**
 * 风控日报。`date` 传 YYYY-MM-DD 可取历史某日（急停历史「近 7 日」就是逐日取）。
 * 后端 DailyReportReply 没有 rows/summary 字段：指标行由真实计数拼出，
 * 摘要保持后端原样（今天没有就是空串，不编一段话）。
 */
export async function getRiskReport(date = ''): Promise<RiskReport> {
  const raw = await request('/api/risk/report', { query: { date } })
  const record = (raw && typeof raw === 'object' ? raw : {}) as RawRecord
  const rejects = asList<RawRecord>(record, 'rejects').map((item) => ({
    rule: pick<string>(item, 'rule', 'code') || '',
    name: pick<string>(item, 'name', 'title') || '',
    count: num(pick(item, 'count')),
  }))
  const events = asList<RawRecord>(record, 'events').map((item) => ({
    kind: pick<string>(item, 'kind') || '',
    active: !!item.active,
    mode: pick<string>(item, 'mode') || '',
    source: pick<string>(item, 'source') || '',
    reason: pick<string>(item, 'reason') || '',
    operator: pick<string>(item, 'operator') || '',
    at: pick<string>(item, 'at') || '',
  }))
  const checks = num(pick(record, 'checks'))
  const approved = num(pick(record, 'approved'))
  const rejected = num(pick(record, 'rejected'))
  const adjusted = num(pick(record, 'adjusted'))
  const p99Us = num(pick(record, 'p99LatencyUs', 'p99_latency_us'))
  const given = asList<RawRecord>(record, 'rows', 'items').map((r) => ({
    metric: pick<string>(r, 'metric', 'name', 'key') || '',
    value: pick<string>(r, 'value', 'val') || '',
    note: pick<string>(r, 'note', 'comment') || '',
  }))
  const rows = given.length > 0 ? given : reportRows(checks, approved, rejected, adjusted, p99Us, rejects)
  return {
    date: pick<string>(record, 'date', 'tradeDate', 'trade_date') || '',
    rows,
    summary: pick<string>(record, 'summary', 'digest') || '',
    generatedAt: pick<string>(record, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
    checks,
    approved,
    rejected,
    adjusted,
    p99Ms: p99Us == null ? null : p99Us / 1000,
    rejects,
    events,
  }
}

/** 后端只给计数，不给指标行：这里按计数拼行，缺的计数写「未返回」而不是 0 */
function reportRows(
  checks: number | null,
  approved: number | null,
  rejected: number | null,
  adjusted: number | null,
  p99Us: number | null,
  rejects: RuleCount[],
): RiskReportRow[] {
  const text = (value: number | null) => (value == null ? '未返回' : String(Math.round(value)))
  const rows: RiskReportRow[] = [
    { metric: '风控校验次数', value: text(checks), note: '' },
    { metric: '放行', value: text(approved), note: adjusted == null ? '' : `其中缩量放行 ${Math.round(adjusted)}` },
    { metric: '拒绝', value: text(rejected), note: '' },
    { metric: 'P99 延迟', value: p99Us == null ? '未返回' : `${(p99Us / 1000).toFixed(1)} ms`, note: '' },
  ]
  for (const item of rejects) {
    rows.push({ metric: `${item.rule} ${item.name}`.trim(), value: text(item.count), note: '拒绝次数' })
  }
  return rows
}

export function resetKillSwitch(body: KillBody): Promise<unknown> {
  return request('/api/risk/killswitch/reset', { method: 'POST', json: body })
}

export function setMode(body: ModeBody): Promise<unknown> {
  return request('/api/risk/mode', { method: 'POST', json: body })
}

export function updateParam(body: ParamBody): Promise<unknown> {
  return request('/api/risk/params', { method: 'POST', json: body })
}
