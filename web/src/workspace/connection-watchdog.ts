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
/** 隐藏页轮询降频：WS 推送（含告警）不受影响，REST 兜底降级即可。 */
const HIDDEN_POLL_MS = 60000

export interface WatchdogOptions {
  pollVisibleMs?: number
  pollHiddenMs?: number
  isHidden?: () => boolean
}

/** 账户 / 行情 / 风控三路刷新（失败静默，由界面上的陈旧标记表达）。 */
export function refreshAll(): Promise<unknown> {
  const account = useAccountStore()
  const market = useMarketStore()
  const risk = useRiskStore()
  return Promise.all([quiet(account.refresh), quiet(market.refresh), quiet(risk.refresh)])
}

async function runJobs(jobs: Map<string, () => Promise<boolean>>, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) throw new Error('同步请求已取消')
  const outcomes = await Promise.all([...jobs].map(async ([name, load]) => ({ name, ready: await load() })))
  if (signal?.aborted) throw new Error('同步请求已取消')
  const failed = outcomes.filter((item) => !item.ready).map((item) => item.name)
  if (failed.length) throw new Error(`${failed.join('、')}权威状态未恢复`)
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
  await runJobs(jobs, signal)
}

/** 启动轮询与推送联动；返回停止函数。隐藏页 REST 轮询降频，推送与恢复链路不变。 */
export function startWatchdog(options: WatchdogOptions = {}): () => void {
  const visibleMs = options.pollVisibleMs ?? POLL_MS
  const hiddenMs = options.pollHiddenMs ?? HIDDEN_POLL_MS
  const isHidden = options.isHidden ?? (() => typeof document !== 'undefined' && document.hidden)
  void refreshAll()
  let timer = setInterval(() => void refreshAll(), isHidden() ? hiddenMs : visibleMs)
  const onVisibility = () => {
    clearInterval(timer)
    timer = setInterval(() => void refreshAll(), isHidden() ? hiddenMs : visibleMs)
  }
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility)
  const offResync = onResync(['signal', 'position', 'alert', 'market', 'brain'], async (context) => {
    await refreshDomains(context.topics, context.signal)
  })
  const off = onPush(async (msg) => {
    if (msg.topic === 'position') {
      const account = useAccountStore()
      // 账户快照推送=完整实体态（entity_version 单调）：能增量就增量，免账户/持仓 REST；
      // 订单/成交与风控不在该载荷里，保留语义按权威刷新；解析不了才整域回退。
      if (account.applyPush(msg.data, msg.metadata)) {
        const jobs = new Map<string, () => Promise<boolean>>()
        jobs.set('委托与成交', () => useTradeStore().refresh(account.book || 'SIM'))
        jobs.set('风控', useRiskStore().refresh)
        await runJobs(jobs)
        return
      }
      await refreshDomains(['position'])
      return
    }
    if (['signal', 'alert', 'market', 'brain'].includes(msg.topic)) {
      await refreshDomains([msg.topic as WsTopic])
    }
  })
  return () => {
    clearInterval(timer)
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility)
    off()
    offResync()
  }
}
