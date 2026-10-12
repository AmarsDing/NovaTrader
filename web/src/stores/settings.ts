/**
 * 界面偏好：主题（亮/暗）与密度（标准/紧凑）。
 * 持久化 localStorage，applyAll() 把值写到 <html> 的 data 属性，CSS 令牌据此切换（P2 接入）。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ThemeName = 'dark' | 'light'
export type DensityName = 'normal' | 'compact'

const KEY = 'nt.settings.v1'

export const useSettingsStore = defineStore('settings', () => {
  const theme = ref<ThemeName>('dark')
  const density = ref<DensityName>('normal')
  /** 极端预警声（AlertCenter 未确认期间循环响）；默认开。 */
  const sound = ref(true)

  function load() {
    try {
      const raw = localStorage.getItem(KEY)
      if (!raw) return
      const data = JSON.parse(raw) as { theme?: ThemeName; density?: DensityName; sound?: boolean }
      if (data.theme === 'dark' || data.theme === 'light') theme.value = data.theme
      if (data.density === 'normal' || data.density === 'compact') density.value = data.density
      if (typeof data.sound === 'boolean') sound.value = data.sound
    } catch {
      // 损坏偏好直接回默认。
    }
  }

  function persist() {
    try {
      localStorage.setItem(KEY, JSON.stringify({ theme: theme.value, density: density.value, sound: sound.value }))
    } catch {
      /* 忽略 */
    }
  }

  function applyAll() {
    document.documentElement.dataset.theme = theme.value
    document.documentElement.dataset.density = density.value
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

  function init() {
    load()
    applyAll()
  }

  return { theme, density, sound, init, load, applyAll, setTheme, setDensity, setSound }
})
