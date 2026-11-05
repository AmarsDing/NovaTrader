/**
 * 通知 symbol 契约（步骤 1/3）：同一消息经 REST 与 WS 归一后 symbol 与 id 必须一致；
 * 无 symbol 时两者都为 undefined，不从标题/正文推断，不补交易所后缀。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
import { getMessage, listMessages, normalizeMessage, normalizeNotifySymbol } from '@/api/notify'
import { request } from '@/api/http'

const requestMock = vi.mocked(request)

beforeEach(() => vi.resetAllMocks())

describe('notify symbol 契约', () => {
  it('REST 列表与单条保留 symbol，trim 后原样输出', async () => {
    const row = { id: 7, priority: 'high', category: 'signal', title: '突破', symbol: ' 600519.SH ', created_at: '2026-11-05T02:00:00Z' }
    requestMock.mockResolvedValueOnce({ messages: [row] })
    const [listed] = await listMessages()
    requestMock.mockResolvedValueOnce(row)
    const single = await getMessage('7')
    expect(listed.symbol).toBe('600519.SH')
    expect(single.symbol).toBe('600519.SH')
    expect(single.id).toBe(listed.id)
  })

  it('同一消息 WS/REST 归一后 symbol 与 id 相同', () => {
    const restRow = { id: 42, priority: 'high', category: 'signal', title: '封板', symbol: '600519.SH' }
    const rest = normalizeMessage(restRow)
    const ws = normalizeMessage(restRow, { time: '2026-11-05T02:00:01Z', read: false })
    expect(ws.symbol).toBe('600519.SH')
    expect(rest.symbol).toBe('600519.SH')
    expect(ws.id).toBe(rest.id)
  })

  it('无 symbol 的消息两者都归一为 undefined，且不从标题/正文猜股票', () => {
    const raw = { id: 43, priority: 'medium', category: 'review', title: '600519 复盘', body: '看看 000001.SZ' }
    const rest = normalizeMessage(raw)
    const ws = normalizeMessage(raw, { time: '2026-11-05T02:00:01Z', read: false })
    expect(rest.symbol).toBeUndefined()
    expect(ws.symbol).toBeUndefined()
  })

  it('symbol 归一：空白、非字符串一律 undefined', () => {
    expect(normalizeNotifySymbol('  ')).toBeUndefined()
    expect(normalizeNotifySymbol(600519)).toBeUndefined()
    expect(normalizeNotifySymbol(null)).toBeUndefined()
    expect(normalizeNotifySymbol('600519.SH')).toBe('600519.SH')
  })
})
