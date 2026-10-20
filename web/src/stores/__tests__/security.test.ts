/**
 * 证券名称字典（方案 §6.2）纪律测试：
 * - put 合并：null/undefined/'' name 不覆盖已有名，updatedAt 只增；
 * - resolve hasName=false → UI 显「名称未返回」（数据纪律：没取到≠空串冒充）；
 * - ensure：注入 fetcher 后台补齐，300ms 批量合并 + 重复 ensure 去重 + 失败冷却 5min；
 * - LRU 3000 条封顶按 updatedAt 最旧淘汰；hydrate 从 localStorage 恢复。
 */
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { normalizeCode, useSecurityStore } from '../security'

const KEY = 'nt.secbasic.v1'

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})

describe('normalizeCode 各市场规则', () => {
  it('裸六位归一：6xx/605/688→.SH，00x/30x→.SZ，4xx/8xx/92x→.BJ', () => {
    expect(normalizeCode('600519')).toBe('600519.SH')
    expect(normalizeCode('605099')).toBe('605099.SH')
    expect(normalizeCode('688981')).toBe('688981.SH')
    expect(normalizeCode('000001')).toBe('000001.SZ')
    expect(normalizeCode('300750')).toBe('300750.SZ')
    expect(normalizeCode('430047')).toBe('430047.BJ')
    expect(normalizeCode('830799')).toBe('830799.BJ')
    expect(normalizeCode('920001')).toBe('920001.BJ')
  })

  it('带后缀/前缀规整为大写全码，认不出的原文透传（trim+大写）', () => {
    expect(normalizeCode('600519.sh')).toBe('600519.SH')
    expect(normalizeCode(' SZ000001 ')).toBe('000001.SZ')
    expect(normalizeCode('bj920001')).toBe('920001.BJ')
    expect(normalizeCode('HSI')).toBe('HSI')
    expect(normalizeCode('')).toBe('')
  })
})

describe('put 合并纪律', () => {
  it('新建条目写入并可经裸码/全码读取', () => {
    const sec = useSecurityStore()
    sec.put([{ code: '600519', name: '贵州茅台', market: 'SH', industry: '白酒', st: false, peTtm: 22.5 }])
    expect(sec.get('600519.SH')?.name).toBe('贵州茅台')
    expect(sec.get('600519')?.code).toBe('600519.SH')
    expect(sec.get('600519')?.peTtm).toBe(22.5)
  })

  it('null/undefined/空串 name 不覆盖已有名称', () => {
    const sec = useSecurityStore()
    sec.put([{ code: '600519.SH', name: '贵州茅台' }])
    sec.put([{ code: '600519.SH', name: null }])
    expect(sec.get('600519.SH')?.name).toBe('贵州茅台')
    sec.put([{ code: '600519.SH' }])
    expect(sec.get('600519.SH')?.name).toBe('贵州茅台')
    sec.put([{ code: '600519.SH', name: '' }])
    expect(sec.get('600519.SH')?.name).toBe('贵州茅台')
  })

  it('updatedAt 只增：旧快照整体拒收，其它字段不回盖', () => {
    const sec = useSecurityStore()
    sec.put([{ code: '600519.SH', name: '贵州茅台', industry: '白酒', updatedAt: 2000 }])
    sec.put([{ code: '600519.SH', name: null, industry: '食品', updatedAt: 1000 }])
    expect(sec.get('600519.SH')?.industry).toBe('白酒')
    expect(sec.get('600519.SH')?.updatedAt).toBe(2000)
    // 无 updatedAt 的补丁按 now 合并，updatedAt 不倒退。
    sec.put([{ code: '600519.SH', pb: 8.1, updatedAt: 3000 }])
    expect(sec.get('600519.SH')?.pb).toBe(8.1)
    expect(sec.get('600519.SH')?.updatedAt).toBe(3000)
  })

  it('坏输入静默跳过，不崩溃不写库', () => {
    const sec = useSecurityStore()
    sec.put([{ code: '' }, { code: '   ' }])
    expect(localStorage.getItem(KEY)).toBeNull()
  })
})

describe('resolve 缺名回退', () => {
  it('已知名称：hasName=true；未取到：name 空串 + hasName=false（UI 显「名称未返回」）', () => {
    const sec = useSecurityStore()
    expect(sec.resolve('600519')).toEqual({ code: '600519.SH', name: '', hasName: false })
    sec.put([{ code: '600519.SH', name: '贵州茅台' }])
    expect(sec.resolve('600519')).toEqual({ code: '600519.SH', name: '贵州茅台', hasName: true })
    expect(sec.resolve('600519.SH').hasName).toBe(true)
  })
})

describe('ensure 批量补齐', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('fetcher 未注册时静默 noop', () => {
    const sec = useSecurityStore()
    expect(() => sec.ensure(['600519'])).not.toThrow()
    vi.advanceTimersByTime(1000)
  })

  it('300ms 窗口合并成一次请求；重复 ensure 去重', async () => {
    const sec = useSecurityStore()
    const fetcher = vi.fn(async (codes: string[]) => codes.map((code) => ({ code, name: `名-${code}` })))
    sec.registerBasicsFetcher(fetcher)
    sec.ensure(['600519'])
    sec.ensure(['600519']) // 重复：仍在 pending，不叠加
    sec.ensure(['000001.SZ', '600519.SH'])
    await vi.advanceTimersByTimeAsync(300)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher).toHaveBeenCalledWith(['600519.SH', '000001.SZ'])
    expect(sec.get('600519.SH')?.name).toBe('名-600519.SH')
    // 已有名后再次 ensure 不再发请求。
    sec.ensure(['600519'])
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('取数失败冷却 5min：期间不再请求；查无此名同样冷却', async () => {
    const sec = useSecurityStore()
    const fetcher = vi.fn()
    fetcher.mockRejectedValueOnce(new Error('boom'))
    sec.registerBasicsFetcher(fetcher)
    sec.ensure(['600519'])
    await vi.advanceTimersByTimeAsync(300)
    expect(fetcher).toHaveBeenCalledTimes(1)
    // 失败不落 0 不填空名：仍缺名，hasName=false。
    expect(sec.resolve('600519').hasName).toBe(false)
    // 冷却中：再 ensure 不重发。
    sec.ensure(['600519'])
    await vi.advanceTimersByTimeAsync(300)
    expect(fetcher).toHaveBeenCalledTimes(1)
    // 冷却期满：允许重试，取回仍无名 → 继续冷却不刷接口。
    fetcher.mockResolvedValueOnce([])
    await vi.advanceTimersByTimeAsync(5 * 60 * 1000)
    sec.ensure(['600519'])
    await vi.advanceTimersByTimeAsync(300)
    expect(fetcher).toHaveBeenCalledTimes(2)
    sec.ensure(['600519'])
    await vi.advanceTimersByTimeAsync(300)
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(sec.resolve('600519').hasName).toBe(false)
  })
})

describe('LRU 与持久化', () => {
  it('3000 条封顶：超量按 updatedAt 最旧淘汰', () => {
    const sec = useSecurityStore()
    // 字典填充主路是批量 put（首屏全量/缺名批量），一次性 3001 条即可判淘汰顺序。
    sec.put(Array.from({ length: 3001 }, (_, i) => ({ code: `${600000 + i}.SH`, name: `N${i}`, updatedAt: i })))
    expect(sec.basics.size).toBe(3000)
    // updatedAt=0 的最老条目被淘汰，最新一条保留。
    expect(sec.basics.has('600000.SH')).toBe(false)
    expect(sec.basics.has('603000.SH')).toBe(true)
    // 再来一条新码（updatedAt 取 now，最大）：留新汰旧，现存最老的 600001 被挤掉。
    sec.put([{ code: '699999.SH', name: 'X' }])
    expect(sec.basics.size).toBe(3000)
    expect(sec.basics.has('699999.SH')).toBe(true)
    expect(sec.basics.has('600001.SH')).toBe(false)
  })

  it('hydrate 从 localStorage 恢复；损坏存储回空不崩', () => {
    const sec = useSecurityStore()
    sec.put([
      { code: '600519.SH', name: '贵州茅台', market: 'SH', industry: '白酒', st: false, peTtm: 22.5, updatedAt: 1000 },
      { code: '920001.BJ', name: null, updatedAt: 1001 },
    ])
    setActivePinia(createPinia())
    const restored = useSecurityStore()
    restored.hydrate()
    expect(restored.get('600519')?.name).toBe('贵州茅台')
    expect(restored.get('600519')?.peTtm).toBe(22.5)
    expect(restored.resolve('920001')).toEqual({ code: '920001.BJ', name: '', hasName: false })

    // 损坏 JSON / 非数组内容净化为空表。
    localStorage.setItem(KEY, '{oops')
    setActivePinia(createPinia())
    const broken = useSecurityStore()
    broken.hydrate()
    expect(broken.basics.size).toBe(0)
    localStorage.setItem(KEY, '{"a":1}')
    setActivePinia(createPinia())
    const wrong = useSecurityStore()
    wrong.hydrate()
    expect(wrong.basics.size).toBe(0)
  })
})
