import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getTraceCoverage } from '@/api/traces'
import { request } from '@/api/http'
vi.mock('@/api/http', () => ({ request: vi.fn() }))
beforeEach(() => vi.clearAllMocks())
describe('trace coverage client', () => {
  it('hits the fixed read-only path and normalizes snake/camel aliases', async () => {
    vi.mocked(request).mockResolvedValue({
      schema_version: 1,
      available: true,
      scope: 'durable_outbox_history',
      as_of: '2026-10-19T10:00:00Z',
      note: '只覆盖落盘域事件',
      window_limit: 2000,
      window_rows: 5,
      oldest_in_window: '2026-10-19T08:00:00Z',
      totals: { total: 5, with_trace: 3, without_trace: 2, children: 3, orphan_parents: 1 },
      by_source: [
        { source: 'notify', total: 2, withTrace: 2, withoutTrace: 0, children: 2, orphanParents: 0 },
        { source: 'trade', total: 3, with_trace: 1, without_trace: 2, children: 1, orphan_parents: 1 },
      ],
    })
    const signal = new AbortController().signal
    const out = await getTraceCoverage(signal)
    expect(request).toHaveBeenCalledWith('/api/ops/trace-coverage', { signal })
    expect(out.available).toBe(true)
    expect(out.totals).toMatchObject({ total: 5, withTrace: 3, withoutTrace: 2, children: 3, orphanParents: 1 })
    expect(out.bySource).toHaveLength(2)
    expect(out.bySource[0]).toMatchObject({ source: 'notify', withTrace: 2, orphanParents: 0 })
    expect(out.bySource[1]).toMatchObject({ source: 'trade', withoutTrace: 2, orphanParents: 1 })
    expect(out.windowRows).toBe(5)
    expect(out.oldestInWindow).toBe('2026-10-19T08:00:00Z')
  })
  it('keeps unparsable counts null instead of zero-filling the unknown', async () => {
    vi.mocked(request).mockResolvedValue({
      schemaVersion: 1,
      scope: 'durable_outbox_history',
      available: false,
      totals: { total: 'many', withTrace: null, withoutTrace: undefined, children: -1, orphanParents: 0 },
      bySource: 'nope',
    })
    const out = await getTraceCoverage()
    expect(out.available).toBe(false)
    expect(out.totals.total).toBeNull()
    expect(out.totals.withTrace).toBeNull()
    expect(out.totals.withoutTrace).toBeNull()
    expect(out.totals.children).toBeNull()
    expect(out.totals.orphanParents).toBe(0)
    expect(out.bySource).toEqual([])
  })
  it('rejects a mismatched contract instead of guessing at the shape', async () => {
    vi.mocked(request).mockResolvedValue({ schema_version: 1, scope: 'something_else' })
    await expect(getTraceCoverage()).rejects.toThrow('合同不可用')
    vi.mocked(request).mockResolvedValue({ schema_version: 2, scope: 'durable_outbox_history' })
    await expect(getTraceCoverage()).rejects.toThrow('合同不可用')
  })
})
