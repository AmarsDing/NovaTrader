import { describe, expect, it } from 'vitest'
import { pulseFromPush } from './pulses'

describe('pulseFromPush', () => {
  it('脑链路优先用 trace，没有标识符则丢弃', () => {
    const traced = pulseFromPush({ topic: 'brain', data: { traceId: 'abc', symbol: '600000' } }, 10)
    expect(traced).toMatchObject({
      from: 'service:brain',
      to: 'post:model',
      identifier: { kind: 'trace', value: 'abc' },
    })

    const symbolOnly = pulseFromPush({ topic: 'brain', data: { trace_id: '', symbol: '000001' } }, 11)
    expect(symbolOnly?.identifier).toEqual({ kind: 'symbol', value: '000001' })

    expect(pulseFromPush({ topic: 'brain', data: { stage: 'score' } }, 12)).toBeNull()
  })

  it('信号、行情、告警按岗位连边', () => {
    expect(pulseFromPush({ topic: 'signal', data: { id: '88' } }, 1)?.from).toBe('post:model')
    expect(pulseFromPush({ topic: 'signal', data: { id: '88' } }, 1)?.to).toBe('post:desk')
    expect(pulseFromPush({ topic: 'signal', data: {} }, 1)).toBeNull()
    expect(pulseFromPush({ topic: 'market', data: { symbol: '600000' } }, 1)).toMatchObject({
      from: 'service:market',
      to: 'post:scan',
    })
    expect(pulseFromPush({ topic: 'market', data: {} }, 1)).toBeNull()
    expect(pulseFromPush({ topic: 'alert', data: { kind: 'breaker' } }, 1)).toMatchObject({
      from: 'post:risk',
      to: 'book:center',
      identifier: { kind: 'rule', value: 'breaker' },
    })
  })

  it('持仓和续传不产生脉冲', () => {
    expect(pulseFromPush({ topic: 'position', data: { equity: 1 } }, 1)).toBeNull()
    expect(pulseFromPush({ topic: 'resume', data: {} }, 1)).toBeNull()
  })

  it('真实SignalUpdated字段与信封身份能到达画布，重投保持同一事件ID', () => {
    const msg = { topic: 'signal', data: { signal_id: 's-9', stock_code: '600519.SH', status: 'approved' }, metadata: { event_id: 'e-9', trace_id: 't-1', subject: 'strategy.signal.updated', source_time: '2026-10-15T01:30:00Z' } }
    const first = pulseFromPush(msg, 100)
    expect(first).toMatchObject({ id: 'event:e-9', identifier: { kind: 'signal', value: 's-9' }, traceId: 't-1', subject: 'strategy.signal.updated', sourceTime: '2026-10-15T01:30:00Z' })
    expect(pulseFromPush(msg, 200)?.id).toBe(first?.id)
  })

  it('真实行情批次不编证券，不把没有身份的空对象当传输', () => {
    const batch = pulseFromPush({ topic: 'market', data: { source: 'tdx', seq: 42, as_of: '2026-10-15T01:30:01Z', count: 5000 } }, 100)
    expect(batch?.identifier).toEqual({ kind: 'source', value: 'tdx · 批次 42' })
    expect(batch?.sourceTime).toBe('')
    expect(pulseFromPush({ topic: 'market', data: { source: 'tdx', count: 0 } }, 100)).toBeNull()
  })
})
