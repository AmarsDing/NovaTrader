/**
 * 系统通知三 sink 分流（G3 告警活跃感知）：
 *   应用活跃（窗口聚焦且未 hidden）→ 仅应用内 toast，不打扰系统响铃；
 *   应用不活跃（最小化/失焦/锁屏）→ Tauri 原生通知；
 *   浏览器开发态（无桌面壳）→ 一律应用内 toast 兜底。
 * 活跃判定直读 document.visibilityState / hasFocus，与 settings store 的
 * pageVisible（visibilitychange 同步）同源，不再订阅第二份。
 */
import { useAlertsStore, type ToastItem } from '@/stores/alerts'
import { inTauri } from './tauri'

export type NotifySink = 'inapp' | 'native'

/** 活跃 → inapp；不活跃且有壳 → native；不活跃且无壳 → inapp（页面内提示兜底）。 */
export function pickNotifySink(active: boolean, tauri: boolean): NotifySink {
  if (active) return 'inapp'
  return tauri ? 'native' : 'inapp'
}

/** 推送 severity/kind 文本 → toast 色调。红涨绿跌无关，纯告警级别语义。 */
export function toastKindOf(raw: string): ToastItem['kind'] {
  const key = raw.toLowerCase()
  if (key.includes('crit') || key.includes('fatal') || key.includes('error') || key.includes('danger')) return 'error'
  if (key.includes('warn') || key.includes('alert')) return 'warn'
  if (key.includes('success') || key.includes('up') || key.includes('ok')) return 'success'
  return 'info'
}

function isAppFocused(): boolean {
  if (typeof document === 'undefined') return false
  return document.visibilityState !== 'hidden' && document.hasFocus()
}

const TOAST_TTL: Record<ToastItem['kind'], number> = {
  info: 4200,
  success: 4200,
  warn: 6000,
  error: 8000,
}

async function nativeNotify(title: string, body: string): Promise<boolean> {
  try {
    const { isPermissionGranted, requestPermission, sendNotification } = await import('@tauri-apps/plugin-notification')
    let allowed = await isPermissionGranted()
    if (!allowed) {
      const perm = await requestPermission()
      allowed = perm === 'granted'
    }
    if (!allowed) return false
    sendNotification({ title, body })
    return true
  } catch {
    return false
  }
}

/**
 * 通知入口（hub handlePush 唯一直调应用侧 API）：按活跃感知分流到单一 sink。
 * kind 传后端 severity/kind 原文，仅影响 in-app toast 色调，不进原生正文。
 */
export async function deskNotify(title: unknown, body?: unknown, kind?: unknown): Promise<boolean> {
  const text = title ? String(title).trim() : ''
  if (!text) return false
  const textBody = body ? String(body) : ''
  if (pickNotifySink(isAppFocused(), inTauri()) === 'native') return nativeNotify(text, textBody)
  try {
    const alerts = useAlertsStore()
    const toastKind = toastKindOf(kind ? String(kind) : '')
    const id = alerts.pushToast(toastKind, text, textBody)
    setTimeout(() => alerts.dismissToast(id), TOAST_TTL[toastKind])
    return true
  } catch {
    return false
  }
}
