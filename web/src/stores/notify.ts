/**
 * 未读通知计数：订阅 alert 主题累计未读，StatusBar 角标读它。
 * 面板打开时 markRead 清零；start 幂等，由 StatusBar 首次挂载拉起。
 * P2 告警漏斗落账（方案 §8.6）：feed 给 notify-center 消费、attention 给 PanelTab 角标消费；
 * 只存账不弹窗（弹窗归 utils/notify 漏斗），既有 alert 主题 unread/lastTitle 逻辑不动。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { onPush, type WsPushMessage } from '@/ws/hub'
import type { AlertLevel } from '@/ui/alert-levels'

/** feed 条目：漏斗每一跳的留痕（含静默与冷却），同因 10s 窗内合并计数。 */
export interface FeedEntry {
  id: number
  level: AlertLevel
  kind?: string
  title: string
  body?: string
  symbol?: string
  /** 同因累计次数（首条 1；窗内再发 +1 或按聚合器 count 对齐）。 */
  count: number
  causeKey?: string
  at: number
  /** 静默时段命中：只存账，未打扰用户。 */
  silenced?: boolean
}

/** 页签角标状态：L2 静态章 / L3 脉冲章；面板激活即 clearAttention 清零。 */
export interface TabAttention {
  level: 'L2' | 'L3'
  count: number
  at: number
}

/** feed 硬上限（条）：高频告警挤掉最旧，notify-center 不滚到天荒地老。 */
const FEED_CAP = 200
/** feed 同因合并窗（与 alert-levels 聚合器同窗）。 */
const FEED_MERGE_MS = 10_000

let offPush: (() => void) | null = null
let feedSeq = 0

export const useNotifyStore = defineStore('notify', () => {
  const unread = ref(0)
  const lastTitle = ref('')
  const lastAt = ref('')
  const started = ref(false)
  const feed = ref<FeedEntry[]>([])
  const attention = ref<Record<string, TabAttention>>({})

  function start() {
    if (offPush) return
    // alert 属常驻主题（BASE_TOPICS），onPush 即可收到，无需再 subscribe。
    offPush = onPush((msg: WsPushMessage) => {
      if (msg.topic !== 'alert') return
      unread.value += 1
      const data = msg.data && typeof msg.data === 'object' ? (msg.data as Record<string, unknown>) : {}
      lastTitle.value = String(data.title || data.subject || data.kind || '告警')
      lastAt.value = new Date().toISOString()
    })
    started.value = true
  }

  function markRead() {
    unread.value = 0
  }

  /** 漏斗落账：同因 10s 内→既有条目 count +1（或对齐聚合器 count）并刷新时刻；否则 unshift 并截断到上限。 */
  function ingestFeed(entry: Omit<FeedEntry, 'id' | 'count'> & { count?: number }): FeedEntry {
    const hit = entry.causeKey ? feed.value.find((f) => f.causeKey === entry.causeKey) : undefined
    if (hit && entry.at - hit.at <= FEED_MERGE_MS) {
      hit.count = entry.count ?? hit.count + 1
      hit.at = entry.at
      return hit
    }
    const item: FeedEntry = { id: ++feedSeq, count: 1, ...entry }
    feed.value.unshift(item)
    if (feed.value.length > FEED_CAP) feed.value.splice(FEED_CAP)
    return item
  }

  /** 抬面板角标：count +1；L3 覆盖 L2（已 L3 不被低级降级）。 */
  function pulseAttention(panelId: string, level: 'L2' | 'L3') {
    const id = panelId.trim()
    if (!id) return
    const cur = attention.value[id]
    attention.value[id] = { level: cur?.level === 'L3' ? 'L3' : level, count: (cur?.count ?? 0) + 1, at: Date.now() }
  }

  function clearAttention(panelId: string) {
    delete attention.value[panelId]
  }

  return { unread, lastTitle, lastAt, started, start, markRead, feed, attention, ingestFeed, pulseAttention, clearAttention }
})
