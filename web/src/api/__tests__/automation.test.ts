import { describe, expect, it } from 'vitest'
import { parsePlan } from '../automation'

function fixture() {
  return {
    plan: {
      version: 1,
      plan_id: 'p1',
      revision: 1,
      snapshot_id: 's1',
      signal_id: 1,
      symbol: '600519.SH',
      side: 'buy',
      price_origin: 'rules',
      account_type: 'SIM',
      account_id: 'default',
      entry_low: 9.8,
      entry_high: 10.2,
      limit_price: 10,
      stop_loss: 9,
      take_profit: 11,
      max_notional: 5000,
      valid_until: '2026-10-16T03:00:00Z',
      invalidation: ['过期'],
    },
    snapshot: { version: 1, snapshot_id: 's1', evidence: [], counter_evidence: [], unknowns: ['未校准'] },
    state: 'draft',
  }
}
describe('immutable plan contract', () => {
  it('accepts a coherent rule plan and preserves explicit uncertainty', () => {
    expect(parsePlan(fixture()).snapshot.unknowns).toEqual(['未校准'])
  })
  it('rejects mixed facts, model prices, missing budget and unsupported contract', () => {
    for (const mutate of [
      (v: ReturnType<typeof fixture>) => {
        v.plan.snapshot_id = 'other'
      },
      (v: ReturnType<typeof fixture>) => {
        v.plan.price_origin = 'model'
      },
      (v: ReturnType<typeof fixture>) => {
        v.plan.max_notional = NaN
      },
      (v: ReturnType<typeof fixture>) => {
        v.plan.version = 2
      },
    ]) {
      const v = fixture()
      mutate(v)
      expect(() => parsePlan(v)).toThrow()
    }
  })
})
