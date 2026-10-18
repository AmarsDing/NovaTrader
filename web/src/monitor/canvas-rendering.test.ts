import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import CanvasStage from './CanvasStage.vue'
import type { SceneGraph } from './types'

let wrapper: VueWrapper | undefined
let frames: Map<number, FrameRequestCallback>
let nextId: number
const fillText = vi.fn()
const fillRect = vi.fn()
const gradient = { addColorStop: vi.fn() }

function graph(face = '等待输入'): SceneGraph {
  return {
    nodes: [{ id: 'book:center', title: '账户', ring: 'book', status: 'gap', detail: '', rank: 'focus',
      face, sub: '', tone: 'flat', inputs: [], outputs: [] }],
    edges: [], pulses: [], news: [], terminals: [], focusId: '', truncated: false,
  }
}

beforeEach(() => {
  frames = new Map()
  nextId = 0
  fillText.mockClear()
  fillRect.mockClear()
  localStorage.clear()
  document.documentElement.dataset.motion = 'reduced'
  document.documentElement.dataset.pageVisibility = 'visible'
  document.documentElement.dataset.visualQuality = 'reduced'
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000)
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600)
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    frames.set(++nextId, callback)
    return nextId
  })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id))
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  const context = new Proxy<Record<string, unknown>>({
    fillText, fillRect, measureText: (text: string) => ({ width: text.length * 6 }),
    createLinearGradient: () => gradient, createRadialGradient: () => gradient,
  }, { get: (target, key) => target[String(key)] ?? (() => {}) })
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D)
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  document.documentElement.removeAttribute('data-motion')
  document.documentElement.removeAttribute('data-page-visibility')
  document.documentElement.removeAttribute('data-visual-quality')
})

function flushFrame() {
  const callbacks = [...frames.values()]
  frames.clear()
  callbacks.forEach((callback) => callback(0))
}

it('减少动态不续装饰帧，但新账户状态和缩放仍绘制', async () => {
  wrapper = mount(CanvasStage, { props: { graph: graph(), selectedId: '', paused: false } })
  flushFrame()
  expect(fillText.mock.calls.some(([value]) => value === '等待输入')).toBe(true)
  expect(frames.size).toBe(0)
  await wrapper.setProps({ graph: graph('状态已更新') })
  flushFrame()
  expect(fillText.mock.calls.some(([value]) => value === '状态已更新')).toBe(true)
  const before = fillRect.mock.calls.length
  wrapper.find('canvas').element.dispatchEvent(new WheelEvent('wheel', {
    deltaY: -1, clientX: 200, clientY: 200, cancelable: true,
  }))
  flushFrame()
  expect(fillRect.mock.calls.length).toBeGreaterThan(before)
  expect(frames.size).toBe(0)
})

it('完整模式无真实动态内容也不空转，切模式不丢当前节点', async () => {
  document.documentElement.dataset.motion = 'full'
  document.documentElement.dataset.visualQuality = 'full'
  wrapper = mount(CanvasStage, { props: { graph: graph(), selectedId: '', paused: false } })
  flushFrame()
  expect(frames.size).toBe(0)
  await wrapper.setProps({ killActive: true })
  flushFrame()
  expect(frames.size).toBe(1)
  document.documentElement.dataset.motion = 'reduced'
  await nextTick()
  flushFrame()
  expect(frames.size).toBe(0)
  expect(fillText.mock.calls.some(([value]) => value === '等待输入')).toBe(true)
})

it('后台取消Canvas RAF，恢复绘制最新状态，卸载不再续帧', async () => {
  document.documentElement.dataset.motion = 'full'
  document.documentElement.dataset.visualQuality = 'full'
  const visible = vi.spyOn(document, 'visibilityState', 'get')
  wrapper = mount(CanvasStage, { props: { graph: graph(), selectedId: '', paused: false, killActive: true } })
  flushFrame()
  expect(frames.size).toBe(1)
  visible.mockReturnValue('hidden')
  document.dispatchEvent(new Event('visibilitychange'))
  expect(frames.size).toBe(0)
  await wrapper.setProps({ graph: graph('后台已更新') })
  expect(frames.size).toBe(0)
  visible.mockReturnValue('visible')
  document.dispatchEvent(new Event('visibilitychange'))
  flushFrame()
  expect(fillText.mock.calls.some(([value]) => value === '后台已更新')).toBe(true)
  expect(frames.size).toBe(1)
  wrapper.unmount()
  wrapper = undefined
  expect(frames.size).toBe(0)
})
