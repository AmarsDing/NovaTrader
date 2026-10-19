/**
 * 弹层 z 栈与 Esc 链。
 * - open() 同 handle 先去重再入栈（杜绝重入孤儿与 zIndex 通胀），zIndex 80 起每层 +10；
 * - 全局 keydown 单例，Esc 只派发给栈顶；栈非空即 preventDefault，栈外 window 监听
 *   （如 WorkbenchView 的 config 分支）凭 defaultPrevented 让位，一按只关一层；
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
    if (!top) return
    event.preventDefault()
    top.onEsc?.()
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
    close()
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
