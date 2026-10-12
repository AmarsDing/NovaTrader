/**
 * layout store 单测（P6）：布局持久化 roundtrip、版本迁移兜底、损坏 JSON 容错、预设增删。
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ARCHIVED_LAYOUT_NAME, LAYOUT_STORAGE_VERSION, useLayoutStore } from '@/stores/layout'

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
})

describe('layout 持久化', () => {
  it('save/load roundtrip（带版本号信封）', () => {
    const store = useLayoutStore()
    const layout = { grid: { views: [{ id: 'kline' }] }, panels: { kline: {} } }
    store.save('acc1', layout)

    const raw = localStorage.getItem(store.storageKey('acc1'))
    expect(raw).toBeTruthy()
    expect(JSON.parse(raw as string)).toEqual({ version: LAYOUT_STORAGE_VERSION, layout })
    expect(store.load('acc1')).toEqual(layout)
  })

  it('无存储返回 null', () => {
    expect(useLayoutStore().load('nobody')).toBeNull()
  })

  it('账号隔离：不同 accountId 互不可见', () => {
    const store = useLayoutStore()
    store.save('acc1', { a: 1 })
    expect(store.load('acc2')).toBeNull()
  })

  it('空 accountId 落 local 键', () => {
    const store = useLayoutStore()
    expect(store.storageKey('')).toBe('nt.layout.v1.local')
    store.save('', { b: 2 })
    expect(store.load('')).toEqual({ b: 2 })
  })

  it('未知版本和未来版本丢弃，且不写进模板', () => {
    const store = useLayoutStore()
    localStorage.setItem(
      store.storageKey('acc1'),
      JSON.stringify({ version: 0, layout: { old: true } }),
    )
    expect(store.load('acc1')).toBeNull()

    localStorage.setItem(
      store.storageKey('acc1'),
      JSON.stringify({ version: 999, layout: { future: true } }),
    )
    expect(store.load('acc1')).toBeNull()
    expect(store.presets[ARCHIVED_LAYOUT_NAME]).toBeUndefined()
  })

  it('低版本当前排列收成模板，屏幕仍回默认', () => {
    const store = useLayoutStore()
    store.savePreset('盯盘', { grid: 1 })
    localStorage.setItem(
      store.storageKey('acc1'),
      JSON.stringify({ version: 2, layout: { panels: ['mine'] } }),
    )
    expect(store.load('acc1')).toBeNull()
    expect(store.presets[ARCHIVED_LAYOUT_NAME]).toEqual({ panels: ['mine'] })
    expect(store.presets['盯盘']).toEqual({ grid: 1 })

    localStorage.setItem(
      store.storageKey('acc1'),
      JSON.stringify({ version: 2, layout: { panels: ['later'] } }),
    )
    expect(store.load('acc1')).toBeNull()
    expect(store.presets[ARCHIVED_LAYOUT_NAME]).toEqual({ panels: ['mine'] })
  })

  it('损坏 JSON / 异形信封不抛错', () => {
    const store = useLayoutStore()
    localStorage.setItem(store.storageKey('acc1'), '{{{ not json')
    expect(store.load('acc1')).toBeNull()

    localStorage.setItem(store.storageKey('acc1'), JSON.stringify('plain string'))
    expect(store.load('acc1')).toBeNull()

    localStorage.setItem(store.storageKey('acc1'), JSON.stringify({ version: LAYOUT_STORAGE_VERSION }))
    expect(store.load('acc1')).toBeNull()
  })

  it('clear 清掉布局', () => {
    const store = useLayoutStore()
    store.save('acc1', { a: 1 })
    store.clear('acc1')
    expect(store.load('acc1')).toBeNull()
  })
})

describe('layout 预设', () => {
  it('savePreset/removePreset 持久化到 localStorage', () => {
    const store = useLayoutStore()
    store.savePreset('盯盘', { grid: 1 })
    store.savePreset('交易', { grid: 2 })
    expect(store.presets).toEqual({ 盯盘: { grid: 1 }, 交易: { grid: 2 } })
    expect(JSON.parse(localStorage.getItem('nt.layout.presets.v1') || '{}')).toEqual(store.presets)

    store.removePreset('盯盘')
    expect(store.presets).toEqual({ 交易: { grid: 2 } })
  })

  it('loadPresets 从存储恢复（新 store 实例）', () => {
    const first = useLayoutStore()
    first.savePreset('复盘', { grid: 3 })

    setActivePinia(createPinia())
    const second = useLayoutStore()
    expect(second.presets).toEqual({})
    second.loadPresets()
    expect(second.presets).toEqual({ 复盘: { grid: 3 } })
  })

  it('损坏预设 JSON 恢复为空', () => {
    localStorage.setItem('nt.layout.presets.v1', 'not-json')
    const store = useLayoutStore()
    store.loadPresets()
    expect(store.presets).toEqual({})
  })

  it('同名预设覆盖', () => {
    const store = useLayoutStore()
    store.savePreset('默认', { v: 1 })
    store.savePreset('默认', { v: 2 })
    expect(store.presets['默认']).toEqual({ v: 2 })
  })
})
