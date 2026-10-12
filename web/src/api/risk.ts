import { request } from '@/api/http'
import { num, pick, type RawRecord } from '@/utils/format'

export interface RiskStateView {
  mode: string
  killActive: boolean
  killSource: string
  killReason: string
  killAt: string
  params: Record<string, unknown>
  buysToday: number | null
  marketBreaker: string
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
    params: record.params && typeof record.params === 'object' ? (record.params as RawRecord) : {},
    buysToday: num(pick(record, 'buysToday', 'buys_today')),
    marketBreaker: pick<string>(record, 'marketBreaker', 'market_breaker') || '',
  }
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

export interface RiskReport {
  date: string
  rows: RiskReportRow[]
  summary: string
  generatedAt: string
}

export async function getRiskReport(): Promise<RiskReport> {
  const raw = await request('/api/risk/report')
  const record = (raw && typeof raw === 'object' ? raw : {}) as RawRecord
  const rows = Array.isArray(record.rows) ? record.rows : Array.isArray(record.items) ? record.items : []
  return {
    date: pick<string>(record, 'date', 'tradeDate', 'trade_date') || '',
    rows: (rows as RawRecord[]).map((r) => ({
      metric: pick<string>(r, 'metric', 'name', 'key') || '',
      value: pick<string>(r, 'value', 'val') || '',
      note: pick<string>(r, 'note', 'comment') || '',
    })),
    summary: pick<string>(record, 'summary', 'digest') || '',
    generatedAt: pick<string>(record, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
  }
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
