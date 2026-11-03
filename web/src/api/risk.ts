import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'

export interface RiskStateView {
  configRevision?: number | null
  configReady?: boolean
  engineReady?: boolean
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
    configRevision: num(pick(record, 'configRevision', 'config_revision')),
    configReady: typeof (record.configReady ?? record.config_ready) === 'boolean' ? (record.configReady ?? record.config_ready) as boolean : undefined,
    engineReady: typeof record.ready === 'boolean' ? record.ready : undefined,
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

/* ---------- G7 干跑试算：不下单的风控预检 + 金额/费用/现金影响 ---------- */

export interface PreviewBody {
  client_order_id?: string
  account_type: string
  symbol: string
  side: string
  price: number
  volume: number
  source?: string
  operator?: string
  [key: string]: unknown
}

export interface PreviewReply {
  approved: boolean
  blockRule: string
  reason: string
  /** 风控缩量后的股数（未缩量为申报数） */
  volume: number | null
  adjusted: boolean
  amount: number | null
  fee: number | null
  /** 买入为负、卖出为正 */
  cashDelta: number | null
  hits: RuleHit[]
}

export async function previewOrder(body: PreviewBody): Promise<PreviewReply> {
  const raw = (await request('/api/risk/preview', { method: 'POST', json: body })) as RawRecord
  const record = (raw && typeof raw === 'object' ? raw : {}) as RawRecord
  return {
    approved: !!record.approved,
    blockRule: pick<string>(record, 'blockRule', 'block_rule') || '',
    reason: pick<string>(record, 'reason', 'message') || '',
    volume: num(pick(record, 'volume')),
    adjusted: !!record.adjusted,
    amount: num(pick(record, 'amount')),
    fee: num(pick(record, 'fee')),
    cashDelta: num(pick(record, 'cashDelta', 'cash_delta')),
    hits: asList<RawRecord>(record, 'results').map((h) => ({
      code: pick<string>(h, 'rule', 'code') || '',
      name: pick<string>(h, 'name', 'title') || '',
      pass: !!(h.pass ?? h.ok),
      detail: pick<string>(h, 'note', 'detail', 'reason') || '',
    })),
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

/* ---------- BE-11 拒绝聚合：日报 TopN 与 N 日拦截率曲线（只读） ---------- */

export interface RejectDay {
  date: string
  checks: number
  rejected: number
  /** rejected/checks；checks 为 0 时是 0（后端口径） */
  rejectRate: number
}

export interface RejectTrend {
  /** 窗口结束日（YYYY-MM-DD，上海时区） */
  end: string
  days: number
  /** 按日期升序，窗口内每日一格（无数据日全 0） */
  trend: RejectDay[]
  /** 窗口内按规则聚合，count 降序、rule 升序兜底 */
  topRules: RuleCount[]
}

/** 拒绝聚合：end 空为今天（上海），days<=0 后端取默认 7，上限 31。 */
export async function getRejectTrend(end = '', days = 7): Promise<RejectTrend> {
  const query: Record<string, string> = {}
  if (end) query.end = end
  if (days > 0) query.days = String(days)
  const raw = await request('/api/risk/rejects/trend', { query })
  const record = (raw && typeof raw === 'object' ? raw : {}) as RawRecord
  return {
    end: pick<string>(record, 'end') || '',
    days: num(pick(record, 'days')) ?? 0,
    trend: asList<RawRecord>(record, 'trend').map((d) => ({
      date: pick<string>(d, 'date') || '',
      checks: num(pick(d, 'checks')) ?? 0,
      rejected: num(pick(d, 'rejected')) ?? 0,
      rejectRate: num(pick(d, 'rejectRate', 'reject_rate')) ?? 0,
    })),
    topRules: asList<RawRecord>(record, 'topRules', 'top_rules').map((r) => ({
      rule: pick<string>(r, 'rule') || '',
      name: pick<string>(r, 'name') || '',
      count: num(pick(r, 'count')),
    })),
  }
}

/* ---------- FE-R2 拒绝明细：抽屉逐条记录（只读，与 risk_check 行一一对应） ---------- */

export interface RejectRecordQuery {
  /** 窗口结束日 YYYY-MM-DD，空为今天（上海时区） */
  end?: string
  /** 回看天数（含结束日）；<=0 后端取默认 1，上限 31 */
  days?: number
  /** 规则号精确过滤（R00–R15），空为全部 */
  rule?: string
  /** 标的精确过滤，空为全部 */
  symbol?: string
  /** 操作人精确过滤，空为全部 */
  operator?: string
  /** 账户类型 SIM / LIVE，空为全部 */
  accountType?: string
  /** 拒绝原因子串（不区分大小写），空为全部 */
  reason?: string
  /** 分页偏移，<0 后端归 0 */
  offset?: number
  /** 页大小，<=0 后端取默认 50，上限 200 */
  limit?: number
}

export interface RejectRecordItem {
  clientOrderId: string
  accountType: string
  symbol: string
  /** buy / sell */
  side: string
  /** auto / watch / manual */
  source: string
  operator: string
  price: number
  volumeReq: number
  volumeFinal: number
  /** 触发拒绝的规则号 R00–R15 */
  rule: string
  /** 规则中文名（与 R01–R15 链一致） */
  ruleName: string
  reason: string
  latencyUs: number
  /** RFC3339 */
  createdAt: string
}

export interface RejectRecords {
  end: string
  days: number
  /** 过滤条件下窗口内拒绝总数 */
  total: number
  offset: number
  limit: number
  /** 按 created_at 降序 */
  items: RejectRecordItem[]
}

/** 拒绝明细：抽屉逐条记录，参数口径与后端一致（见 RejectRecordQuery 注释）。 */
export async function getRejectRecords(q: RejectRecordQuery = {}): Promise<RejectRecords> {
  const query: Record<string, string> = {}
  if (q.end) query.end = q.end
  if (q.days && q.days > 0) query.days = String(q.days)
  if (q.rule) query.rule = q.rule
  if (q.symbol) query.symbol = q.symbol
  if (q.operator) query.operator = q.operator
  if (q.accountType) query.account_type = q.accountType
  if (q.reason) query.reason = q.reason
  if (q.offset && q.offset > 0) query.offset = String(q.offset)
  if (q.limit && q.limit > 0) query.limit = String(q.limit)
  const raw = await request('/api/risk/rejects', { query })
  const record = (raw && typeof raw === 'object' ? raw : {}) as RawRecord
  return {
    end: pick<string>(record, 'end') || '',
    days: num(pick(record, 'days')) ?? 0,
    total: num(pick(record, 'total')) ?? 0,
    offset: num(pick(record, 'offset')) ?? 0,
    limit: num(pick(record, 'limit')) ?? 0,
    items: asList<RawRecord>(record, 'items').map((it) => ({
      clientOrderId: pick<string>(it, 'clientOrderId', 'client_order_id') || '',
      accountType: pick<string>(it, 'accountType', 'account_type') || '',
      symbol: pick<string>(it, 'symbol') || '',
      side: pick<string>(it, 'side') || '',
      source: pick<string>(it, 'source') || '',
      operator: pick<string>(it, 'operator') || '',
      price: num(pick(it, 'price')) ?? 0,
      volumeReq: num(pick(it, 'volumeReq', 'volume_req')) ?? 0,
      volumeFinal: num(pick(it, 'volumeFinal', 'volume_final')) ?? 0,
      rule: pick<string>(it, 'rule') || '',
      ruleName: pick<string>(it, 'ruleName', 'rule_name') || '',
      reason: pick<string>(it, 'reason') || '',
      latencyUs: num(pick(it, 'latencyUs', 'latency_us')) ?? 0,
      createdAt: pick<string>(it, 'createdAt', 'created_at') || '',
    })),
  }
}

export function setMode(body: ModeBody): Promise<unknown> {
  return request('/api/risk/mode', { method: 'POST', json: body })
}

export function updateParam(body: ParamBody): Promise<unknown> {
  return request('/api/risk/params', { method: 'POST', json: body })
}

/* ---------- 监控驾驶舱 P1：限额上限 × 当前用量（只读） ---------- */

export interface RiskLimitItem {
  key: string
  name: string
  unit: string
  limit: number
  used: number
  ratio: number
  note: string
}

export async function getRiskLimits(accountType = ''): Promise<RiskLimitItem[]> {
  const data = await request('/api/risk/limits', { query: accountType ? { account_type: accountType } : {} })
  return asList(data, 'items').map((raw) => ({
    key: pick<string>(raw, 'key') || '',
    name: pick<string>(raw, 'name') || '',
    unit: pick<string>(raw, 'unit') || '',
    limit: num(pick(raw, 'limit')) ?? -1,
    used: num(pick(raw, 'used')) ?? -1,
    ratio: num(pick(raw, 'ratio')) ?? -1,
    note: pick<string>(raw, 'note') || '',
  }))
}
