/**
 * 通知中心（M10）+ 应用内 toast 桥。
 * toast 由 WS push / 操作结果驱动；P2 的 NtToast 组件消费 toasts。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { listMessages, type NotifyMessage } from '@/api/notify'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'

export interface ToastItem {
  id: number
  kind: 'info' | 'success' | 'warn' | 'error'
  title: string
  body: string
  at: number
}

let toastSeq = 0

export const useAlertsStore = defineStore('alerts', () => {
  const messages = ref<NotifyMessage[]>([])
  const loading = ref(false)
  const ready = ref(false)
  const toasts = ref<ToastItem[]>([])

  const unreadCount = computed(() => messages.value.filter((m) => !m.read).length)

  const refresh = createRefreshQueue(async () => {
    loading.value = true
    try {
      const list = await quiet(() => listMessages({ limit: 100 }))
      if (list) messages.value = list
      ready.value = list != null
      return ready.value
    } finally {
      loading.value = false
    }
  })

  function pushToast(kind: ToastItem['kind'], title: string, body = ''): number {
    const id = ++toastSeq
    toasts.value.push({ id, kind, title, body, at: Date.now() })
    return id
  }

  function dismissToast(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  return { messages, loading, ready, toasts, unreadCount, refresh, pushToast, dismissToast }
})
