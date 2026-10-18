/**
 * 连接看门狗：原 MainLayout 的刷新逻辑（10s 轮询 + 推送触发）。
 * 抽出来给工作台用，也可被 CommandPalette 的「刷新」动作直接调用。
 */
import { useAccountStore } from '@/stores/account'
import { useMarketStore } from '@/stores/market'
import { useRiskStore } from '@/stores/risk'
import { useTradeStore } from '@/stores/trade'
import { useSignalsStore } from '@/stores/signals'
import { useAlertsStore } from '@/stores/alerts'
import { useBrainStore } from '@/stores/brain'
import { quiet } from '@/utils/async'
import { onPush, onResync, type WsTopic } from '@/ws/hub'

const POLL_MS = 10000

/** 账户 / 行情 / 风控三路刷新（失败静默，由界面上的陈旧标记表达）。 */
export function refreshAll(): Promise<unknown> {
  const account = useAccountStore()
  const market = useMarketStore()
  const risk = useRiskStore()
  return Promise.all([quiet(account.refresh), quiet(market.refresh), quiet(risk.refresh)])
}

/** 只恢复受影响域；失败保持required，不能把吞错的REST刷新当作恢复成功。 */
export async function refreshDomains(topics: readonly WsTopic[], signal?: AbortSignal): Promise<void> {
  const jobs = new Map<string, () => Promise<boolean>>()
  const account = useAccountStore()
  if (topics.includes('position')) {
    jobs.set('账户与持仓', account.refresh)
    jobs.set('委托与成交', () => useTradeStore().refresh(account.book || 'SIM'))
    jobs.set('风控', useRiskStore().refresh)
  }
  if (topics.includes('signal')) jobs.set('信号', useSignalsStore().refresh)
  if (topics.includes('alert')) {
    jobs.set('风控', useRiskStore().refresh)
    jobs.set('通知', useAlertsStore().refresh)
  }
  if (topics.includes('market')) jobs.set('市场', useMarketStore().refresh)
  if (topics.includes('brain')) jobs.set('模型', useBrainStore().refresh)
  if (signal?.aborted) throw new Error('同步请求已取消')
  const outcomes = await Promise.all([...jobs].map(async ([name, load]) => ({ name, ready: await load() })))
  if (signal?.aborted) throw new Error('同步请求已取消')
  const failed = outcomes.filter((item) => !item.ready).map((item) => item.name)
  if (failed.length) throw new Error(`${failed.join('、')}权威状态未恢复`)
}

/** 启动轮询与推送联动；返回停止函数。 */
export function startWatchdog(): () => void {
  void refreshAll()
  const timer = setInterval(() => void refreshAll(), POLL_MS)
  const offResync = onResync(['signal', 'position', 'alert', 'market', 'brain'], async (context) => {
    await refreshDomains(context.topics, context.signal)
  })
  const off = onPush(async (msg) => {
    if (['position', 'signal', 'alert', 'market', 'brain'].includes(msg.topic)) {
      await refreshDomains([msg.topic as WsTopic])
    }
  })
  return () => {
    clearInterval(timer)
    off()
    offResync()
  }
}
