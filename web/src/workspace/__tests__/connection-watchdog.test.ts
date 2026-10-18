import { beforeEach, expect, it, vi } from 'vitest'
import { refreshDomains, startWatchdog } from '../connection-watchdog'
import type { WsResyncContext } from '@/ws/hub'

const state = vi.hoisted(() => ({ account: vi.fn(), trade: vi.fn(), risk: vi.fn(), market: vi.fn(), signal: vi.fn(), alert: vi.fn(), brain: vi.fn(),
  recovery: null as null | ((context: WsResyncContext) => Promise<void>), off: vi.fn() }))
vi.mock('@/stores/account', () => ({ useAccountStore: () => ({ book: 'SIM', refresh: state.account }) }))
vi.mock('@/stores/trade', () => ({ useTradeStore: () => ({ refresh: state.trade }) }))
vi.mock('@/stores/risk', () => ({ useRiskStore: () => ({ refresh: state.risk }) }))
vi.mock('@/stores/market', () => ({ useMarketStore: () => ({ refresh: state.market }) }))
vi.mock('@/stores/signals', () => ({ useSignalsStore: () => ({ refresh: state.signal }) }))
vi.mock('@/stores/alerts', () => ({ useAlertsStore: () => ({ refresh: state.alert }) }))
vi.mock('@/stores/brain', () => ({ useBrainStore: () => ({ refresh: state.brain }) }))
vi.mock('@/ws/hub', () => ({ onPush: () => vi.fn(), onResync: (_topics: string[], handler: typeof state.recovery) => { state.recovery = handler; return state.off } }))
beforeEach(() => {
  vi.clearAllMocks()
  for (const job of [state.account, state.trade, state.risk, state.market, state.signal, state.alert, state.brain]) job.mockResolvedValue(true)
})

it('只重查所需领域，重复依赖合并；任何失败不会确认恢复', async () => {
  state.risk.mockResolvedValue(false)
  await expect(refreshDomains(['position', 'alert'])).rejects.toThrow('风控权威状态未恢复')
  expect(state.risk).toHaveBeenCalledTimes(1)
  expect(state.trade).toHaveBeenCalledWith('SIM')
  expect(state.market).not.toHaveBeenCalled()
  expect(state.signal).not.toHaveBeenCalled()
})

it('恢复处理器等待真实领域读取，取消后不可确认，退出释放', async () => {
  const stop = startWatchdog()
  const controller = new AbortController()
  controller.abort()
  await expect(state.recovery!({ serverEpoch: 'e', topics: ['signal'], watermarks: { signal: 1 }, reason: 'restart', signal: controller.signal })).rejects.toThrow('取消')
  stop()
  expect(state.off).toHaveBeenCalledTimes(1)
})
