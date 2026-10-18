/**
 * M04 知识库接口。走 admin 聚合 /api/kb/*，对齐 api/intel/v1/knowledge.proto：
 * - Search / SimilarCases 是 POST（body 检索），旧件的 GET search、GET cases/{id}/similar 在后端并不存在；
 * - 案例没有单独的列表接口：situation）留空调 SimilarCases 即「按结束时间倒序」。
 * 响应字段 protojson 可能 snake/camel 混出，读取一律双键容错。
 */
import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type {
  KbCase,
  KbCaseHit,
  KbDocument,
  KbDocumentDetail,
  KbIngestParams,
  KbIngestReply,
  KbListDocsParams,
  KbSearchHit,
  KbSearchParams,
  KbSearchResult,
  KbSimilarCasesResult,
  KbSimilarParams,
} from './types/kb'

export type {
  KbCase,
  KbCaseHit,
  KbDocument,
  KbDocumentDetail,
  KbIngestParams,
  KbIngestReply,
  KbListDocsParams,
  KbSearchHit,
  KbSearchParams,
  KbSearchResult,
  KbSimilarCasesResult,
  KbSimilarParams,
} from './types/kb'

/** int64 在 proto JSON 里是字符串；容忍后端吐数字。 */
function str(raw: RawRecord, ...keys: string[]): string {
  const v = pick<string | number>(raw, ...keys)
  return v == null ? '' : String(v)
}

function strs(raw: RawRecord, ...keys: string[]): string[] {
  for (const key of keys) {
    const v = raw[key]
    if (Array.isArray(v)) return v.map((x) => String(x))
  }
  return []
}

function normalizeDoc(raw: RawRecord): KbDocument {
  return {
    id: str(raw, 'id'),
    category: str(raw, 'category'),
    title: str(raw, 'title'),
    source: str(raw, 'source'),
    sourceType: pick<string>(raw, 'sourceType', 'source_type') || '',
    author: str(raw, 'author'),
    format: str(raw, 'format'),
    fileName: pick<string>(raw, 'fileName', 'file_name') || '',
    sizeBytes: num(pick(raw, 'sizeBytes', 'size_bytes')),
    contentHash: pick<string>(raw, 'contentHash', 'content_hash') || '',
    status: str(raw, 'status'),
    version: num(pick(raw, 'version')),
    stockCodes: strs(raw, 'stockCodes', 'stock_codes'),
    tags: strs(raw, 'tags'),
    expiresAt: pick<string>(raw, 'expiresAt', 'expires_at') || '',
    chunkCount: num(pick(raw, 'chunkCount', 'chunk_count')),
    embedStatus: pick<string>(raw, 'embedStatus', 'embed_status') || '',
    createdAt: pick<string>(raw, 'createdAt', 'created_at') || '',
    updatedAt: pick<string>(raw, 'updatedAt', 'updated_at') || '',
  }
}

function normalizeChunk(raw: RawRecord) {
  return {
    id: str(raw, 'id'),
    seq: num(pick(raw, 'seq')),
    heading: str(raw, 'heading'),
    content: str(raw, 'content'),
    tokenCount: num(pick(raw, 'tokenCount', 'token_count')),
    embedded: !!raw.embedded,
  }
}

/* ---------- 检索 ---------- */

export async function searchKb(params: KbSearchParams): Promise<KbSearchResult> {
  const data = await request('/api/kb/search', {
    method: 'POST',
    json: {
      query: params.query,
      top_k: params.topK ?? 8,
      categories: params.categories?.length ? params.categories : undefined,
      stock_code: params.stockCode || undefined,
    },
  })
  const raw = (data ?? {}) as RawRecord
  const hits = asList<RawRecord>(raw, 'hits').map(
    (h): KbSearchHit => ({
      docId: str(h, 'docId', 'doc_id'),
      chunkId: str(h, 'chunkId', 'chunk_id'),
      title: str(h, 'title'),
      heading: str(h, 'heading'),
      content: str(h, 'content'),
      source: str(h, 'source'),
      sourceType: pick<string>(h, 'sourceType', 'source_type') || '',
      category: str(h, 'category'),
      score: num(pick(h, 'score')),
      keywordRank: num(pick(h, 'keywordRank', 'keyword_rank')),
      vectorRank: num(pick(h, 'vectorRank', 'vector_rank')),
    }),
  )
  return { hits, degraded: !!raw.degraded }
}

/* ---------- 案例 ---------- */

export async function similarCases(params: KbSimilarParams): Promise<KbSimilarCasesResult> {
  const data = await request('/api/kb/cases/similar', {
    method: 'POST',
    json: {
      situation: params.situation?.trim() || undefined,
      pattern: params.pattern?.trim() || undefined,
      emotion_phase: params.emotionPhase || undefined,
      stock_code: params.stockCode?.trim() || undefined,
      strategy: params.strategy?.trim() || undefined,
      book: params.book || undefined,
      as_of: params.asOf || undefined,
      top_k: params.topK ?? 10,
    },
  })
  const raw = (data ?? {}) as RawRecord
  const hits = asList<RawRecord>(raw, 'cases').map(normalizeCaseHit)
  return { hits, degraded: !!raw.degraded }
}

function normalizeCase(raw: RawRecord): KbCase {
  return {
    id: str(raw, 'id'),
    book: str(raw, 'book'),
    signalId: pick(raw, 'signalId', 'signal_id') != null ? String(pick(raw, 'signalId', 'signal_id')) : '',
    stockCode: pick<string>(raw, 'stockCode', 'stock_code') || '',
    stockName: pick<string>(raw, 'stockName', 'stock_name') || '',
    strategy: str(raw, 'strategy'),
    pattern: str(raw, 'pattern'),
    emotionPhase: pick<string>(raw, 'emotionPhase', 'emotion_phase') || '',
    signalType: pick<string>(raw, 'signalType', 'signal_type') || '',
    finalStatus: pick<string>(raw, 'finalStatus', 'final_status') || '',
    signalTime: pick<string>(raw, 'signalTime', 'signal_time') || '',
    closedAt: pick<string>(raw, 'closedAt', 'closed_at') || '',
    entryPrice: num(pick(raw, 'entryPrice', 'entry_price')),
    exitPrice: num(pick(raw, 'exitPrice', 'exit_price')),
    pnlPct: num(pick(raw, 'pnlPct', 'pnl_pct')),
    holdingDays: num(pick(raw, 'holdingDays', 'holding_days')),
    outcome: str(raw, 'outcome'),
    attribution: str(raw, 'attribution'),
    docId: str(raw, 'docId', 'doc_id'),
  }
}

function normalizeCaseHit(raw: RawRecord): KbCaseHit {
  return {
    kbCase: normalizeCase((pick<RawRecord>(raw, 'case') ?? {}) as RawRecord),
    chunkId: str(raw, 'chunkId', 'chunk_id'),
    score: num(pick(raw, 'score')),
  }
}

/* ---------- 文档管理 ---------- */

export interface KbDocPage {
  documents: KbDocument[]
  total: number
}

export async function listDocuments(params: KbListDocsParams = {}): Promise<KbDocPage> {
  const data = await request('/api/kb/documents', {
    query: {
      category: params.category || undefined,
      status: params.status || undefined,
      offset: params.offset ?? 0,
      limit: params.limit ?? 50,
    },
  })
  const raw = (data ?? {}) as RawRecord
  return {
    documents: asList<RawRecord>(raw, 'documents').map(normalizeDoc),
    total: num(pick(raw, 'total')) ?? 0,
  }
}

export async function getDocument(id: string): Promise<KbDocumentDetail> {
  const data = await request(`/api/kb/documents/${encodeURIComponent(id)}`)
  const raw = (data ?? {}) as RawRecord
  const doc = pick<RawRecord>(raw, 'document')
  return {
    document: doc ? normalizeDoc(doc) : null,
    chunks: asList<RawRecord>(raw, 'chunks').map(normalizeChunk),
  }
}

export async function deleteDocument(id: string): Promise<void> {
  await request(`/api/kb/documents/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/* ---------- 上传（含分块+向量，最长给到 2 分钟） ---------- */

export async function ingestDocument(params: KbIngestParams): Promise<KbIngestReply> {
  const data = await request('/api/kb/documents', {
    method: 'POST',
    timeoutMs: 120_000,
    json: {
      category: params.category,
      title: params.title?.trim() || undefined,
      source: params.source.trim(),
      source_type: params.sourceType,
      author: params.author?.trim() || undefined,
      file_name: params.fileName || undefined,
      content: params.contentBase64,
      stock_codes: params.stockCodes?.length ? params.stockCodes : undefined,
      tags: params.tags?.length ? params.tags : undefined,
      expires_at: params.expiresAt || undefined,
    },
  })
  const raw = (data ?? {}) as RawRecord
  const doc = pick<RawRecord>(raw, 'document')
  return {
    document: doc ? normalizeDoc(doc) : null,
    duplicate: !!raw.duplicate,
    embedded: !!raw.embedded,
  }
}
