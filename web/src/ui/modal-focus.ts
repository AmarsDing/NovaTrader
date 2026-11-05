/**
 * 弹层焦点管理（监控与通知共用的最小实现）：
 * - 打开：记录触发元素并把焦点送进层内（首个可聚焦元素，缺省落到 dialog 本身 tabindex=-1）；
 * - Tab/Shift+Tab 在层内循环，背景不可经键盘进入；
 * - Esc 只关当前层（keydown 捕获阶段 stopPropagation，避免与 useOverlay 的 window 冒泡链重复触发）；
 * - 关闭：焦点归位到触发元素；触发元素已被合并移除（如通知行被新消息挤出）时由调用方给 fallback。
 */
import { onBeforeUnmount } from 'vue'

const FOCUSABLE =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'

export interface ModalFocusOptions {
  /** 打开后首选焦点元素；缺省取层内首个可聚焦元素。 */
  initial?: HTMLElement | null
  /** 关闭后焦点归位元素；缺省为打开前的 document.activeElement。 */
  fallback?: HTMLElement | null
  /** 是否响应 Esc；默认 true。 */
  esc?: boolean
}

export function createModalFocus() {
  let dialog: HTMLElement | null = null
  let restoreEl: HTMLElement | null = null
  let keyHandler: ((event: KeyboardEvent) => void) | null = null

  function visibleFocusables(root: HTMLElement): HTMLElement[] {
    return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
      (el) => el.tabIndex >= 0 && (el.offsetWidth > 0 || el.offsetHeight > 0 || el === document.activeElement),
    )
  }

  function deactivate(restore = true) {
    if (keyHandler) document.removeEventListener('keydown', keyHandler, true)
    keyHandler = null
    dialog = null
    if (restore && restoreEl?.isConnected) {
      const target = restoreEl
      // 归位不滚动页面：列表滚动位置由阅读锚点单独维护。
      requestAnimationFrame(() => target.focus({ preventScroll: true }))
    }
    restoreEl = null
  }

  function activate(next: HTMLElement | null, onClose: () => void, options: ModalFocusOptions = {}) {
    deactivate(false)
    if (!next || typeof document === 'undefined') return
    dialog = next
    restoreEl = options.fallback ?? ((document.activeElement as HTMLElement | null) || null)
    const initial = options.initial ?? visibleFocusables(next)[0] ?? next
    initial.focus({ preventScroll: true })

    keyHandler = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && (options.esc ?? true)) {
        // 只关顶层：捕获阶段截断，useOverlay 的 window 冒泡监听不会二次触发。
        event.preventDefault()
        event.stopPropagation()
        onClose()
        return
      }
      if (event.key !== 'Tab' || !dialog) return
      const list = visibleFocusables(dialog)
      if (!list.length) {
        event.preventDefault()
        dialog.focus({ preventScroll: true })
        return
      }
      const first = list[0]
      const last = list[list.length - 1]
      const active = document.activeElement as HTMLElement | null
      if (event.shiftKey && (active === first || active === dialog || !dialog.contains(active))) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && (active === last || !dialog.contains(active))) {
        event.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', keyHandler, true)
  }

  onBeforeUnmount(() => deactivate(false))

  return { activate, deactivate }
}
