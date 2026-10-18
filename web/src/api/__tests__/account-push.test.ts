import { expect, it } from 'vitest'
import { parseAccountPush } from '../account-push'
import type { WsEventMetadata } from '@/ws/topics'

/** 与后端 pkg/accountstate.Snapshot 同形的完整载荷。 */
function snapshotBody(overrides: Record<string, unknown> = {}) {
  return {
    version: 1,
    kind: 'full',
    account_id: 'default',
    account_type: 'SIM',
    snapshot_id: 'snap-1',
    generation: 5,
    as_of: new Date(Date.now() - 1000).toISOString(),
    trading_day: '2026-10-18',
    cash: 100000,
    frozen_cash: 0,
    equity: 110000,
    available: 100000,
    day_start_equity: 108000,
    day_start_known: true,
    holdings: [
      { symbol: '600519.SH', quantity: 100, available: 100, frozen_sell: 0, avg_cost: 95, market_value: 10000, held_days: 3, stop_price: 0, take_price: 0 },
    ],
    open_orders: [],
    observed_order_ids: ['ord-1'],
    reconciliation: { status: 'matched', as_of: new Date(Date.now() - 2000).toISOString(), reason: '' },
    ...overrides,
  }
}

function meta(overrides: Partial<WsEventMetadata> = {}): WsEventMetadata {
  return { entity_id: 'SIM:default', entity_version: 5, ...overrides }
}

it('完整账户快照解析为归一化增量：equity/cash/unrealized/dayPnl/positions 同后端口径', () => {
  const parsed = parseAccountPush(snapshotBody(), meta())
  expect(parsed).not.toBeNull()
  expect(parsed!.book).toBe('SIM')
  expect(parsed!.generation).toBe(5)
  expect(parsed!.equity).toBe(110000)
  expect(parsed!.cash).toBe(100000)
  // 浮动 = 市值10000 − 成本95×100 = 500；当日盈亏 = 110000 − 108000 = 2000。
  expect(parsed!.unrealized).toBeCloseTo(500, 6)
  expect(parsed!.dayPnl).toBe(2000)
  expect(parsed!.positions).toHaveLength(1)
  expect(parsed!.positions[0]).toMatchObject({ symbol: '600519.SH', quantity: 100, available: 100, avgCost: 95, lastPrice: 100 })
})

it('小写 account_type 归一到大写账本', () => {
  const parsed = parseAccountPush(snapshotBody({ account_type: 'live' }), meta({ entity_id: 'LIVE:default' }))
  expect(parsed!.book).toBe('LIVE')
})

it('day_start 未知时当日盈亏为 null，不拿 0 顶', () => {
  const parsed = parseAccountPush(snapshotBody({ day_start_known: false, day_start_equity: 0 }), meta())
  expect(parsed!.dayPnl).toBeNull()
})

it.each([
  ['缺 holdings', () => { const b = snapshotBody() as Record<string, unknown>; delete b.holdings; return b }],
  ['kind 不是 full', () => snapshotBody({ kind: 'delta' })],
  ['version 不是 1', () => snapshotBody({ version: 2 })],
  ['account_id 不是 default', () => snapshotBody({ account_id: 'other' })],
  ['载荷 generation 与 entity_version 不一致', () => snapshotBody({ generation: 4 })],
  ['持仓行缺字段', () => snapshotBody({ holdings: [{ symbol: '600519.SH', quantity: 100 }] })],
  ['数量外的可卖（可用>数量）', () => snapshotBody({ holdings: [{ symbol: '600519.SH', quantity: 100, available: 101, frozen_sell: 0, avg_cost: 95, market_value: 10000, held_days: 1, stop_price: 0, take_price: 0 }] })],
])('残缺/不自洽载荷一律拒绝：%s', (_name, build) => {
  expect(parseAccountPush(build(), meta())).toBeNull()
})

it('entity_version 缺失/非正整数 → null', () => {
  expect(parseAccountPush(snapshotBody(), meta({ entity_version: 0 }))).toBeNull()
  expect(parseAccountPush(snapshotBody(), {})).toBeNull()
  expect(parseAccountPush(snapshotBody(), undefined)).toBeNull()
})

it('as_of 不可解析或来自未来（>5s 容忍）→ null', () => {
  expect(parseAccountPush(snapshotBody({ as_of: 'not-a-date' }), meta())).toBeNull()
  expect(parseAccountPush(snapshotBody({ as_of: new Date(Date.now() + 60000).toISOString() }), meta())).toBeNull()
  expect(parseAccountPush(snapshotBody({ as_of: new Date(Date.now() + 3000).toISOString() }), meta())).not.toBeNull()
})
