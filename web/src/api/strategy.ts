import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { Candidate, ScanReply } from './types/strategy'

export type { Candidate, ScanReply } from './types/strategy'

export interface ScanBody {
  symbols?: string[]
  strategies?: string[]
  [key: string]: unknown
}

export interface BlacklistBody {
  symbol: string
  reason?: string
  operator?: string
  [key: string]: unknown
}

export async function getSignal(id: string): Promise<RawRecord> {
  return (await request(`/api/strategy/signals/${encodeURIComponent(id)}`)) as RawRecord
}

export function transitionSignal(id: string, body: Record<string, unknown>): Promise<unknown> {
  return request(`/api/strategy/signals/${encodeURIComponent(id)}/transition`, { method: 'POST', json: body })
}

export async function scan(body: ScanBody): Promise<ScanReply> {
  const raw = (await request('/api/strategy/scan', { method: 'POST', json: body })) as RawRecord
  return {
    traceId: pick<string>(raw, 'traceId', 'trace_id') || '',
    universe: num(pick(raw, 'universe')),
    matched: num(pick(raw, 'matched')),
    ranked: num(pick(raw, 'ranked')),
    aiCalls: num(pick(raw, 'aiCalls', 'ai_calls')),
    degraded: !!raw.degraded,
    signalIds: asList<string>(raw, 'signalIds', 'signal_ids'),
    sellSignalIds: asList<string>(raw, 'sellSignalIds', 'sell_signal_ids'),
    dropped: (raw.dropped && typeof raw.dropped === 'object' ? raw.dropped : {}) as Record<string, number>,
    misses: (raw.misses && typeof raw.misses === 'object' ? raw.misses : {}) as Record<string, number>,
    killSwitch: !!(raw.killSwitch || raw.kill_switch),
    elapsedMs: num(pick(raw, 'elapsedMs', 'elapsed_ms')),
  }
}

export async function listCandidates(query: Record<string, unknown> = {}): Promise<Candidate[]> {
  const data = await request('/api/strategy/candidates', { query })
  return asList(data, 'items', 'candidates').map(normalizeCandidate)
}

export function addBlacklist(body: BlacklistBody): Promise<unknown> {
  return request('/api/strategy/blacklist', { method: 'POST', json: body })
}

export function removeBlacklist(symbol: string): Promise<unknown> {
  return request(`/api/strategy/blacklist/${encodeURIComponent(symbol)}`, { method: 'DELETE' })
}

function normalizeCandidate(raw: RawRecord): Candidate {
  return {
    id: pick<string>(raw, 'id') || '',
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    strategy: pick<string>(raw, 'strategy') || '',
    symbol: pick<string>(raw, 'symbol') || '',
    name: pick<string>(raw, 'name') || '',
    pool: pick<string>(raw, 'pool') || '',
    stage: pick<string>(raw, 'stage') || '',
    missReason: pick<string>(raw, 'missReason', 'miss_reason') || '',
    ruleScore: num(pick(raw, 'ruleScore', 'rule_score')),
    aiScore: num(pick(raw, 'aiScore', 'ai_score')),
    finalScore: num(pick(raw, 'finalScore', 'final_score')),
    signalId: pick<string>(raw, 'signalId', 'signal_id') || '',
    refPrice: num(pick(raw, 'refPrice', 'ref_price')),
    retT1: num(pick(raw, 'retT1', 'ret_t1')),
    retT3: num(pick(raw, 'retT3', 'ret_t3')),
    retT5: num(pick(raw, 'retT5', 'ret_t5')),
    hasT1: !!(raw.hasT1 || raw.has_t1),
    hasT3: !!(raw.hasT3 || raw.has_t3),
    hasT5: !!(raw.hasT5 || raw.has_t5),
  }
}

/* ---------- S7 P1：绩效与评分-收益（只读） ---------- */

export interface StrategyAttribution {
  attribution: string
  count: number
}

export interface StrategyPerformance {
  sample: number
  winRate: number
  plRatio: number
  maxDrawdown: number
  avgReturn: number
  attributions: StrategyAttribution[]
}

export interface ScorePnlPoint {
  signalId: string
  symbol: string
  score: number
  actualReturn: number
  tradeDate: string
  strategy: string
}

export interface ScoreVsPnl {
  points: ScorePnlPoint[]
  pearson: number
  sample: number
}

export async function getStrategyPerformance(last = 20): Promise<StrategyPerformance> {
  const data = await request('/api/strategy/performance', { query: { last } })
  const raw = (data ?? {}) as RawRecord
  return {
    sample: num(pick(raw, 'sample')) ?? 0,
    winRate: num(pick(raw, 'winRate', 'win_rate')) ?? 0,
    plRatio: num(pick(raw, 'plRatio', 'pl_ratio')) ?? 0,
    maxDrawdown: num(pick(raw, 'maxDrawdown', 'max_drawdown')) ?? 0,
    avgReturn: num(pick(raw, 'avgReturn', 'avg_return')) ?? 0,
    attributions: asList<RawRecord>(raw, 'attributions').map((item) => ({
      attribution: pick<string>(item, 'attribution') || '未归因',
      count: num(pick(item, 'count')) ?? 0,
    })),
  }
}

export async function getScoreVsPnl(last = 50): Promise<ScoreVsPnl> {
  const data = await request('/api/strategy/score-vs-pnl', { query: { last } })
  const raw = (data ?? {}) as RawRecord
  return {
    points: asList<RawRecord>(raw, 'points').map((item) => ({
      signalId: pick<string>(item, 'signalId', 'signal_id') || '',
      symbol: pick<string>(item, 'symbol') || '',
      score: num(pick(item, 'score')) ?? 0,
      actualReturn: num(pick(item, 'actualReturn', 'actual_return')) ?? 0,
      tradeDate: pick<string>(item, 'tradeDate', 'trade_date') || '',
      strategy: pick<string>(item, 'strategy') || '',
    })),
    pearson: num(pick(raw, 'pearson')) ?? 0,
    sample: num(pick(raw, 'sample')) ?? 0,
  }
}
