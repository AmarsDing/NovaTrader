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
