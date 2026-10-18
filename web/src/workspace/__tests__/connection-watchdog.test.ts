import { beforeEach, expect, it, vi } from 'vitest'
import { refreshDomains, startWatchdog } from '../connection-watchdog'
import type { WsPushMessage, WsResyncContext } from '@/ws/hub'

const state = vi.hoisted(() => ({ account: vi.fn(), trade: vi.fn(), risk: vi.fn(), market: vi.fn(), signal: vi.fn(), alert: vi.fn(), brain: vi.fn(),
  applyPush: vi.fn(), push: null as null | ((msg: WsPushMessage) => Promise<void> | void),
  recovery: null as null | ((context: WsResyncContext) => Promise<void>), off: vi.fn() }))
vi.mock('@/stores/account', () => ({ useAccountStore: () => ({ book: 'SIM', refresh: state.account, applyPush: state.applyPush }) }))
vi.mock('@/stores/trade', () => ({ useTradeStore: () => ({ refresh: state.trade }) }))
vi.mock('@/stores/risk', () => ({ useRiskStore: () => ({ refresh: state.risk }) }))
vi.mock('@/stores/market', () => ({ useMarketStore: () => ({ refresh: state.market }) }))
vi.mock('@/stores/signals', () => ({ useSignalsStore: () => ({ refresh: state.signal }) }))
vi.mock('@/stores/alerts', () => ({ useAlertsStore: () => ({ refresh: state.alert }) }))
vi.mock('@/stores/brain', () => ({ useBrainStore: () => ({ refresh: state.brain }) }))
vi.mock('@/ws/hub', () => ({
  onPush: (handler: typeof state.push) => { state.push = handler; return vi.fn() },
  onResync: (_topics: string[], handler: typeof state.recovery) => { state.recovery = handler; return state.off },
}))
beforeEach(() => {
  vi.clearAllMocks()
  for (const job of [state.account, state.trade, state.risk, state.market, state.signal, state.alert, state.brain]) job.mockResolvedValue(true)
  state.applyPush.mockReturnValue(false)
  state.push = null
})

const positionMsg: WsPushMessage = { topic: 'position', data: { kind: 'full' }, seq: 1, metadata: { entity_version: 5 } }

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

it('账户快照推送被增量消化：订单/成交与风控按原语义刷新，账户 REST 不再发', async () => {
  state.applyPush.mockReturnValue(true)
  const stop = startWatchdog()
  state.account.mockClear(); state.trade.mockClear(); state.risk.mockClear() // 去掉启动 refreshAll 的计数
  await state.push!(positionMsg)
  expect(state.applyPush).toHaveBeenCalledWith(positionMsg.data, positionMsg.metadata)
  expect(state.trade).toHaveBeenCalledTimes(1)
  expect(state.risk).toHaveBeenCalledTimes(1)
  expect(state.account).not.toHaveBeenCalled()
  stop()
})

it('解析不了的 position 帧整域回退：账户/订单/风控都权威刷新', async () => {
  state.applyPush.mockReturnValue(false)
  const stop = startWatchdog()
  state.account.mockClear(); state.trade.mockClear(); state.risk.mockClear() // 去掉启动 refreshAll 的计数
  await state.push!(positionMsg)
  expect(state.account).toHaveBeenCalledTimes(1)
  expect(state.trade).toHaveBeenCalledTimes(1)
  expect(state.risk).toHaveBeenCalledTimes(1)
  stop()
})

it('隐藏页 REST 轮询降频；隐藏期间推送路径不受影响', async () => {
  vi.useFakeTimers()
  let hidden = false
  try {
    const stop = startWatchdog({ pollVisibleMs: 100, pollHiddenMs: 1000, isHidden: () => hidden })
    state.account.mockClear(); state.market.mockClear(); state.risk.mockClear()
    await vi.advanceTimersByTimeAsync(350)
    const visiblePolls = state.account.mock.calls.length
    expect(visiblePolls).toBe(3)
    state.account.mockClear()
    hidden = true
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(1050)
    expect(state.account).toHaveBeenCalledTimes(1) // 60s 级节奏（这里是 1000ms），不再 10s 级
    // 推送链路不随轮询降频：隐藏时告警/数据照样触发权威刷新。
    state.applyPush.mockReturnValue(true)
    await state.push!(positionMsg)
    expect(state.trade).toHaveBeenCalledTimes(1)
    stop()
  } finally {
    vi.useRealTimers()
  }
})
