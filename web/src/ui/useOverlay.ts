/**
 * 弹层 z 栈与 Esc 链。
 * - open() 入栈并返回当前 zIndex（80 起，每层 +10）；
 * - 全局 keydown 单例，Esc 只派发给栈顶；
 * - 组件卸载自动出栈。
 */
import { onScopeDispose, ref, type Ref } from 'vue'

interface OverlayEntry {
  id: number
  onEsc?: () => void
}

const stack: OverlayEntry[] = []
let seq = 0
let bound = false

function ensureBound() {
  if (bound || typeof window === 'undefined') return
  bound = true
  window.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return
    const top = stack[stack.length - 1]
    top?.onEsc?.()
  })
}

export interface OverlayHandle {
  visible: Ref<boolean>
  zIndex: Ref<number>
  open: () => void
  close: () => void
}

export function useOverlay(onEsc?: () => void): OverlayHandle {
  ensureBound()
  const visible = ref(false)
  const zIndex = ref(80)
  let id = 0

  function open() {
    id = ++seq
    stack.push({ id, onEsc })
    visible.value = true
    zIndex.value = 80 + stack.length * 10
  }

  function close() {
    const index = stack.findIndex((entry) => entry.id === id)
    if (index >= 0) stack.splice(index, 1)
    visible.value = false
  }

  onScopeDispose(() => {
    const index = stack.findIndex((entry) => entry.id === id)
    if (index >= 0) stack.splice(index, 1)
  })

  return { visible, zIndex, open, close }
}
