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

/** 影子统计某一持有窗口的聚合（比率口径）。 */
export interface ShadowHorizon {
  horizon: number
  count: number | null
  avgReturn: number | null
  winRate: number | null
}

/** 影子统计按信号分数桶的聚合（比率口径）。 */
export interface ShadowBucket {
  bucket: string
  count: number | null
  avgT1: number | null
  avgT3: number | null
  avgT5: number | null
}

/** 影子统计（策略影子仓后验，非实盘对比）。 */
export interface ShadowStats {
  total: number | null
  pending: number | null
  monotonic: boolean
  horizons: ShadowHorizon[]
  buckets: ShadowBucket[]
}

export async function getShadowStats(): Promise<ShadowStats> {
  const data = await request('/api/backtest/shadow/stats')
  const raw = (data && typeof data === 'object' ? data : {}) as RawRecord
  return {
    total: num(pick(raw, 'total')),
    pending: num(pick(raw, 'pending')),
    monotonic: !!raw.monotonic,
    horizons: asList<RawRecord>(raw, 'horizons').map((row) => ({
      horizon: num(pick(row, 'horizon')) ?? 0,
      count: num(pick(row, 'count')),
      avgReturn: num(pick(row, 'avgReturn', 'avg_return')),
      winRate: num(pick(row, 'winRate', 'win_rate')),
    })),
    buckets: asList<RawRecord>(raw, 'buckets').map((row) => ({
      bucket: pick<string>(row, 'bucket') || '',
      count: num(pick(row, 'count')),
      avgT1: num(pick(row, 'avgT1', 'avg_t1')),
      avgT3: num(pick(row, 'avgT3', 'avg_t3')),
      avgT5: num(pick(row, 'avgT5', 'avg_t5')),
    })),
  }
}

/** 自省记录（盘后窗口统计 + 触发原因）。 */
export interface Introspection {
  id: string
  tradeDate: string
  book: string
  windowDays: number | null
  sampleCount: number | null
  winRate: number | null
  avgReturn: number | null
  maxDrawdown: number | null
  missedCount: number | null
  attribution: Record<string, number>
  triggered: boolean
  triggerReason: string
  note: string
  createdAt: string
}

export async function listIntrospections(): Promise<Introspection[]> {
  const data = await request('/api/backtest/introspections')
  return asList<RawRecord>(data, 'items', 'introspections').map((raw) => ({
    id: String(pick(raw, 'id') ?? ''),
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    book: pick<string>(raw, 'book') || '',
    windowDays: num(pick(raw, 'windowDays', 'window_days')),
    sampleCount: num(pick(raw, 'sampleCount', 'sample_count')),
    winRate: num(pick(raw, 'winRate', 'win_rate')),
    avgReturn: num(pick(raw, 'avgReturn', 'avg_return')),
    maxDrawdown: num(pick(raw, 'maxDrawdown', 'max_drawdown')),
    missedCount: num(pick(raw, 'missedCount', 'missed_count')),
    attribution:
      raw.attribution && typeof raw.attribution === 'object'
        ? Object.fromEntries(Object.entries(raw.attribution as RawRecord).map(([k, v]) => [k, num(v) ?? 0]))
        : {},
    triggered: !!raw.triggered,
    triggerReason: pick<string>(raw, 'triggerReason', 'trigger_reason') || '',
    note: pick<string>(raw, 'note') || '',
    createdAt: pick<string>(raw, 'createdAt', 'created_at') || '',
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
