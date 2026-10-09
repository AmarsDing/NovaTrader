import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { KbCase, KbDocument, KbSearchHit } from './types/kb'

export type { KbCase, KbDocument, KbSearchHit } from './types/kb'

export async function searchKb(query: string, limit = 20): Promise<KbSearchHit[]> {
  const data = await request('/api/kb/search', { query: { q: query, limit } })
  return asList(data, 'hits', 'items', 'results').map((raw) => ({
    id: pick<string>(raw, 'id') || '',
    documentId: pick<string>(raw, 'documentId', 'document_id') || '',
    title: pick<string>(raw, 'title') || '',
    snippet: pick<string>(raw, 'snippet', 'content', 'text') || '',
    score: num(pick(raw, 'score')),
    source: pick<string>(raw, 'source') || '',
  }))
}

export async function listDocuments(query: Record<string, unknown> = {}): Promise<KbDocument[]> {
  const data = await request('/api/kb/documents', { query })
  return asList(data, 'items', 'documents').map((raw) => ({
    id: pick<string>(raw, 'id') || '',
    title: pick<string>(raw, 'title') || '',
    source: pick<string>(raw, 'source') || '',
    kind: pick<string>(raw, 'kind', 'type') || '',
    createdAt: pick<string>(raw, 'createdAt', 'created_at') || '',
  }))
}

export async function similarCases(id: string): Promise<KbCase[]> {
  const data = await request(`/api/kb/cases/${encodeURIComponent(id)}/similar`)
  return asList(data, 'items', 'cases').map(normalizeCase)
}

export async function listCases(query: Record<string, unknown> = {}): Promise<KbCase[]> {
  const data = await request('/api/kb/cases', { query })
  return asList(data, 'items', 'cases').map(normalizeCase)
}

function normalizeCase(raw: RawRecord): KbCase {
  return {
    id: pick<string>(raw, 'id') || '',
    symbol: pick<string>(raw, 'symbol') || '',
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    summary: pick<string>(raw, 'summary') || '',
    outcome: pick<string>(raw, 'outcome', 'result') || '',
  }
}
