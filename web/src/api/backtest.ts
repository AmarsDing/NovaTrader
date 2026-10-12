import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { BacktestReport, BacktestTask, BacktestTrade, StrategyMeta } from './types/backtest'

export type {
  BacktestReport,
  BacktestTask,
  BacktestTrade,
  EquityPoint,
  ReportMetrics,
  StrategyMeta,
} from './types/backtest'

export async function listStrategies(): Promise<StrategyMeta[]> {
  const data = await request('/api/backtest/strategies')
  return asList(data, 'strategies').map((raw) => ({
    name: pick<string>(raw, 'name') || '',
    title: pick<string>(raw, 'title') || pick<string>(raw, 'name') || '',
  }))
}

export async function listTasks(): Promise<BacktestTask[]> {
  const data = await request('/api/backtest/tasks', { query: { limit: 30 } })
  return asList(data, 'tasks').map(normalizeTask)
}

export function createTask(body: Record<string, unknown>): Promise<unknown> {
  return request('/api/backtest/tasks', { method: 'POST', json: body })
}

export async function getReport(id: string): Promise<BacktestReport> {
  const data = await request(`/api/backtest/tasks/${encodeURIComponent(id)}/report`)
  const record = (data && typeof data === 'object' ? data : {}) as RawRecord
  const report = (record.report && typeof record.report === 'object' ? record.report : record) as RawRecord
  const metrics = (report.metrics && typeof report.metrics === 'object' ? report.metrics : {}) as RawRecord
  const equity = Array.isArray(report.equity) ? (report.equity as RawRecord[]) : []
  return {
    metrics: {
      totalReturn: num(pick(metrics, 'total_return', 'totalReturn')),
      annualReturn: num(pick(metrics, 'annual_return', 'annualReturn')),
      maxDrawdown: num(pick(metrics, 'max_drawdown', 'maxDrawdown')),
      sharpe: num(pick(metrics, 'sharpe')),
      winRate: num(pick(metrics, 'win_rate', 'winRate')),
      profitFactor: num(pick(metrics, 'profit_factor', 'profitFactor')),
      rounds: num(pick(metrics, 'rounds')),
    },
    equity: equity.map((raw) => ({
      day: String(pick(raw, 'day') || '').slice(0, 10),
      equity: num(pick(raw, 'equity')),
      benchmark: num(pick(raw, 'benchmark')),
      drawdown: num(pick(raw, 'drawdown')),
    })),
    warnings: Array.isArray(report.warnings) ? report.warnings.map(String) : [],
  }
}

export async function listTrades(id: string): Promise<BacktestTrade[]> {
  const data = await request(`/api/backtest/tasks/${encodeURIComponent(id)}/trades`, { query: { limit: 200 } })
  return asList(data, 'trades').map((raw) => ({
    seq: num(pick(raw, 'seq')),
    symbol: pick<string>(raw, 'symbol') || '',
    side: pick<string>(raw, 'side') || '',
    time: pick<string>(raw, 'time') || '',
    price: num(pick(raw, 'price')),
    quantity: num(pick(raw, 'quantity')),
    amount: num(pick(raw, 'amount')),
    pnl: num(pick(raw, 'pnl')),
    reason: pick<string>(raw, 'reason') || '',
  }))
}

function normalizeTask(raw: RawRecord): BacktestTask {
  const summary = (raw.summary && typeof raw.summary === 'object' ? raw.summary : {}) as RawRecord
  return {
    id: pick<string>(raw, 'id') || '',
    name: pick<string>(raw, 'name') || '',
    kind: pick<string>(raw, 'kind') || '',
    strategy: pick<string>(raw, 'strategy') || '',
    freq: pick<string>(raw, 'freq') || '',
    status: pick<string>(raw, 'status') || '',
    progress: num(pick(raw, 'progress')),
    message: pick<string>(raw, 'message') || pick<string>(raw, 'error') || '',
    start: pick<string>(raw, 'startDate', 'start_date') || '',
    end: pick<string>(raw, 'endDate', 'end_date') || '',
    totalReturn: num(pick(summary, 'totalReturn', 'total_return')),
    maxDrawdown: num(pick(summary, 'maxDrawdown', 'max_drawdown')),
    winRate: num(pick(summary, 'winRate', 'win_rate')),
    hasReport: !!(summary.hasReport || summary.has_report),
  }
}

/* ---------- M07 自进化：影子 / 自省 / 提案 / 准入 ---------- */

/** 影子统计（实盘 vs 回测口径对比）。 */
export interface ShadowStat {
  name: string
  live: number | null
  shadow: number | null
  delta: number | null
}

export async function getShadowStats(): Promise<ShadowStat[]> {
  const data = await request('/api/backtest/shadow/stats')
  return asList(data, 'items', 'stats', 'rows').map((raw) => ({
    name: pick<string>(raw, 'name', 'metric') || '',
    live: num(pick(raw, 'live')),
    shadow: num(pick(raw, 'shadow')),
    delta: num(pick(raw, 'delta', 'diff')),
  }))
}

/** 自省记录。 */
export interface Introspection {
  id: string
  at: string
  title: string
  body: string
  kind: string
}

export async function listIntrospections(): Promise<Introspection[]> {
  const data = await request('/api/backtest/introspections')
  return asList(data, 'items', 'introspections').map((raw) => ({
    id: pick<string>(raw, 'id') || '',
    at: pick<string>(raw, 'at', 'createdAt', 'created_at', 'time') || '',
    title: pick<string>(raw, 'title', 'subject') || '',
    body: pick<string>(raw, 'body', 'text', 'content') || '',
    kind: pick<string>(raw, 'kind', 'type') || '',
  }))
}

export function runIntrospection(): Promise<unknown> {
  return request('/api/backtest/introspections/run', { method: 'POST', json: {} })
}

/** 进化提案（approve/reject 需 owner）。 */
export interface Proposal {
  id: string
  title: string
  body: string
  strategy: string
  status: string
  createdAt: string
  decidedAt: string
  reason: string
}

export async function listProposals(): Promise<Proposal[]> {
  const data = await request('/api/backtest/proposals')
  return asList(data, 'items', 'proposals').map((raw) => ({
    id: pick<string>(raw, 'id') || '',
    title: pick<string>(raw, 'title', 'subject') || '',
    body: pick<string>(raw, 'body', 'text', 'content') || '',
    strategy: pick<string>(raw, 'strategy') || '',
    status: pick<string>(raw, 'status', 'state') || '',
    createdAt: pick<string>(raw, 'createdAt', 'created_at') || '',
    decidedAt: pick<string>(raw, 'decidedAt', 'decided_at') || '',
    reason: pick<string>(raw, 'reason') || '',
  }))
}

export function approveProposal(id: string, reason = ''): Promise<unknown> {
  return request(`/api/backtest/proposals/${encodeURIComponent(id)}/approve`, { method: 'POST', json: { reason } })
}

export function rejectProposal(id: string, reason = ''): Promise<unknown> {
  return request(`/api/backtest/proposals/${encodeURIComponent(id)}/reject`, { method: 'POST', json: { reason } })
}

/** 模拟盘 20 交易日准入检查表。 */
export interface AdmissionItem {
  key: string
  label: string
  pass: boolean
  status: string
  detail: string
}

export interface Admission {
  ready: boolean
  tradingDays: number | null
  requiredDays: number | null
  items: AdmissionItem[]
}

export async function getAdmission(strategy = ''): Promise<Admission> {
  const data = await request('/api/backtest/admission', { query: strategy ? { strategy } : {} })
  const record = (data && typeof data === 'object' ? data : {}) as RawRecord
  const items = Array.isArray(record.items) ? record.items : Array.isArray(record.checks) ? record.checks : []
  const mapped = (items as RawRecord[]).map((r) => {
    const status = (pick<string>(r, 'status') || '').toLowerCase()
    return {
      key: pick<string>(r, 'key', 'id') || '',
      label: pick<string>(r, 'title', 'label', 'name') || '',
      pass: status === 'pass' || !!(r.pass ?? r.ok),
      status: status || (r.pass || r.ok ? 'pass' : 'fail'),
      detail: pick<string>(r, 'detail', 'reason', 'message') || '',
    }
  })
  const paper = mapped.find((item) => item.key === 'paper_days')
  const days = paper?.detail.match(/(\d+)/)
  const required = paper?.label.match(/(\d+)/)
  return {
    ready: !!(record.ready ?? record.admitted),
    tradingDays: num(pick(record, 'tradingDays', 'trading_days', 'days')) ?? (days ? Number(days[1]) : null),
    requiredDays: num(pick(record, 'requiredDays', 'required_days', 'target')) ?? (required ? Number(required[1]) : null),
    items: mapped,
  }
}
