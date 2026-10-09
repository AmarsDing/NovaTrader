import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { Analysis, AskReply, Briefing, DimensionScore, Explanation, TierStats } from './types/brain'

export type { Analysis, AskReply, Briefing, DimensionScore, Explanation, TierStats } from './types/brain'

export function getBriefing(date: string): Promise<unknown> {
  return request(`/api/brain/briefing/${encodeURIComponent(date)}`)
}

export function runBriefing(body: Record<string, unknown> = {}): Promise<unknown> {
  return request('/api/brain/briefing', { method: 'POST', json: body })
}

export function askBrain(body: Record<string, unknown>): Promise<unknown> {
  return request('/api/brain/ask', { method: 'POST', json: body })
}

export function explainBrain(body: Record<string, unknown>): Promise<unknown> {
  return request('/api/brain/explain', { method: 'POST', json: body })
}

export function analyzeBrain(body: Record<string, unknown>): Promise<unknown> {
  return request('/api/brain/analyze', { method: 'POST', json: body })
}

export async function brainStats(): Promise<TierStats[]> {
  const data = await request('/api/brain/stats')
  return asList(data, 'tiers', 'items', 'stats').map(normalizeTier)
}

function normalizeTier(raw: RawRecord): TierStats {
  return {
    tier: pick<string>(raw, 'tier') || '',
    model: pick<string>(raw, 'model') || '',
    configured: !!(raw.configured),
    calls: num(pick(raw, 'calls')),
    failures: num(pick(raw, 'failures')),
    p50Ms: num(pick(raw, 'p50Ms', 'p50_ms')),
    p95Ms: num(pick(raw, 'p95Ms', 'p95_ms')),
    inFlight: num(pick(raw, 'inFlight', 'in_flight')),
    queued: num(pick(raw, 'queued')),
    circuitOpen: !!(raw.circuitOpen || raw.circuit_open),
    kvCacheUsage: num(pick(raw, 'kvCacheUsage', 'kv_cache_usage')),
    running: num(pick(raw, 'running')),
    waiting: num(pick(raw, 'waiting')),
  }
}
