/**
 * G6 布局撤销/重做栈单测：编辑器语义（新切换清空重做、容量 20、空栈静默）。
 * 栈存 dockview toJSON 快照，本测试用任意对象替身——模块对快照内容无感知。
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { historySizes, recordLayout, redoLayout, resetHistory, undoLayout } from '../layout-history'

beforeEach(() => resetHistory())

describe('record / undo / redo 语义', () => {
  it('空栈撤销/重做返回 null 且不改栈', () => {
    expect(undoLayout('current')).toBeNull()
    expect(redoLayout('current')).toBeNull()
    expect(historySizes()).toEqual({ undo: 0, redo: 0 })
  })

  it('切换前记录 → 撤销回到上一版，当前进重做栈', () => {
    recordLayout('v1')
    recordLayout('v2')
    expect(historySizes()).toEqual({ undo: 2, redo: 0 })
    expect(undoLayout('v3')).toBe('v2')
    expect(historySizes()).toEqual({ undo: 1, redo: 1 })
    expect(undoLayout('v2')).toBe('v1')
    expect(historySizes()).toEqual({ undo: 0, redo: 2 })
  })

  it('撤销后重做还原链路', () => {
    recordLayout('v1')
    recordLayout('v2')
    undoLayout('v3')
    expect(redoLayout('v2')).toBe('v3')
    expect(historySizes()).toEqual({ undo: 2, redo: 0 })
  })

  it('新切换清空重做栈（分支语义）', () => {
    recordLayout('v1')
    undoLayout('v2')
    expect(historySizes().redo).toBe(1)
    recordLayout('v2')
    expect(historySizes()).toEqual({ undo: 1, redo: 0 })
    expect(redoLayout('v3')).toBeNull()
  })

  it('null 快照不入栈（启动空态等场景）', () => {
    recordLayout(null)
    recordLayout(undefined)
    expect(historySizes()).toEqual({ undo: 0, redo: 0 })
  })

  it('容量钉死 20：最老快照被挤出', () => {
    for (let i = 0; i < 25; i += 1) recordLayout(`v${i}`)
    expect(historySizes().undo).toBe(20)
    const drained: unknown[] = []
    let item = undoLayout(`current-${drained.length}`)
    while (item !== null) {
      drained.push(item)
      item = undoLayout(item)
    }
    expect(historySizes().undo).toBe(0)
    expect(drained[0]).toBe('v24')
    expect(drained.at(-1)).toBe('v5')
  })
})
