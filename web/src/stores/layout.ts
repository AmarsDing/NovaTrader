/**
 * 工作台布局（P3 dockview 持久化）。
 * - 键：nt.layout.v1.<accountId>；
 * - 版本号演进：未知 / 过低版本走 migrate()，最终兜底丢弃回默认预设；
 * - 预设：存 localStorage 的 JSON 字典（名称 → dockview toJSON）。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

/** 布局存储版本：默认屏结构变更即 bump（v4=FE-C6 默认屏瘦身四件），旧版当前排列收成「升级前的布局」模板。 */
export const LAYOUT_STORAGE_VERSION = 4
/** 布局版本升级时，把上一版当前排列收成这个模板，不直接丢掉。 */
export const ARCHIVED_LAYOUT_NAME = '升级前的布局'
export const LAYOUT_PREFIX = 'nt.layout.v1'
const PRESET_KEY = 'nt.layout.presets.v1'
/** FE-C5：预设热键映射（'5'..'9' → 预设名）独立存储，不动 presets 存量结构。 */
const PRESET_HOTKEY_KEY = 'nt.layout.preset-hotkeys.v1'

interface StoredLayout {
  version: number
  layout: unknown
}

export const useLayoutStore = defineStore('layout', () => {
  const presets = ref<Record<string, unknown>>({})
  const presetHotkeys = ref<Record<string, string>>({})

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

  /**
   * 版本迁移：低版本里那份当前排列收成「升级前的布局」，然后回默认屏。
   * 未来版本和空信封不收，避免把读不懂的结构写进模板。
   */
  function migrate(payload: Partial<StoredLayout>): unknown | null {
    const version = payload.version
    if (
      typeof version === 'number' &&
      version >= 1 &&
      version < LAYOUT_STORAGE_VERSION &&
      payload.layout != null
    ) {
      loadPresets()
      if (!Object.prototype.hasOwnProperty.call(presets.value, ARCHIVED_LAYOUT_NAME)) {
        savePreset(ARCHIVED_LAYOUT_NAME, payload.layout)
      }
    }
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
    try {
      const raw = localStorage.getItem(PRESET_HOTKEY_KEY)
      presetHotkeys.value = raw ? (JSON.parse(raw) as Record<string, string>) : {}
    } catch {
      presetHotkeys.value = {}
    }
  }

  function savePreset(name: string, layout: unknown) {
    presets.value = { ...presets.value, [name]: layout }
    // FE-C5：命名桌——保存即自动分配 Shift+5..9 空闲热键（满员不分配，菜单里仍可按名切换）
    if (!hotkeyOfPreset(name)) assignPresetHotkey(name)
    persistPresets()
  }

  function removePreset(name: string) {
    const next = { ...presets.value }
    delete next[name]
    presets.value = next
    unassignPresetHotkey(name)
    persistPresets()
  }

  /** FE-C5：预设热键表（'5'..'9' → 预设名）。内置桌占 1..4，这里只发 5..9。 */
  function hotkeyOfPreset(name: string): number | null {
    for (const [k, v] of Object.entries(presetHotkeys.value)) {
      if (v === name) return Number(k)
    }
    return null
  }

  function assignPresetHotkey(name: string): number | null {
    for (let n = 5; n <= 9; n += 1) {
      if (!presetHotkeys.value[String(n)]) {
        presetHotkeys.value = { ...presetHotkeys.value, [String(n)]: name }
        persistPresets()
        return n
      }
    }
    return null
  }

  function unassignPresetHotkey(name: string) {
    const hit = hotkeyOfPreset(name)
    if (hit == null) return
    const next = { ...presetHotkeys.value }
    delete next[String(hit)]
    presetHotkeys.value = next
  }

  function presetByHotkey(n: number): string | null {
    return presetHotkeys.value[String(n)] ?? null
  }

  function persistPresets() {
    try {
      localStorage.setItem(PRESET_KEY, JSON.stringify(presets.value))
      localStorage.setItem(PRESET_HOTKEY_KEY, JSON.stringify(presetHotkeys.value))
    } catch {
      /* 忽略 */
    }
  }

  return {
    presets,
    presetHotkeys,
    storageKey,
    save,
    load,
    clear,
    loadPresets,
    savePreset,
    removePreset,
    hotkeyOfPreset,
    presetByHotkey,
    assignPresetHotkey,
  }
})
