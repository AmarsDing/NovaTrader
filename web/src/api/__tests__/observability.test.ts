import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getObservability, normalizeObservability } from '@/api/observability'
import { request } from '@/api/http'
vi.mock('@/api/http', () => ({ request: vi.fn() }))
const contract = { schemaVersion: 1, scope: 'admin_gateway_ingress', available: true }
beforeEach(() => vi.clearAllMocks())
describe('gateway observability contract', () => {
  it('normalizes proto JSON counters without turning missing or invalid values into zero', () => {
    const out = normalizeObservability({
      ...contract,
      serverEpoch: 'epoch',
      windowSeconds: 60,
      edges: [
        {
          subject: 'trade.order',
          receivedTotal: '12',
          acceptedTotal: '0',
          bytesTotal: '1024',
          errorsTotal: false,
          windowCount: '3',
          windowErrors: '-1',
        },
      ],
      errorsTotal: '0',
    })
    expect(out.available).toBe(true)
    expect(out.edges[0]).toMatchObject({
      receivedTotal: 12,
      acceptedTotal: 0,
      bytesTotal: 1024,
      errorsTotal: null,
      windowCount: 3,
      windowBytes: null,
      windowErrors: null,
      lastReceivedAt: null,
    })
    expect(out.errorsTotal).toBe(0)
  })
  it('accepts snake aliases, preserves unknown source time and filters payload to safe scalar projection', () => {
    const out = normalizeObservability({
      schema_version: 1,
      scope: 'admin_gateway_ingress',
      available: true,
      window_seconds: 60,
      samples: [
        {
          event_id: 'e1',
          trace_id: 'trace',
          received_at: '2026-10-15T01:30:00Z',
          event_time: '2026-10-15T01:29:59Z',
          payload_status: 'projected',
          payload: {
            symbol: '600000',
            count: '5000',
            api_key: 'secret',
            cash: '12345',
            holdings: [{ qty: 1 }],
            reason: 'private',
            status: { nested: 'secret' },
          },
        },
      ],
    })
    expect(out.samples[0]).toMatchObject({
      eventId: 'e1',
      traceId: 'trace',
      sourceTime: null,
      eventTime: '2026-10-15T01:29:59Z',
      payload: { symbol: '600000', count: '5000' },
    })
    expect(JSON.stringify(out)).not.toContain('secret')
  })
  it('unsupported versions or unavailable hubs cannot expose apparently usable observations', () => {
    for (const input of [
      { schema_version: 99, scope: 'admin_gateway_ingress', available: true },
      { ...contract, available: false },
      {},
    ]) {
      const out = normalizeObservability({
        ...input,
        edges: [{ received_total: '100' }],
        samples: [{ event_id: 'untrusted' }],
        registered_routes: [{ subject: 'trade.order' }],
      })
      expect(out.available).toBe(false)
      expect(out.edges).toEqual([])
      expect(out.samples).toEqual([])
      expect(out.registeredRoutes).toEqual([])
    }
  })
  it('retains explicit sampling, coverage and overflow information', () => {
    const out = normalizeObservability({
      ...contract,
      samples_truncated: true,
      sample_capacity: 80,
      counter_capacity: 128,
      counter_overflow_total: '7',
      window_basis: '60_one_second_buckets_including_partial_current_second',
      statistics_scope: 'subject_source_counters;trace_id_filters_samples_only',
      unsupported: ['business_consumer_acknowledgement', 'latency_percentiles'],
    })
    expect(out).toMatchObject({
      samplesTruncated: true,
      sampleCapacity: 80,
      counterCapacity: 128,
      counterOverflowTotal: 7,
    })
    expect(out.unsupported).toContain('business_consumer_acknowledgement')
  })
  it('queries through the authenticated HTTP helper using exact subject and trace filters', async () => {
    vi.mocked(request).mockResolvedValue(contract)
    const signal = new AbortController().signal
    await getObservability({ subject: 'strategy.signal.updated', traceId: 'trace-1', limit: 5, signal })
    expect(request).toHaveBeenCalledWith('/api/ops/observability', {
      signal,
      query: { subject: 'strategy.signal.updated', trace_id: 'trace-1', limit: 5 },
    })
  })
})
