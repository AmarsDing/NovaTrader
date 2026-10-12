/**
 * 未读通知计数：订阅 alert 主题累计未读，StatusBar 角标读它。
 * 面板打开时 markRead 清零；start 幂等，由 StatusBar 首次挂载拉起。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { onPush, type WsPushMessage } from '@/ws/hub'

let offPush: (() => void) | null = null

export const useNotifyStore = defineStore('notify', () => {
  const unread = ref(0)
  const lastTitle = ref('')
  const lastAt = ref('')
  const started = ref(false)

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

  return { unread, lastTitle, lastAt, started, start, markRead }
})
