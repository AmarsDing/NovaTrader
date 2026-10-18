import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, it, vi } from 'vitest'
import { useAccountStore } from '../account'
import { getAccount, listPositions, type AccountSnapshot } from '@/api/trade'

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
