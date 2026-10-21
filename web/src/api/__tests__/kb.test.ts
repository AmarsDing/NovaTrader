import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { request } from '@/api/http'
import { deleteDocument, getDocument, ingestDocument, listDocuments, searchKb, similarCases } from '@/api/kb'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
const req = request as unknown as Mock

beforeEach(() => {
  vi.clearAllMocks()
  // api-fill 批次：normalizeCase 命中名称字典会在函数体内 useSecurityStore，需激活 pinia
  setActivePinia(createPinia())
})

describe('api/kb：与 api/intel/v1/knowledge.proto 的合同', () => {
  it('searchKb 走 POST /api/kb/search，body 用 proto json_name，degraded 原样透出', async () => {
    req.mockResolvedValueOnce({
      hits: [
        {
          doc_id: 12,
          chunk_id: '7',
          title: '断板应对',
          heading: '二板断板',
          content: '断板次日不要急着接。',
          source: '短线笔记.md',
          source_type: 'file',
          category: 'case',
          score: 0.0112,
          keyword_rank: 2,
          vector_rank: 0,
        },
      ],
      degraded: true,
    })
    const res = await searchKb({ query: '断板', topK: 30, categories: ['case'], stockCode: '600519' })
    expect(req).toHaveBeenCalledWith('/api/kb/search', {
      method: 'POST',
      json: { query: '断板', top_k: 30, categories: ['case'], stock_code: '600519' },
    })
    expect(res.degraded).toBe(true)
    expect(res.hits[0]).toMatchObject({
      docId: '12',
      chunkId: '7',
      title: '断板应对',
      keywordRank: 2,
      vectorRank: 0,
      category: 'case',
    })
  })

  it('searchKb 兼容 camelCase 响应，名次/得分不可解析时保持 null', async () => {
    req.mockResolvedValueOnce({
      hits: [{ docId: '5', chunkId: 9, title: 'x', score: 'nope' }],
    })
    const res = await searchKb({ query: 'x' })
    expect(res.hits[0].docId).toBe('5')
    expect(res.hits[0].chunkId).toBe('9')
    expect(res.hits[0].score).toBeNull()
    expect(res.hits[0].keywordRank).toBeNull()
    expect(res.degraded).toBe(false)
  })

  it('similarCases 空情形也发 POST（后端按结束时间倒序即「案例列表」），字段含可选价量', async () => {
    req.mockResolvedValueOnce({
      cases: [
        {
          case: {
            id: '3',
            book: 'paper',
            signal_id: '42',
            stock_code: '600519',
            stock_name: '贵州茅台',
            final_status: 'closed',
            pnl_pct: 3.25,
            outcome: 'win',
            holding_days: 2,
          },
          chunk_id: '9',
          score: 1.49,
        },
      ],
      degraded: false,
    })
    const res = await similarCases({})
    const [, opts] = req.mock.calls[0]
    expect(req.mock.calls[0][0]).toBe('/api/kb/cases/similar')
    expect(opts.method).toBe('POST')
    expect(opts.json.top_k).toBe(10)
    expect(opts.json.situation).toBeUndefined()
    expect(res.hits[0].kbCase.pnlPct).toBe(3.25)
    expect(res.hits[0].kbCase.signalId).toBe('42')
    expect(res.hits[0].kbCase.entryPrice).toBeNull()
    expect(res.hits[0].score).toBe(1.49)
  })

  it('listDocuments 透传过滤与分页，返回 total', async () => {
    req.mockResolvedValueOnce({
      documents: [
        { id: '8', category: 'rule', status: 'active', embed_status: 'pending', chunk_count: 3, stock_codes: ['600519'] },
      ],
      total: 21,
    })
    const res = await listDocuments({ category: 'rule', status: 'active', offset: 20, limit: 20 })
    expect(req).toHaveBeenCalledWith('/api/kb/documents', {
      query: { category: 'rule', status: 'active', offset: 20, limit: 20 },
    })
    expect(res.total).toBe(21)
    expect(res.documents[0]).toMatchObject({ id: '8', embedStatus: 'pending', chunkCount: 3, stockCodes: ['600519'] })
  })

  it('getDocument 拉回文档与全部分块', async () => {
    req.mockResolvedValueOnce({
      document: { id: '8', title: 't', status: 'active' },
      chunks: [{ id: '1', seq: 0, heading: 'h', content: 'c', token_count: 128, embedded: true }],
    })
    const res = await getDocument('8')
    expect(req).toHaveBeenCalledWith('/api/kb/documents/8')
    expect(res.document?.title).toBe('t')
    expect(res.chunks[0]).toMatchObject({ tokenCount: 128, embedded: true })
  })

  it('ingestDocument base64 原样上送，放宽超时到 2 分钟', async () => {
    req.mockResolvedValueOnce({ document: { id: '9', version: 2 }, duplicate: true, embedded: false })
    const res = await ingestDocument({
      category: 'rule',
      sourceType: 'manual',
      author: '张三',
      source: '复盘笔记',
      fileName: 'note.md',
      contentBase64: 'aGVsbG8=',
      stockCodes: ['600519'],
    })
    const [, opts] = req.mock.calls[0]
    expect(req.mock.calls[0][0]).toBe('/api/kb/documents')
    expect(opts.method).toBe('POST')
    expect(opts.timeoutMs).toBe(120000)
    expect(opts.json.content).toBe('aGVsbG8=')
    expect(opts.json.source_type).toBe('manual')
    expect(res.duplicate).toBe(true)
    expect(res.embedded).toBe(false)
  })

  it('deleteDocument 走 DELETE /api/kb/documents/{id}', async () => {
    req.mockResolvedValueOnce({})
    await deleteDocument('8')
    expect(req).toHaveBeenCalledWith('/api/kb/documents/8', { method: 'DELETE' })
  })
})
