import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useSettingsStore } from '../settings'

let stores: ReturnType<typeof useSettingsStore>[]
let reduced = false
let change: (() => void) | undefined
const addEventListener = vi.fn((_event: string, callback: () => void) => { change = callback })
const removeEventListener = vi.fn()

beforeEach(() => {
  stores = []
  reduced = false
  change = undefined
  vi.clearAllMocks()
  localStorage.clear()
  setActivePinia(createPinia())
  vi.stubGlobal('matchMedia', () => ({ get matches() { return reduced }, addEventListener, removeEventListener }))
})

afterEach(() => {
  for (const store of stores) store.$dispose()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function settings() {
  const store = useSettingsStore()
  stores.push(store)
  return store
}

it('模式写入统一设置并在新实例恢复，旧主题密度和预警声保留', () => {
  const store = settings()
  store.init()
  store.setTheme('light')
  store.setDensity('compact')
  store.setSound(false)
  store.setVisualQuality('eco')
  expect(document.documentElement.dataset.visualQuality).toBe('eco')
  expect(document.documentElement.dataset.motion).toBe('reduced')
  expect(JSON.parse(localStorage.getItem('nt.settings.v1') || '{}').visualQuality).toBe('eco')
  setActivePinia(createPinia())
  const restored = settings()
  restored.init()
  expect(restored.visualQuality).toBe('eco')
  expect(restored.theme).toBe('light')
  expect(restored.density).toBe('compact')
  expect(restored.sound).toBe(false)
})

it('系统减少动态覆盖完整模式，偏好变化即时更新且不覆盖用户模式', () => {
  const store = settings()
  store.init()
  expect(store.reducedMotion).toBe(false)
  reduced = true
  change?.()
  expect(store.systemReducedMotion).toBe(true)
  expect(document.documentElement.dataset.motion).toBe('reduced')
  expect(store.visualQuality).toBe('full')
  reduced = false
  change?.()
  expect(document.documentElement.dataset.motion).toBe('full')
  store.setVisualQuality('reduced')
  expect(document.documentElement.dataset.motion).toBe('reduced')
})

it('缺少或无效材质字段兼容旧设置，重复init释放旧环境监听', () => {
  localStorage.setItem('nt.settings.v1', JSON.stringify({ theme: 'light', density: 'compact', visualQuality: 'unknown' }))
  const store = settings()
  store.init()
  expect(store.visualQuality).toBe('full')
  expect(store.theme).toBe('light')
  store.init()
  expect(removeEventListener).toHaveBeenCalledTimes(1)
  expect(addEventListener).toHaveBeenCalledTimes(2)
})

it('页面隐藏和恢复标志即时更新，显示质量和动态偏好保持', () => {
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  const store = settings()
  store.init()
  visibility.mockReturnValue('hidden')
  document.dispatchEvent(new Event('visibilitychange'))
  expect(store.pageVisible).toBe(false)
  expect(document.documentElement.dataset.pageVisibility).toBe('hidden')
  expect(store.visualQuality).toBe('full')
  visibility.mockReturnValue('visible')
  document.dispatchEvent(new Event('visibilitychange'))
  expect(document.documentElement.dataset.pageVisibility).toBe('visible')
})

it('受管弹窗同步主题/密度/质量，独立可见性与解绑清理', () => {
  const store = settings()
  store.init()
  const child = document.implementation.createHTMLDocument('popout')
  const visibility = vi.spyOn(child, 'visibilityState', 'get').mockReturnValue('visible')
  const detach = store.attachVisualDocument(child)
  store.setTheme('light')
  store.setDensity('compact')
  store.setVisualQuality('eco')
  expect(child.documentElement.dataset).toMatchObject({
    theme: 'light', density: 'compact', visualQuality: 'eco', motion: 'reduced', pageVisibility: 'visible',
  })
  visibility.mockReturnValue('hidden')
  child.dispatchEvent(new Event('visibilitychange'))
  expect(child.documentElement.dataset.pageVisibility).toBe('hidden')
  expect(store.pageVisible).toBe(true)
  detach()
  detach()
  store.setVisualQuality('full')
  expect(child.documentElement.dataset.visualQuality).toBe('eco')
})
