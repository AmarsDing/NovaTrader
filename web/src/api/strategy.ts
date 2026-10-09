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
    degraded: !!(raw.degraded),
    signalIds: asList<string>(raw, 'signalIds', 'signal_ids'),
    sellSignalIds: asList<string>(raw, 'sellSignalIds', 'sell_signal_ids'),
    dropped: (raw.dropped && typeof raw.dropped === 'object' ? raw.dropped : {}) as Record<string, number>,
    misses: (raw.misses && typeof raw.misses === 'object' ? raw.misses : {}) as Record<string, string>,
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
