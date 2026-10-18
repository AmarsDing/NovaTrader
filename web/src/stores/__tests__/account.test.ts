import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, it, vi } from 'vitest'
import { useAccountStore } from '../account'
import { getAccount, listPositions, type AccountSnapshot } from '@/api/trade'
import type { WsEventMetadata } from '@/ws/topics'

vi.mock('@/api/trade', () => ({ getAccount: vi.fn(), listPositions: vi.fn() }))
beforeEach(() => {
  vi.resetAllMocks()
  setActivePinia(createPinia())
})

it('慢到的SIM刷新不能覆盖当前LIVE共享账本', async () => {
  const account = useAccountStore()
  let release: ((value: AccountSnapshot) => void) | undefined
  vi.mocked(getAccount).mockImplementationOnce(() => new Promise((resolve) => {
    release = resolve
  }))
  vi.mocked(listPositions).mockResolvedValueOnce([{ symbol: 'SIM', quantity: 1, available: 1, avgCost: 1, lastPrice: 1 }])
  const old = account.refresh()
  account.book = 'LIVE'
  vi.mocked(getAccount).mockResolvedValueOnce({ account: 'LIVE', equity: 200, cash: 150, unrealized: 0, realized: 0, dayPnl: null, paperDays: 0, openHalted: false, haltReason: '' })
  vi.mocked(listPositions).mockResolvedValueOnce([{ symbol: 'LIVE', quantity: 2, available: 2, avgCost: 1, lastPrice: 1 }])
  await account.refresh()
  release?.({ account: 'SIM', equity: 100, cash: 100, unrealized: 0, realized: 0, dayPnl: null, paperDays: 20, openHalted: false, haltReason: '' })
  await old
  expect(account.book).toBe('LIVE')
  expect(account.equity).toBe(200)
  expect(account.positions[0]?.symbol).toBe('LIVE')
})

/** 与后端 accountstate.Snapshot 同形的推送载荷。 */
function pushBody(generation: number, equity: number, overrides: Record<string, unknown> = {}) {
  return {
    version: 1, kind: 'full', account_id: 'default', account_type: 'SIM',
    snapshot_id: `snap-${generation}`, generation,
    as_of: new Date(Date.now() - 500).toISOString(), trading_day: '2026-10-18',
    cash: equity - 10000, frozen_cash: 0, equity, available: equity - 10000,
    day_start_equity: equity, day_start_known: true,
    holdings: [{ symbol: '600519.SH', quantity: 100, available: 100, frozen_sell: 0, avg_cost: 95, market_value: 10000, held_days: 1, stop_price: 0, take_price: 0 }],
    open_orders: [], observed_order_ids: ['ord-1'],
    reconciliation: { status: 'matched', as_of: new Date(Date.now() - 1000).toISOString(), reason: '' },
    ...overrides,
  }
}
function pushMeta(generation: number): WsEventMetadata {
  return { entity_id: 'SIM:default', entity_version: generation }
}

it('完整账户快照推送按实体版本增量应用，免掉账户/持仓 REST', () => {
  const account = useAccountStore()
  expect(account.applyPush(pushBody(5, 110000), pushMeta(5))).toBe(true)
  expect(account.equity).toBe(110000)
  expect(account.cash).toBe(100000)
  expect(account.unrealized).toBeCloseTo(500, 6)
  expect(account.positions[0]?.symbol).toBe('600519.SH')
  expect(account.accountOk).toBe(true)
  expect(account.positionsOk).toBe(true)
  expect(getAccount).not.toHaveBeenCalled()
  expect(listPositions).not.toHaveBeenCalled()
})

it('乱序/重放的旧版本不改状态，也不触发 REST 回退', () => {
  const account = useAccountStore()
  account.applyPush(pushBody(5, 110000), pushMeta(5))
  expect(account.applyPush(pushBody(3, 90000), pushMeta(3))).toBe(true)
  expect(account.equity).toBe(110000)
  expect(getAccount).not.toHaveBeenCalled()
})

it('别的账本的帧与残缺载荷一律回退（false），状态不动', () => {
  const account = useAccountStore()
  const live = pushBody(5, 110000, { account_type: 'LIVE' })
  expect(account.applyPush(live, { entity_id: 'LIVE:default', entity_version: 5 })).toBe(false)
  expect(account.applyPush({ kind: 'hint' }, pushMeta(5))).toBe(false)
  expect(account.applyPush(pushBody(5, 110000), { entity_version: 0 })).toBe(false)
  expect(account.equity).toBeNull()
  expect(account.accountOk).toBe(false)
})

it('换账本后旧实体版本失效：SIM 帧不再被当成已覆盖', () => {
  const account = useAccountStore()
  account.applyPush(pushBody(5, 110000), pushMeta(5))
  account.book = 'LIVE'
  expect(account.equity).toBeNull()
  // LIVE 视野下 SIM 帧回退；切回 SIM 后同版本帧会重新应用（实体版本随账本重置）。
  expect(account.applyPush(pushBody(5, 110000), pushMeta(5))).toBe(false)
  account.book = 'SIM'
  expect(account.applyPush(pushBody(5, 110000), pushMeta(5))).toBe(true)
  expect(account.equity).toBe(110000)
})

it('慢 REST 不得覆盖飞行期间应用的新推送：推 Equity 保留，REST 只补独有字段', async () => {
  const account = useAccountStore()
  account.applyPush(pushBody(5, 110000), pushMeta(5))
  let release: ((value: AccountSnapshot) => void) | undefined
  vi.mocked(getAccount).mockImplementationOnce(() => new Promise((resolve) => { release = resolve }))
  vi.mocked(listPositions).mockResolvedValueOnce([{ symbol: 'OLD.SH', quantity: 1, available: 1, avgCost: 1, lastPrice: 1 }])
  const pending = account.refresh()
  // REST 飞行期间又来了更新的快照（G=6）。
  account.applyPush(pushBody(6, 120000), pushMeta(6))
  // REST 返回的是更早读到的旧实体（G≈5 时刻）。
  release?.({ account: 'SIM', equity: 110000, cash: 100000, unrealized: 400, realized: 7, dayPnl: null, paperDays: 21, openHalted: true, haltReason: '演练' })
  await pending
  expect(account.equity).toBe(120000)
  expect(account.cash).toBe(110000)
  expect(account.unrealized).toBeCloseTo(500, 6)
  expect(account.positions[0]?.symbol).toBe('600519.SH')
  // REST 独有字段仍补齐；accountOk/checkedAt 不被旧响应降级。
  expect(account.realized).toBe(7)
  expect(account.paperDays).toBe(21)
  expect(account.openHalted).toBe(true)
  expect(account.haltReason).toBe('演练')
  expect(account.accountOk).toBe(true)
  expect(account.dayPnl).toBe(0) // 推送口径（日初=权益），REST 的 null 不覆盖
})

it('突发推送压力：300 帧连续增量，最终值可见且全程零 REST', () => {
  const account = useAccountStore()
  for (let g = 1; g <= 300; g++) account.applyPush(pushBody(g, 100000 + g), pushMeta(g))
  expect(account.equity).toBe(100300)
  expect(account.accountOk).toBe(true)
  expect(getAccount).not.toHaveBeenCalled()
  expect(listPositions).not.toHaveBeenCalled()
})
