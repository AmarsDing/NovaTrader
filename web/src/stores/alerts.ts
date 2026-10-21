/**
 * 通知中心（M10）+ 应用内 toast 桥。
 * toast 由 WS push / 操作结果驱动；P2 的 NtToast 组件消费 toasts。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { listMessages, type NotifyMessage } from '@/api/notify'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'
import { serverNow } from '@/utils/server-clock'

export interface ToastItem {
  id: number
  kind: 'info' | 'success' | 'warn' | 'error'
  title: string
  body: string
  at: number
  /** P2：同因连发计数（bumpToast 累加）；首条无该字段，消费侧按 1 读。 */
  count?: number
}

let toastSeq = 0

/** G3：应用内 toast 栈硬上限，高频告警时挤掉最旧，防屏幕右上一片糊。 */
export const MAX_TOASTS = 5

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
    /* P2-10：toast 时刻与底栏时段钟同走服务端钟（未校准回退本地）。 */
    toasts.value.push({ id, kind, title, body, at: serverNow() })
    if (toasts.value.length > MAX_TOASTS) toasts.value.splice(0, toasts.value.length - MAX_TOASTS)
    return id
  }

  function dismissToast(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  return { messages, loading, ready, toasts, unreadCount, refresh, pushToast, dismissToast }
})

/**
 * P2：同因 toast 计数命中（bump）——新帧与前条同因时不新增条目，计数 +1 并刷新时刻；
 * 返回是否命中。不新增条目、不改 MAX_TOASTS、不改 pushToast 行为，供推送汇聚方在
 * pushToast 前调用（配合 alert-levels 的 causeKey/createCauseAggregator 判定同因）。
 * id 取 pushToast 返回值；容忍字符串形入参（帧字段串化场景）。
 */
export function bumpToast(id: number | string): boolean {
  const store = useAlertsStore()
  const nid = typeof id === 'string' ? Number(id) : id
  const t = store.toasts.find((x) => x.id === nid)
  if (!t) return false
  t.count = (t.count || 1) + 1
  /* 与 pushToast 同走服务端钟（未校准回退本地），防计数刷新把时刻打回本地漂移。 */
  t.at = serverNow()
  return true
}
