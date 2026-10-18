/**
 * M04 知识库（RAG）。经 admin 聚合 /api/kb/* → intel /v1/kb。
 * 字段对齐 backend/api/intel/v1/knowledge.proto；int64 在 proto JSON 里是字符串，id 一律按 string 归一。
 */

export interface KbDocument {
  id: string
  /** case / rule / compliance / review */
  category: string
  title: string
  source: string
  /** file / url / signal / manual */
  sourceType: string
  author: string
  /** md / txt / docx / xlsx / pdf，由 file_name 后缀判定 */
  format: string
  fileName: string
  sizeBytes: number | null
  contentHash: string
  /** active / superseded / expired / deleted */
  status: string
  version: number | null
  stockCodes: string[]
  tags: string[]
  /** RFC3339，空 = 不过期 */
  expiresAt: string
  chunkCount: number | null
  /** pending / done */
  embedStatus: string
  createdAt: string
  updatedAt: string
}

export interface KbChunk {
  id: string
  seq: number | null
  heading: string
  content: string
  tokenCount: number | null
  embedded: boolean
}

export interface KbDocumentDetail {
  document: KbDocument | null
  chunks: KbChunk[]
}

export interface KbSearchHit {
  docId: string
  chunkId: string
  title: string
  heading: string
  content: string
  source: string
  sourceType: string
  category: string
  /** RRF 融合分 */
  score: number | null
  /** 两路名次，null / 0 = 该路未命中 */
  keywordRank: number | null
  vectorRank: number | null
}

export interface KbSearchResult {
  hits: KbSearchHit[]
  /** true = 向量路不可用，只走了关键词 */
  degraded: boolean
}

export interface KbCase {
  id: string
  book: string
  signalId: string
  stockCode: string
  stockName: string
  strategy: string
  pattern: string
  emotionPhase: string
  signalType: string
  /** closed / expired / cancelled */
  finalStatus: string
  signalTime: string
  closedAt: string
  entryPrice: number | null
  exitPrice: number | null
  /** 百分点口径：3.25 = +3.25% */
  pnlPct: number | null
  holdingDays: number | null
  /** win / loss / flat / unfilled / cancelled */
  outcome: string
  /** indicator_failed / news_misread / unfilled / emotion_mismatch */
  attribution: string
  docId: string
}

export interface KbCaseHit {
  kbCase: KbCase
  chunkId: string
  score: number | null
}

export interface KbSimilarCasesResult {
  hits: KbCaseHit[]
  degraded: boolean
}

/* ---------- 请求参数（发送侧用 proto json_name，snake_case） ---------- */

export interface KbSearchParams {
  query: string
  /** 默认 8，最大 50 */
  topK?: number
  categories?: string[]
  stockCode?: string
}

export interface KbSimilarParams {
  /** 为空时按结束时间倒序列出案例 */
  situation?: string
  pattern?: string
  emotionPhase?: string
  stockCode?: string
  strategy?: string
  book?: string
  /** RFC3339，默认当前时间；只返回 as_of 前已结束的案例 */
  asOf?: string
  topK?: number
}

export interface KbListDocsParams {
  category?: string
  status?: string
  offset?: number
  /** 默认 50，最大 200 */
  limit?: number
}

export interface KbIngestParams {
  category: string
  /** 空 = 取文件名 */
  title?: string
  source: string
  sourceType: 'file' | 'url' | 'manual'
  /** manual 必填 */
  author?: string
  /** 后缀判格式：md、txt、docx、xlsx、pdf */
  fileName?: string
  /** 原始字节 base64 */
  contentBase64: string
  stockCodes?: string[]
  tags?: string[]
  /** RFC3339，可空 */
  expiresAt?: string
}

export interface KbIngestReply {
  document: KbDocument | null
  /** 同内容重复上传：返回的是原文档 */
  duplicate: boolean
  /** false = 向量未写入，等补嵌入任务（嵌入服务未接时恒 false） */
  embedded: boolean
}
