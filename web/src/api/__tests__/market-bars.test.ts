import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getBars } from '@/api/market'
import { request } from '@/api/http'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
const requestMock = vi.mocked(request)

function callArgs(index = 0): [string, { query: Record<string, unknown> }] {
  const call = requestMock.mock.calls[index]
  if (!call) throw new Error('request 未被调用')
  return call as [string, { query: Record<string, unknown> }]
}

describe('getBars 窗口透传（P2-9）', () => {
  beforeEach(() => {
    requestMock.mockReset()
    requestMock.mockResolvedValue({ bars: [] })
  })

  it('省略窗口：维持默认行数，不带 start/end 键值', async () => {
    await getBars('600519.SH', '1d')
    const [path, options] = callArgs(0)
    expect(path).toBe('/api/market/bars')
    expect(options.query).toMatchObject({ symbol: '600519.SH', freq: '1d', limit: 180, adjust: 'NONE' })
    expect(options.query.start).toBeUndefined()
    expect(options.query.end).toBeUndefined()

    await getBars('600519.SH', '1m')
    expect(callArgs(1)[1].query.limit).toBe(240)
  })

  it('给定窗口：start/end/limit 原样透传（后端含两端、limit 从 end 往前取）', async () => {
    await getBars('600519.SH', '1m', {
      start: '2026-10-19T09:31:00+08:00',
      end: '2026-10-19T11:30:00+08:00',
      limit: 800,
    })
    const [, options] = callArgs(0)
    expect(options.query).toMatchObject({
      symbol: '600519.SH',
      freq: '1m',
      limit: 800,
      adjust: 'NONE',
      start: '2026-10-19T09:31:00+08:00',
      end: '2026-10-19T11:30:00+08:00',
    })
  })

  it('响应归一：数值串转 number，缺值落 null，time 三键回退', async () => {
    requestMock.mockResolvedValue({
      bars: [{ time: '2026-10-19', open: '10', high: 11, low: null, close: 10.5, volume: '120', amount: 1260 }],
    })
    const rows = await getBars('600519.SH', '1d')
    expect(rows).toEqual([
      { time: '2026-10-19', open: 10, high: 11, low: null, close: 10.5, volume: 120, amount: 1260 },
    ])
  })

  it('响应归一回退链：time 缺省时按 barTime / bar_time 兜底取键', async () => {
    requestMock.mockResolvedValue({
      bars: [
        { barTime: '2026-10-20', open: 1, high: 1, low: 1, close: 1, volume: 1, amount: 1 },
        { bar_time: '2026-10-21', open: 2, high: 2, low: 2, close: 2, volume: 2, amount: 2 },
      ],
    })
    const rows = await getBars('600519.SH', '1d')
    expect(rows.map((r) => r.time)).toEqual(['2026-10-20', '2026-10-21'])
  })
})
