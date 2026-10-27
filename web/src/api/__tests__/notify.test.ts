/**
 * M10 错配修复（W2-a）：REST MessageReply 的 priority/category/dedup_key 不再被归一丢弃——
 * level 回落 priority、kind 回落 category、causeKey 与 WS 推送条同口径，feed/api 合并去重可用。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
import { listMessages } from '@/api/notify'
import { request } from '@/api/http'

const requestMock = vi.mocked(request)

beforeEach(() => vi.resetAllMocks())

describe('notify REST 归一（W2-a）', () => {
  it('priority/category/dedup_key 全保留：critical 落到 level，category 落 kind，causeKey 取前两段', async () => {
    requestMock.mockResolvedValue({
      messages: [{
        id: 7, category: 'risk', priority: 'critical', title: '熔断', body: '触发',
        dedup_key: 'risk:breaker:LIVE', created_at: '2026-10-24T09:30:00Z', read_at: '',
      }],
    })
    const [m] = await listMessages()
    expect(m.level).toBe('critical')
    expect(m.kind).toBe('risk')
    expect(m.priority).toBe('critical')
    expect(m.category).toBe('risk')
    expect(m.dedupKey).toBe('risk:breaker:LIVE')
    // causeKey 冒号截前两段（防后端去重计数尾段漂移），与告警漏斗同口径。
    expect(m.causeKey).toBe('risk:breaker')
    expect(m.read).toBe(false)
    expect(m.time).toBe('2026-10-24T09:30:00Z')
  })

  it('level/severity 原值优先于 priority；read_at 非空即已读', async () => {
    requestMock.mockResolvedValue({
      messages: [{
        id: 8, category: 'market', priority: 'high', level: 'warn',
        dedup_key: 'market:600519.SH:surge', read_at: '2026-10-24T15:00:00Z',
      }],
    })
    const [m] = await listMessages()
    expect(m.level).toBe('warn')
    expect(m.read).toBe(true)
  })

  it('无 dedup_key 的行 causeKey 回落「alert:系统」，不误造键', async () => {
    requestMock.mockResolvedValue({ messages: [{ id: 9, category: 'review', priority: 'medium', title: '复盘' }] })
    const [m] = await listMessages()
    expect(m.causeKey).toBe('alert:系统')
  })
})
