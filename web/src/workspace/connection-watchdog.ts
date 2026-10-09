/**
 * 连接看门狗：原 MainLayout 的刷新逻辑（10s 轮询 + 推送触发）。
 * 抽出来给工作台用，也可被 CommandPalette 的「刷新」动作直接调用。
 */
import { useAccountStore } from '@/stores/account'
import { useMarketStore } from '@/stores/market'
import { useRiskStore } from '@/stores/risk'
import { quiet } from '@/utils/async'
import { onPush } from '@/ws/hub'

const POLL_MS = 10000

/** 账户 / 行情 / 风控三路刷新（失败静默，由界面上的陈旧标记表达）。 */
export function refreshAll(): Promise<unknown> {
  const account = useAccountStore()
  const market = useMarketStore()
  const risk = useRiskStore()
  return Promise.all([quiet(account.refresh), quiet(market.refresh), quiet(risk.refresh)])
}

/** 启动轮询与推送联动；返回停止函数。 */
export function startWatchdog(): () => void {
  void refreshAll()
  const timer = setInterval(() => void refreshAll(), POLL_MS)
  const off = onPush((msg) => {
    if (msg.topic === 'resume' || msg.topic === 'position' || msg.topic === 'signal') void refreshAll()
  })
  return () => {
    clearInterval(timer)
    off()
  }
}
