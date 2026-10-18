import { beforeEach, describe, expect, it, vi } from 'vitest'
import { dispositionLabel, listDispositions, writeDisposition } from '@/api/traces'
import { request } from '@/api/http'
vi.mock('@/api/http', () => ({ request: vi.fn() }))
beforeEach(() => vi.clearAllMocks())
describe('disposition receipts client', () => {
  it('queries with event or trace scope and normalizes snake/camel aliases', async () => {
    vi.mocked(request).mockResolvedValue({
      items: [
        { id: 2, event_id: 'e2', trace_id: 't1', action: 'resolved', note: '对账一致', actor: 'owner', created_at: '2026-10-18T02:00:00Z' },
        { id: 1, eventId: 'e1', traceId: 't1', action: 'acknowledged', note: '', actor: 'owner', createdAt: '2026-10-18T01:00:00Z' },
      ],
    })
    const signal = new AbortController().signal
    const out = await listDispositions({ traceId: 't1' }, signal)
    expect(request).toHaveBeenCalledWith('/api/ops/dispositions', {
      query: { event_id: undefined, trace_id: 't1', limit: 100 },
      signal,
    })
    expect(out).toHaveLength(2)
    expect(out[0]).toMatchObject({ id: 2, eventId: 'e2', traceId: 't1', action: 'resolved', note: '对账一致', actor: 'owner', createdAt: '2026-10-18T02:00:00Z' })
  })
  it('posts only closed-set actions with exact wire field names', async () => {
    vi.mocked(request).mockResolvedValue({ ok: true })
    await writeDisposition({ eventId: 'e1', traceId: 't1', action: 'investigating', note: '持续观察' })
    expect(request).toHaveBeenCalledWith('/api/ops/dispositions', {
      method: 'POST',
      json: { event_id: 'e1', trace_id: 't1', action: 'investigating', note: '持续观察' },
    })
    await expect(writeDisposition({ eventId: 'e1', action: 'replay' })).rejects.toThrow('允许集合')
    expect(request).toHaveBeenCalledTimes(1)
  })
  it('renders labels for known actions and keeps unknown ones visible, not silent', () => {
    expect(dispositionLabel('acknowledged')).toBe('已知晓')
    expect(dispositionLabel('weird')).toContain('未知动作')
  })
})
