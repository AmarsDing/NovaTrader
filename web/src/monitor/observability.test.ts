import { expect, it } from 'vitest'
import { normalizeObservability } from '@/api/observability'
import { observationSections, topicsForNode } from './observability'

it('观测缺失显示未知，外部数据源节点不获得伪造的网关计数', () => {
  expect(observationSections(null, ['signal'])[0]?.rows?.[0]?.value).toContain('不可用')
  expect(topicsForNode('source:tdx')).toEqual([])
  expect(observationSections(null, [])).toEqual([])
})

it('相关主题计数和白名单样例可查，源时间不从事件形成时间或页面时钟填充', () => {
  const snapshot = normalizeObservability({ schema_version: 1, available: true, scope: 'admin_gateway_ingress', as_of: '2026-10-15T01:31:00Z', server_epoch: 'epoch-1', window_start: '2026-10-15T01:30:00Z', window_seconds: 60, window_observed_seconds: 8.25, window_complete: false, nats_connected: true,
    registered_routes: [{ subject: 'strategy.signal.updated', topic: 'signal', transport: 'jetstream', binding_state: 'failed' }],
    edges: [{ topic: 'signal', source: 'strategy', subject: 'strategy.signal.updated', window_count: 2, window_bytes: 300, window_errors: 0, received_total: 8, accepted_total: 7 }, { topic: 'position', source: 'trade', subject: 'trade.account', window_count: 99 }],
    samples: [{ topic: 'signal', event_id: 'e-1', trace_id: 't-1', event_time: '2026-10-15T01:30:00Z', received_at: '2026-10-15T01:30:01Z', subject: 'strategy.signal.updated', source: 'strategy', payload: { signal_id: 's-1', cash: '9999' } }],
  })
  const sections = observationSections(snapshot, ['signal'])
  const counters = sections.find((item) => item.key === 'ingress-counters')!
  expect(counters.table?.rows).toHaveLength(1)
  expect(counters.table?.rows[0]?.[1]).toBe('2 / 300 B / 0')
  expect(counters.foot).toContain('不是业务ACK')
  expect(sections.find((item) => item.key === 'ingress-basis')?.rows?.find((row) => row.key === 'coverage')?.value).toBe('窗口尚不完整 · 已观测8.3秒')
  expect(sections.find((item) => item.key === 'ingress-routes')?.table?.rows[0]?.[1]).toBe('绑定失败')
  const sample = sections.find((item) => item.key === 'ingress-samples')!.table!.rows[0]!
  expect(sample[0]).toContain('源时间未知')
  expect(sample[1]).toContain('e-1 / t-1')
  expect(sample[3]).toContain('s-1')
  expect(sample[3]).not.toContain('cash')
})
