/** 应用内 toast。薄封装 alerts store 的 toasts，组件只负责渲染。 */
import { useAlertsStore } from '@/stores/alerts'
import type { ToastItem } from '@/stores/alerts'

export function useToast() {
  const alerts = useAlertsStore()

  function push(kind: ToastItem['kind'], title: string, body = '', ttlMs = 4200) {
    const id = alerts.pushToast(kind, title, body)
    if (ttlMs > 0) setTimeout(() => alerts.dismissToast(id), ttlMs)
    return id
  }

  return {
    info: (title: string, body?: string) => push('info', title, body),
    success: (title: string, body?: string) => push('success', title, body),
    warn: (title: string, body?: string) => push('warn', title, body, 6000),
    error: (title: string, body?: string) => push('error', title, body, 8000),
  }
}
