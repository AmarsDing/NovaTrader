import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { Analysis, AskReply, Briefing, BriefingItem, DimensionScore, Explanation, TierStats } from './types/brain'

export type { Analysis, AskReply, Briefing, BriefingItem, DimensionScore, Explanation, TierStats } from './types/brain'

/* ---------- 晨报（M05 §晨报） ---------- */

function normalizeBriefingItem(raw: RawRecord): BriefingItem {
  return {
    kind: pick<string>(raw, 'kind', 'type', 'category') || '',
    title: pick<string>(raw, 'title', 'subject') || '',
    body: pick<string>(raw, 'body', 'text', 'content') || '',
    symbol: pick<string>(raw, 'symbol', 'code') || '',
  }
}

function normalizeBriefing(raw: RawRecord): Briefing {
  return {
    date: pick<string>(raw, 'date', 'tradeDate', 'trade_date') || '',
    summary: pick<string>(raw, 'summary', 'digest') || '',
    items: asList(raw, 'items', 'sections').map(normalizeBriefingItem),
    generatedAt: pick<string>(raw, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
    stale: !!raw.stale,
  }
}

/** 读晨报；date 为空串 = 今天（后端口径）。 */
export async function getBriefing(date: string): Promise<Briefing> {
  const data = await request(`/api/brain/briefing/${encodeURIComponent(date || 'latest')}`)
  return normalizeBriefing((data && typeof data === 'object' ? data : {}) as RawRecord)
}

/** 触发晨报生成（owner 写操作）。 */
export async function runBriefing(body: Record<string, unknown> = {}): Promise<Briefing> {
  const data = await request('/api/brain/briefing', { method: 'POST', json: body })
  return normalizeBriefing((data && typeof data === 'object' ? data : {}) as RawRecord)
}

/* ---------- 分析 / 追问 ---------- */

function normalizeDims(raw: unknown): DimensionScore[] {
  if (!Array.isArray(raw)) {
    // 部分后端把四维写成对象 {技术面: 88, 消息面: {...}}；这里兼容两种。
    if (raw && typeof raw === 'object') {
      return Object.entries(raw as RawRecord).map(([name, value]) => {
        const score = num(value)
        const comment =
          score === null && value && typeof value === 'object'
            ? pick<string>(value, 'comment', 'reason', 'text') || ''
            : ''
        return { name, score, comment }
      })
    }
    return []
  }
  return (raw as RawRecord[]).map((row) => ({
    name: pick<string>(row, 'name', 'dimension', 'dim') || '',
    score: num(pick(row, 'score')),
    comment: pick<string>(row, 'comment', 'reason', 'text') || '',
  }))
}

function normalizeAnalysis(raw: RawRecord): Analysis {
  return {
    symbol: pick<string>(raw, 'symbol', 'code') || '',
    name: pick<string>(raw, 'name', 'stockName', 'stock_name') || '',
    score: num(pick(raw, 'score', 'totalScore', 'total_score')),
    verdict: pick<string>(raw, 'verdict', 'advice', 'action') || '',
    dimensions: normalizeDims(raw.dimensions ?? raw.dims),
    reasons: Array.isArray(raw.reasons) ? raw.reasons.map(String) : [],
    risks: Array.isArray(raw.risks) ? raw.risks.map(String) : [],
    generatedAt: pick<string>(raw, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
    model: pick<string>(raw, 'model', 'modelName', 'model_name') || '',
  }
}

export async function analyzeBrain(body: Record<string, unknown>): Promise<Analysis> {
  const data = await request('/api/brain/analyze', { method: 'POST', json: body })
  return normalizeAnalysis((data && typeof data === 'object' ? data : {}) as RawRecord)
}

export async function analyzeBatch(symbols: string[], body: Record<string, unknown> = {}): Promise<Analysis[]> {
  const data = await request('/api/brain/analyze/batch', { method: 'POST', json: { ...body, symbols } })
  return asList(data, 'items', 'results', 'analyses').map(normalizeAnalysis)
}

export async function askBrain(body: Record<string, unknown>): Promise<AskReply> {
  const data = await request('/api/brain/ask', { method: 'POST', json: body })
  const raw = (data && typeof data === 'object' ? data : {}) as RawRecord
  return {
    answer: pick<string>(raw, 'answer', 'text', 'reply') || '',
    citations: Array.isArray(raw.citations) ? raw.citations.map(String) : [],
    generatedAt: pick<string>(raw, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
    model: pick<string>(raw, 'model', 'modelName', 'model_name') || '',
  }
}

export async function explainBrain(body: Record<string, unknown>): Promise<Explanation> {
  const data = await request('/api/brain/explain', { method: 'POST', json: body })
  const raw = (data && typeof data === 'object' ? data : {}) as RawRecord
  return {
    symbol: pick<string>(raw, 'symbol', 'code') || '',
    text: pick<string>(raw, 'text', 'explanation', 'answer') || '',
    reasons: Array.isArray(raw.reasons) ? raw.reasons.map(String) : [],
    generatedAt: pick<string>(raw, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
    model: pick<string>(raw, 'model', 'modelName', 'model_name') || '',
  }
}

/* ---------- 分层运行统计（brain-monitor） ---------- */

export async function brainStats(): Promise<TierStats[]> {
  const data = await request('/api/brain/stats')
  return asList(data, 'tiers', 'items', 'stats').map(normalizeTier)
}

function normalizeTier(raw: RawRecord): TierStats {
  return {
    tier: pick<string>(raw, 'tier') || '',
    model: pick<string>(raw, 'model') || '',
    configured: !!raw.configured,
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

/* ---------- 监控驾驶舱 P1：令牌用量 / 推理摘录 / 延迟直方图 ---------- */

export interface BrainTierUsage {
  tier: string
  model: string
  promptTokens: number
  completionTokens: number
  totalTokens: number
  calls: number
  failures: number
}

export interface BrainUsage {
  window: string
  since: string
  tiers: BrainTierUsage[]
  total: BrainTierUsage
}

function normalizeTierUsage(raw: RawRecord): BrainTierUsage {
  return {
    tier: pick<string>(raw, 'tier') || '',
    model: pick<string>(raw, 'model') || '',
    promptTokens: num(pick(raw, 'promptTokens', 'prompt_tokens')) ?? 0,
    completionTokens: num(pick(raw, 'completionTokens', 'completion_tokens')) ?? 0,
    totalTokens: num(pick(raw, 'totalTokens', 'total_tokens')) ?? 0,
    calls: num(pick(raw, 'calls')) ?? 0,
    failures: num(pick(raw, 'failures')) ?? 0,
  }
}

export async function brainUsage(window: 'today' | '1h' | '24h' = 'today'): Promise<BrainUsage> {
  const data = (await request('/api/brain/usage', { query: { window } })) as RawRecord
  return {
    window: pick<string>(data, 'window') || window,
    since: pick<string>(data, 'since') || '',
    tiers: asList(data, 'tiers').map(normalizeTierUsage),
    total: normalizeTierUsage((data.total as RawRecord) ?? {}),
  }
}

export interface BrainReasoning {
  found: boolean
  decisionId: number
  traceId: string
  excerpt: string
  latencyMs: number
  tier: string
  model: string
  task: string
  createdAt: string
}

export async function brainReasoning(signalId: number): Promise<BrainReasoning> {
  const data = (await request('/api/brain/reasoning', { query: { signal_id: signalId } })) as RawRecord
  return {
    found: !!data.found,
    decisionId: num(pick(data, 'decisionId', 'decision_id')) ?? 0,
    traceId: pick<string>(data, 'traceId', 'trace_id') || '',
    excerpt: pick<string>(data, 'excerpt') || '',
    latencyMs: num(pick(data, 'latencyMs', 'latency_ms')) ?? 0,
    tier: pick<string>(data, 'tier') || '',
    model: pick<string>(data, 'model') || '',
    task: pick<string>(data, 'task') || '',
    createdAt: pick<string>(data, 'createdAt', 'created_at') || '',
  }
}

export interface BrainLatencyBucket {
  leMs: number
  count: number
}

export interface BrainLatencyHistogram {
  windowMinutes: number
  total: number
  p50Ms: number
  p95Ms: number
  buckets: BrainLatencyBucket[]
}

export async function brainLatencyHistogram(windowMinutes = 60): Promise<BrainLatencyHistogram> {
  const data = (await request('/api/brain/latency-histogram', { query: { window_minutes: windowMinutes } })) as RawRecord
  return {
    windowMinutes: num(pick(data, 'windowMinutes', 'window_minutes')) ?? windowMinutes,
    total: num(pick(data, 'total')) ?? 0,
    p50Ms: num(pick(data, 'p50Ms', 'p50_ms')) ?? 0,
    p95Ms: num(pick(data, 'p95Ms', 'p95_ms')) ?? 0,
    buckets: asList(data, 'buckets').map((b) => ({
      leMs: num(pick(b, 'leMs', 'le_ms')) ?? 0,
      count: num(pick(b, 'count')) ?? 0,
    })),
  }
}
