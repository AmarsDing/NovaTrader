/**
 * shortcut-registry（W5b；UI 宪法 R-UI-31/37）合同测试：
 * - normalizeCombo：修饰键固定序（ctrl→alt→shift→meta）、单字符主键大写、F9/Enter 原名；
 * - 冲突即抛：同 scope+combo 不同 id 抛 ShortcutConflictError，不同 scope 可同名共存；
 * - 注销即释放：unregister 后可重登记；unregisterScope 面板卸载全量释放且不误伤 global；
 * - resolve：scope 内未命中回退 global；isEditableTarget 命中输入控件 run 直接放行；
 * - bind 未登记 id 抛错、重复登记同 id 抛错（不静默覆盖）。
 */
import { describe, expect, it, vi } from 'vitest'
import {
  ShortcutConflictError,
  comboFromEvent,
  createShortcutRegistry,
  isEditableTarget,
  normalizeCombo,
} from '../shortcut-registry'

function keyEvent(init: Partial<KeyboardEvent> & { key: string }): KeyboardEvent {
  return {
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    metaKey: false,
    target: document.body,
    preventDefault: () => undefined,
    ...init,
  } as unknown as KeyboardEvent
}

describe('normalizeCombo 归一', () => {
  it('修饰键固定序 ctrl→alt→shift→meta，与书写顺序无关', () => {
    expect(normalizeCombo('shift+ctrl+k')).toBe('ctrl+shift+K')
    expect(normalizeCombo('meta+alt+ctrl+f')).toBe('ctrl+alt+meta+F')
    expect(normalizeCombo('control+option+p')).toBe('ctrl+alt+P')
  })

  it('单字符主键大写，功能键原名', () => {
    expect(normalizeCombo('f9')).toBe('F9')
    expect(normalizeCombo('F10')).toBe('F10')
    expect(normalizeCombo('enter')).toBe('Enter')
    expect(normalizeCombo('escape')).toBe('Escape')
  })
})

describe('comboFromEvent / isEditableTarget', () => {
  it('键盘事件转组合键', () => {
    expect(comboFromEvent(keyEvent({ key: 'F9' }))).toBe('F9')
    expect(comboFromEvent(keyEvent({ key: 'k', ctrlKey: true, altKey: true }))).toBe('ctrl+alt+K')
  })

  it('input/textarea/contentEditable 命中可编辑判定', () => {
    const input = document.createElement('input')
    const div = document.createElement('div')
    div.setAttribute('contenteditable', 'true')
    expect(isEditableTarget(input)).toBe(true)
    expect(isEditableTarget(div)).toBe(true)
    expect(isEditableTarget(document.body)).toBe(false)
  })
})

describe('登记与冲突', () => {
  it('同 scope+combo 不同 id 抛 ShortcutConflictError（信息含双方 id）', () => {
    const reg = createShortcutRegistry()
    reg.register({ id: 'trade.buy', combo: 'F9', desc: '买入' })
    expect(() => reg.register({ id: 'strategy.scan', combo: 'F9', desc: '扫描' })).toThrow(
      ShortcutConflictError,
    )
  })

  it('不同 scope 同名组合键共存；归一后冲突也成立（shift+ctrl+k === ctrl+shift+k）', () => {
    const reg = createShortcutRegistry()
    reg.register({ id: 'trade.buy', combo: 'F9', desc: '买入' })
    reg.register({ id: 'pool.jump', combo: 'F9', desc: '跳转', scope: 'pool' })
    expect(() => reg.register({ id: 'x.a', combo: 'ctrl+shift+k', desc: 'x' })).not.toThrow()
    expect(() => reg.register({ id: 'x.b', combo: 'shift+ctrl+k', desc: 'x' })).toThrow(ShortcutConflictError)
  })

  it('同 id 重复登记抛错（不静默覆盖）', () => {
    const reg = createShortcutRegistry()
    reg.register({ id: 'a.1', combo: 'F1', desc: 'x' })
    expect(() => reg.register({ id: 'a.1', combo: 'F2', desc: 'x' })).toThrow(/重复登记/)
  })

  it('bind 未登记 id 抛错', () => {
    const reg = createShortcutRegistry()
    expect(() => reg.bind('ghost', vi.fn())).toThrow(/未登记/)
  })
})

describe('resolve / run', () => {
  it('scope 内命中优先，未命中回退 global', () => {
    const reg = createShortcutRegistry()
    reg.register({ id: 'global.refresh', combo: 'F5', desc: '刷新' })
    reg.register({ id: 'pool.jump', combo: 'F5', desc: '池跳转', scope: 'pool' })
    expect(reg.resolve(keyEvent({ key: 'F5' }), 'pool')).toBe('pool.jump')
    expect(reg.resolve(keyEvent({ key: 'F5' }), 'other')).toBe('global.refresh')
    expect(reg.resolve(keyEvent({ key: 'F5' }))).toBe('global.refresh')
  })

  it('run 命中执行处理器并返回 true；未命中返回 false', () => {
    const reg = createShortcutRegistry()
    const fn = vi.fn()
    reg.register({ id: 'trade.buy', combo: 'F9', desc: '买入' })
    reg.bind('trade.buy', fn)
    expect(reg.run(keyEvent({ key: 'F9' }))).toBe(true)
    expect(fn).toHaveBeenCalledTimes(1)
    expect(reg.run(keyEvent({ key: 'F1' }))).toBe(false)
  })

  it('run 在可编辑目标上放行（不劫持打字）', () => {
    const reg = createShortcutRegistry()
    const fn = vi.fn()
    reg.register({ id: 'trade.buy', combo: 'F9', desc: '买入' })
    reg.bind('trade.buy', fn)
    const input = document.createElement('input')
    expect(reg.run(keyEvent({ key: 'F9', target: input }))).toBe(false)
    expect(fn).not.toHaveBeenCalled()
  })
})

describe('注销释放', () => {
  it('unregister 后组合键释放，可重新登记', () => {
    const reg = createShortcutRegistry()
    reg.register({ id: 'trade.buy', combo: 'F9', desc: '买入' })
    reg.unregister('trade.buy')
    // 释放后可重新登记，新登记接管组合键。
    expect(() => reg.register({ id: 'strategy.scan', combo: 'F9', desc: '扫描' })).not.toThrow()
    expect(reg.resolve(keyEvent({ key: 'F9' }))).toBe('strategy.scan')
  })

  it('unregisterScope 释放面板全部键且不误伤 global', () => {
    const reg = createShortcutRegistry()
    reg.register({ id: 'global.refresh', combo: 'F5', desc: '刷新' })
    reg.register({ id: 'pool.jump', combo: 'F9', desc: '跳转', scope: 'pool' })
    reg.register({ id: 'pool.mark', combo: 'F10', desc: '标记', scope: 'pool' })
    reg.unregisterScope('pool')
    expect(reg.resolve(keyEvent({ key: 'F9' }), 'pool')).toBeNull()
    expect(reg.resolve(keyEvent({ key: 'F10' }), 'pool')).toBeNull()
    expect(reg.resolve(keyEvent({ key: 'F5' }))).toBe('global.refresh')
    // pool 槽位已空，F9 可在 global 重新登记。
    expect(() => reg.register({ id: 'trade.buy', combo: 'F9', desc: '买入' })).not.toThrow()
  })

  it('解绑函数只解除自己持有的处理器', () => {
    const reg = createShortcutRegistry()
    const fn1 = vi.fn()
    const fn2 = vi.fn()
    reg.register({ id: 'a.1', combo: 'F1', desc: 'x' })
    const un1 = reg.bind('a.1', fn1)
    reg.bind('a.1', fn2)
    un1()
    reg.run(keyEvent({ key: 'F1' }))
    expect(fn1).not.toHaveBeenCalled()
    expect(fn2).toHaveBeenCalledTimes(1)
  })

  it('list 返回拷贝（外部改不动内部状态）', () => {
    const reg = createShortcutRegistry()
    reg.register({ id: 'a.1', combo: 'F1', desc: 'x' })
    const list = reg.list()
    list[0].id = 'hacked'
    expect(reg.list()[0].id).toBe('a.1')
  })
})
