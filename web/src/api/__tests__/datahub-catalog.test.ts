import { describe, expect, it, vi } from 'vitest'
import { getSourceCatalog } from '@/api/datahub'
import { request } from '@/api/http'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
const requestMock = vi.mocked(request)

/**
 * 源目录归一化（GET /api/datahub/source-catalog → SourceCatalog）纪律钉死：
 * 枚举非法/键缺失一律显式缺省（''/null/[]），前端不猜默认值冒充（没取到≠0）；
 * 费用档枚举到不了界面显「—」，而不是悄悄显示成 free。
 */
describe('getSourceCatalog 归一化：缺失显式缺省，不冒默认值', () => {
  it('请求路径钉死', async () => {
    requestMock.mockResolvedValue({ version: 1, sources: [], domains: [] })
    await getSourceCatalog()
    expect(requestMock).toHaveBeenCalledWith('/api/datahub/source-catalog')
  })

  it('正常载荷贯通：snake 主键 + camel 兜底键都认', async () => {
    requestMock.mockResolvedValue({
      version: 3,
      generated_at: '2026-10-20T08:00:00+08:00',
      services_note: 'datahub 单飞',
      sources: [
        {
          name: 'eastmoney',
          registered: true,
          official: true,
          fee_tier: 'free',
          fee_note: '公开接口',
          requires: 'none',
          via: 'go',
          domains: ['snapshot', 'flash'],
          notes: '涨停池主力',
          risk_note: '无',
        },
      ],
      domains: [{ name: 'snapshot', default_chain: ['eastmoney'], candidates: ['eastmoney', 'sina'] }],
    })
    const cat = await getSourceCatalog()
    expect(cat.version).toBe(3)
    expect(cat.generatedAt).toBe('2026-10-20T08:00:00+08:00')
    expect(cat.servicesNote).toBe('datahub 单飞')
    expect(cat.sources[0]).toEqual({
      id: 'eastmoney',
      registered: true,
      official: true,
      feeTier: 'free',
      feeNote: '公开接口',
      requires: 'none',
      via: 'go',
      domains: ['snapshot', 'flash'],
      notes: '涨停池主力',
      riskNote: '无',
    })
    expect(cat.domains[0]).toEqual({ name: 'snapshot', defaultChain: ['eastmoney'], candidates: ['eastmoney', 'sina'] })

    // camelCase 同语义（旧网关直返 camel 时不哑）
    requestMock.mockResolvedValue({
      version: 3,
      generatedAt: '2026-10-20T09:00:00+08:00',
      servicesNote: 'camel',
      sources: [{ id: 'sina', feeTier: 'free', requires: 'none', domains: ['snapshot'] }],
      domains: [{ name: 'snapshot', defaultChain: ['sina'], candidates: ['sina'] }],
    })
    const cat2 = await getSourceCatalog()
    expect(cat2.generatedAt).toBe('2026-10-20T09:00:00+08:00')
    expect(cat2.sources[0].id).toBe('sina')
    expect(cat2.domains[0].defaultChain).toEqual(['sina'])
  })

  it('fee_tier 非法值 → ""（不冒充 free）；三合法档原样贯通', async () => {
    requestMock.mockResolvedValue({
      version: 1,
      sources: [
        { name: 'a', fee_tier: 'enterprise' },
        { name: 'b', fee_tier: 'freemium' },
        { name: 'c', fee_tier: 'paid' },
        { name: 'd', fee_tier: '' },
      ],
    })
    const cat = await getSourceCatalog()
    expect(cat.sources.map((s) => s.feeTier)).toEqual(['', 'freemium', 'paid', ''])
  })

  it('requires 非法值 → ""；四合法档原样贯通', async () => {
    requestMock.mockResolvedValue({
      version: 1,
      sources: [
        { name: 'a', requires: 'oauth2' },
        { name: 'b', requires: 'token' },
        { name: 'c', requires: 'key' },
        { name: 'd', requires: 'sidecar' },
      ],
    })
    const cat = await getSourceCatalog()
    expect(cat.sources.map((s) => s.requires)).toEqual(['', 'token', 'key', 'sidecar'])
  })

  it('version/generated_at 缺失 → null/""（不冒 0/不编造时间）', async () => {
    requestMock.mockResolvedValue({ sources: [], domains: [] })
    const cat = await getSourceCatalog()
    expect(cat.version).toBeNull()
    expect(cat.generatedAt).toBe('')
    expect(cat.servicesNote).toBe('')
  })

  it('整包非对象（null/数组/字符串）→ 空壳，不炸不冒', async () => {
    for (const bad of [null, [], 'oops', 42]) {
      requestMock.mockResolvedValue(bad)
      const cat = await getSourceCatalog()
      expect(cat).toEqual({ version: null, generatedAt: '', servicesNote: '', sources: [], domains: [] })
    }
  })

  it('sources/domains 顶层非数组 → []', async () => {
    requestMock.mockResolvedValue({ version: 1, sources: { a: 1 }, domains: 'snapshot' })
    const cat = await getSourceCatalog()
    expect(cat.sources).toEqual([])
    expect(cat.domains).toEqual([])
  })

  it('源行 domains 非数组 → []；数组混非字符串被过滤；缺字段 feed 类全 ""', async () => {
    requestMock.mockResolvedValue({
      version: 1,
      sources: [
        { name: 'a', domains: 'snapshot' },
        { name: 'b', domains: ['flash', 1, null, 'news'] },
        { name: 'c' }, // 无任何归一化入键
      ],
    })
    const cat = await getSourceCatalog()
    expect(cat.sources[0].domains).toEqual([])
    expect(cat.sources[1].domains).toEqual(['flash', 'news'])
    expect(cat.sources[2]).toEqual({
      id: 'c',
      registered: false,
      domains: [],
      feeTier: '',
      feeNote: '',
      official: false,
      requires: '',
      via: '',
      notes: '',
      riskNote: '',
    })
  })

  it('registered/official 严格布尔：脏真值（1/"true"）→ false（前端不猜）', async () => {
    requestMock.mockResolvedValue({
      version: 1,
      sources: [
        { name: 'a', registered: 1, official: 'true' },
        { name: 'b', registered: true, official: true },
      ],
    })
    const cat = await getSourceCatalog()
    expect(cat.sources[0].registered).toBe(false)
    expect(cat.sources[0].official).toBe(false)
    expect(cat.sources[1].registered).toBe(true)
    expect(cat.sources[1].official).toBe(true)
  })

  it('域链行：name 缺 → ""；default_chain/candidates 非数组 → []；camel 兜底', async () => {
    requestMock.mockResolvedValue({
      version: 1,
      domains: [
        { default_chain: ['eastmoney'], candidates: 'eastmoney' }, // 缺 name
        { name: 'flash', defaultChain: ['eastmoney'], candidates: ['eastmoney', 'sina7x24'] },
        { name: 'news', default_chain: 1, candidates: null },
      ],
    })
    const cat = await getSourceCatalog()
    expect(cat.domains[0]).toEqual({ name: '', defaultChain: ['eastmoney'], candidates: [] })
    expect(cat.domains[1]).toEqual({ name: 'flash', defaultChain: ['eastmoney'], candidates: ['eastmoney', 'sina7x24'] })
    expect(cat.domains[2]).toEqual({ name: 'news', defaultChain: [], candidates: [] })
  })
})
