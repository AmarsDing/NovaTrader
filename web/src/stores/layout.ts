/**
 * 工作台布局（P3 dockview 持久化）。
 * - 键：nt.layout.v1.<accountId>；
 * - 版本号演进：未知 / 过低版本走 migrate()，最终兜底丢弃回默认预设；
 * - 预设：存 localStorage 的 JSON 字典（名称 → dockview toJSON）。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export const LAYOUT_STORAGE_VERSION = 1
export const LAYOUT_PREFIX = 'nt.layout.v1'
const PRESET_KEY = 'nt.layout.presets.v1'

interface StoredLayout {
  version: number
  layout: unknown
}

export const useLayoutStore = defineStore('layout', () => {
  const presets = ref<Record<string, unknown>>({})

  function storageKey(accountId: string): string {
    return `${LAYOUT_PREFIX}.${accountId || 'local'}`
  }

  function save(accountId: string, layout: unknown) {
    try {
      const payload: StoredLayout = { version: LAYOUT_STORAGE_VERSION, layout }
      localStorage.setItem(storageKey(accountId), JSON.stringify(payload))
    } catch {
      // localStorage 满/不可用时放弃持久化，布局只在内存。
    }
  }

  function load(accountId: string): unknown | null {
    try {
      const raw = localStorage.getItem(storageKey(accountId))
      if (!raw) return null
      const payload = JSON.parse(raw) as Partial<StoredLayout>
      if (!payload || typeof payload !== 'object') return null
      if (payload.version !== LAYOUT_STORAGE_VERSION) return migrate(payload)
      return payload.layout ?? null
    } catch {
      return null
    }
  }

  /** 版本迁移入口：当前只有 v1，低版本/异形直接丢弃（回默认预设）。 */
  function migrate(payload: Partial<StoredLayout>): unknown | null {
    void payload
    return null
  }

  function clear(accountId: string) {
    try {
      localStorage.removeItem(storageKey(accountId))
    } catch {
      /* 忽略 */
    }
  }

  function loadPresets() {
    try {
      const raw = localStorage.getItem(PRESET_KEY)
      presets.value = raw ? (JSON.parse(raw) as Record<string, unknown>) : {}
    } catch {
      presets.value = {}
    }
  }

  function savePreset(name: string, layout: unknown) {
    presets.value = { ...presets.value, [name]: layout }
    persistPresets()
  }

  function removePreset(name: string) {
    const next = { ...presets.value }
    delete next[name]
    presets.value = next
    persistPresets()
  }

  function persistPresets() {
    try {
      localStorage.setItem(PRESET_KEY, JSON.stringify(presets.value))
    } catch {
      /* 忽略 */
    }
  }

  return { presets, storageKey, save, load, clear, loadPresets, savePreset, removePreset }
})
