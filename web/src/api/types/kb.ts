/** M04 知识库（RAG）。经 admin 代理 /api/kb/*。 */

export interface KbDocument {
  id: string
  title: string
  source: string
  kind: string
  createdAt: string
  [key: string]: unknown
}

export interface KbSearchHit {
  id: string
  documentId: string
  title: string
  snippet: string
  score: number | null
  source: string
  [key: string]: unknown
}

export interface KbCase {
  id: string
  symbol: string
  tradeDate: string
  summary: string
  outcome: string
  [key: string]: unknown
}
