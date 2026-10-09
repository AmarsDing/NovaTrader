import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { BacktestReport, BacktestTask, BacktestTrade, StrategyMeta } from './types/backtest'

export type { BacktestReport, BacktestTask, BacktestTrade, EquityPoint, ReportMetrics, StrategyMeta } from './types/backtest'

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
