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
})
