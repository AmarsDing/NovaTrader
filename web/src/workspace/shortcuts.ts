/**
 * 工作台热键表（输入态保护：焦点在输入框/下拉/文本域/富文本时不触发）：
 * - B / S：打开交易面板并预填买/卖（order-ticket 入口）；
 * - Shift+T：打开交易面板；
 * - Shift+K：打开 Kill Switch 对话框（只读焦点，触发仍要二次确认）；
 * - Shift+M：打开涨停梯队；Shift+R：打开风控链；
 * - 1..9：切换 dock 内第 N 个面板（按当前激活组标签顺序）。
 * Ctrl 组合键在 WorkbenchView（Ctrl+K / Ctrl+Shift+P / Ctrl+Shift+K），这里只管裸键与 Shift 组合。
 */
import { getWorkbench } from './inject'
import { getPanel } from './panel-registry'
import type { DockviewApi } from 'dockview-core'

export interface HotkeyOptions {
  dock: () => DockviewApi | null
  openKillSwitch: () => void
}

function isEditable(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (el.isContentEditable) return true
  return !!el.closest?.('[contenteditable="true"]')
}

export function startHotkeys(opts: HotkeyOptions): () => void {
  function activateNth(index: number) {
    const api = opts.dock()
    const group = api?.activeGroup
    const panel = group?.panels[index]
    panel?.api.setActive()
  }

  function orderTicket(side: 'buy' | 'sell') {
    if (!getPanel('trade')) return
    getWorkbench()?.openPanel('trade', side === 'buy' ? { side: 'buy' } : { side: 'sell' })
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.ctrlKey || event.metaKey || event.altKey) return
    if (isEditable(event.target)) return
    const key = event.key

    if (!event.shiftKey && (key === 'b' || key === 'B')) {
      event.preventDefault()
      orderTicket('buy')
      return
    }
    if (!event.shiftKey && (key === 's' || key === 'S')) {
      event.preventDefault()
      orderTicket('sell')
      return
    }
    if (!event.shiftKey && key >= '1' && key <= '9') {
      event.preventDefault()
      activateNth(Number(key) - 1)
      return
    }
    if (event.shiftKey) {
      switch (key) {
        case 'T':
          event.preventDefault()
          getWorkbench()?.openPanel('trade')
          return
        case 'K':
          event.preventDefault()
          opts.openKillSwitch()
          return
        case 'M':
          event.preventDefault()
          getWorkbench()?.openPanel('limit-ladder')
          return
        case 'R':
          event.preventDefault()
          getWorkbench()?.openPanel('risk-chain')
          return
        default:
          return
      }
    }
  }

  window.addEventListener('keydown', onKeydown)
  return () => window.removeEventListener('keydown', onKeydown)
}
