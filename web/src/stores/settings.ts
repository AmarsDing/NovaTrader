/**
 * 界面偏好：主题（亮/暗）与密度（标准/紧凑）。
 * 持久化 localStorage，applyAll() 把值写到 <html> 的 data 属性，CSS 令牌据此切换（P2 接入）。
 */
import { defineStore } from 'pinia'
import { computed, onScopeDispose, ref } from 'vue'
import { reduceVisualMotion, VISUAL_DOCUMENT_EVENT, visualQualityOf, type VisualQuality } from '@/theme/visual-quality'

export type ThemeName = 'dark' | 'light'
export type DensityName = 'normal' | 'compact'

const KEY = 'nt.settings.v1'

export const useSettingsStore = defineStore('settings', () => {
  const theme = ref<ThemeName>('dark')
  const density = ref<DensityName>('normal')
  const visualQuality = ref<VisualQuality>('full')
  const systemReducedMotion = ref(false)
  const pageVisible = ref(true)
  const reducedMotion = computed(() => reduceVisualMotion(visualQuality.value, systemReducedMotion.value))
  let stopVisualEnvironment: (() => void) | undefined
  const visualDocuments = new Map<Document, () => void>()
  /** 极端预警声（AlertCenter 未确认期间循环响）；默认开。 */
  const sound = ref(true)

  function load() {
    try {
      const raw = localStorage.getItem(KEY)
      if (!raw) return
      const data = JSON.parse(raw) as { theme?: ThemeName; density?: DensityName; sound?: boolean; visualQuality?: unknown }
      if (data.theme === 'dark' || data.theme === 'light') theme.value = data.theme
      if (data.density === 'normal' || data.density === 'compact') density.value = data.density
      if (typeof data.sound === 'boolean') sound.value = data.sound
      visualQuality.value = visualQualityOf(data.visualQuality)
    } catch {
      // 损坏偏好直接回默认。
    }
  }

  function persist() {
    try {
      localStorage.setItem(KEY, JSON.stringify({
        theme: theme.value, density: density.value, sound: sound.value, visualQuality: visualQuality.value,
      }))
    } catch {
      /* 忽略 */
    }
  }

  function applyDocument(target: Document) {
    const root = target.documentElement
    root.dataset.theme = theme.value
    root.dataset.density = density.value
    root.dataset.visualQuality = visualQuality.value
    root.dataset.motion = reducedMotion.value ? 'reduced' : 'full'
    root.dataset.pageVisibility = target.visibilityState !== 'hidden' ? 'visible' : 'hidden'
  }

  function applyAll() {
    applyDocument(document)
    for (const target of visualDocuments.keys()) applyDocument(target)
    document.dispatchEvent(new Event(VISUAL_DOCUMENT_EVENT))
  }

  /** Owned same-origin popout only. It receives appearance attributes, never account or execution state. */
  function attachVisualDocument(target: Document): () => void {
    if (target === document) return () => {}
    visualDocuments.get(target)?.()
    const sync = () => applyDocument(target)
    target.addEventListener('visibilitychange', sync)
    let active = true
    const detach = () => {
      if (!active) return
      active = false
      target.removeEventListener('visibilitychange', sync)
      visualDocuments.delete(target)
      document.dispatchEvent(new Event(VISUAL_DOCUMENT_EVENT))
    }
    visualDocuments.set(target, detach)
    applyAll()
    return detach
  }

  function setTheme(value: ThemeName) {
    theme.value = value
    persist()
    applyAll()
  }

  function setDensity(value: DensityName) {
    density.value = value
    persist()
    applyAll()
  }

  function setSound(value: boolean) {
    sound.value = value
    persist()
  }

  function setVisualQuality(value: VisualQuality) {
    visualQuality.value = visualQualityOf(value)
    persist()
    applyAll()
  }

  function watchVisualEnvironment() {
    stopVisualEnvironment?.()
    const media = window.matchMedia?.('(prefers-reduced-motion: reduce)')
    const sync = () => {
      systemReducedMotion.value = media?.matches === true
      pageVisible.value = document.visibilityState !== 'hidden'
      applyAll()
    }
    const syncStorage = (event: StorageEvent) => {
      if (event.key !== KEY || event.storageArea !== localStorage) return
      load()
      sync()
    }
    media?.addEventListener('change', sync)
    document.addEventListener('visibilitychange', sync)
    window.addEventListener('storage', syncStorage)
    stopVisualEnvironment = () => {
      media?.removeEventListener('change', sync)
      document.removeEventListener('visibilitychange', sync)
      window.removeEventListener('storage', syncStorage)
    }
    sync()
  }

  function init() {
    load()
    watchVisualEnvironment()
  }

  onScopeDispose(() => {
    stopVisualEnvironment?.()
    for (const detach of visualDocuments.values()) detach()
  })

  return {
    theme, density, sound, visualQuality, systemReducedMotion, pageVisible, reducedMotion,
    init, load, applyAll, setTheme, setDensity, setSound, setVisualQuality, attachVisualDocument,
  }
})
