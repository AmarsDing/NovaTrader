/** 桌面壳检测与窗口操作。浏览器开发态一律静默 no-op。 */

export function inTauri(): boolean {
  return typeof window !== 'undefined' && !!window.__TAURI_INTERNALS__
}

declare global {
  interface Window {
    __TAURI_INTERNALS__?: unknown
  }
}

export async function showMainWindow(): Promise<void> {
  if (!inTauri()) return
  const { getCurrentWindow } = await import('@tauri-apps/api/window')
  const win = getCurrentWindow()
  await win.unminimize()
  await win.show()
  await win.setFocus()
}
