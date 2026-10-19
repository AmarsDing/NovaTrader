/**
 * useOverlay 栈纪律（G2 审查闭环回写）：
 * - 同 handle 重复 open 先去重，栈不胀、zIndex 不虚抬；
 * - Esc 命中栈顶即 preventDefault，栈外 window 监听凭 defaultPrevented 让位（一按一层）；
 * - 栈空时 Esc 不吞事件（浏览器与其他层照常）。
 */
import { effectScope } from 'vue'
import { describe, expect, it } from 'vitest'
import { useOverlay } from '../useOverlay'

function esc(): KeyboardEvent {
  const event = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true })
  window.dispatchEvent(event)
  return event
}

describe('useOverlay 栈纪律', () => {
  it('同 handle 重复 open 不产生孤儿：zIndex 不虚抬，一次关闭即净栈', () => {
    const scope = effectScope()
    const handle = scope.run(() => useOverlay())!
    handle.open()
    handle.open()
    handle.open()
    // 去重生效：栈内仍只 1 条 → zIndex = 80 + 1*10
    expect(handle.zIndex.value).toBe(90)
    handle.close()
    // 净栈后 Esc 落空且不吞事件
    const event = esc()
    expect(event.defaultPrevented).toBe(false)
    scope.stop()
  })

  it('栈顶优先、一按一层：Esc 命中即 preventDefault，净栈后放行', () => {
    const scope = effectScope()
    let a!: ReturnType<typeof useOverlay>
    let b!: ReturnType<typeof useOverlay>
    scope.run(() => {
      a = useOverlay(() => a.close())
      b = useOverlay(() => b.close())
    })
    a.open()
    b.open()
    const first = esc()
    expect(first.defaultPrevented).toBe(true)
    expect(b.visible.value).toBe(false)
    expect(a.visible.value).toBe(true)
    const second = esc()
    expect(second.defaultPrevented).toBe(true)
    expect(a.visible.value).toBe(false)
    const third = esc()
    expect(third.defaultPrevented).toBe(false)
    scope.stop()
  })

  it('onEsc 回调按栈顶派发；非 Escape 键不消费', () => {
    const scope = effectScope()
    let count = 0
    const handle = scope.run(() => useOverlay(() => (count += 1)))!
    handle.open()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', cancelable: true }))
    expect(count).toBe(0)
    esc()
    expect(count).toBe(1)
    handle.close()
    scope.stop()
  })
})
