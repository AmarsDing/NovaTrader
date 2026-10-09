/** 系统通知：仅桌面壳生效；浏览器开发态静默跳过，页面内提示兜底。 */
import { inTauri } from './tauri'

export async function deskNotify(title: unknown, body?: unknown): Promise<boolean> {
  if (!inTauri() || !title) return false
  try {
    const { isPermissionGranted, requestPermission, sendNotification } = await import(
      '@tauri-apps/plugin-notification'
    )
    let allowed = await isPermissionGranted()
    if (!allowed) {
      const perm = await requestPermission()
      allowed = perm === 'granted'
    }
    if (!allowed) return false
    sendNotification({ title: String(title), body: body ? String(body) : '' })
    return true
  } catch {
    return false
  }
}
