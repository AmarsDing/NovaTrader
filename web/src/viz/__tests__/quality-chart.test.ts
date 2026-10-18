import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import NtEChart from '../NtEChart.vue'
import { VISUAL_DOCUMENT_EVENT } from '@/theme/visual-quality'

const mock = vi.hoisted(() => {
  const stopAnimation = vi.fn()
  const refreshImmediately = vi.fn()
  return {
    stopAnimation, refreshImmediately,
    chart: {
      setOption: vi.fn(), clear: vi.fn(), resize: vi.fn(), dispose: vi.fn(),
      getZr: () => ({ storage: { getDisplayList: () => [{ stopAnimation }] }, refreshImmediately }),
    },
  }
})
vi.mock('echarts/core', () => ({ use: vi.fn(), init: vi.fn(() => mock.chart) }))
let wrapper: VueWrapper | undefined

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  document.documentElement.dataset.motion = 'full'
  document.documentElement.dataset.pageVisibility = 'visible'
  document.documentElement.dataset.visualQuality = 'full'
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.unstubAllGlobals()
  document.documentElement.removeAttribute('data-motion')
  document.documentElement.removeAttribute('data-page-visibility')
  document.documentElement.removeAttribute('data-visual-quality')
})

async function mutations() {
  await new Promise<void>((resolve) => queueMicrotask(resolve))
  await nextTick()
}

it('减少动态覆盖系列动画，只合并动画标志保留数据和用户缩放', async () => {
  wrapper = mount(NtEChart, { props: { option: {
    animation: true, dataZoom: [{ start: 30, end: 70 }], series: [{ id: 'nav', type: 'line', animation: true, data: [1, 2] }],
  } } })
  const first = mock.chart.setOption.mock.calls[0][0]
  expect(first.series[0].data).toEqual([1, 2])
  document.documentElement.dataset.motion = 'reduced'
  await mutations()
  expect(mock.chart.setOption.mock.lastCall).toEqual([{ animation: false, series: [{ id: 'nav', animation: false }] }])
  expect(mock.stopAnimation).toHaveBeenCalledWith(undefined, true)
  expect(mock.refreshImmediately).toHaveBeenCalled()
  document.documentElement.dataset.motion = 'full'
  await mutations()
  expect(mock.chart.setOption.mock.lastCall).toEqual([{ animation: true, series: [{ id: 'nav', animation: true }] }])
})

it('节能和后台禁图表装饰，新数据仍可呈现，回前台恢复调用方动画偏好', async () => {
  document.documentElement.dataset.visualQuality = 'eco'
  wrapper = mount(NtEChart, { props: { option: { series: [{ type: 'line', data: [1] }] } } })
  expect(mock.chart.setOption.mock.calls[0][0].animation).toBe(false)
  await wrapper.setProps({ option: { animation: false, series: [{ type: 'line', data: [3] }] } })
  expect(mock.chart.setOption.mock.lastCall?.[0].series[0].data).toEqual([3])
  document.documentElement.dataset.visualQuality = 'full'
  document.documentElement.dataset.pageVisibility = 'hidden'
  await mutations()
  expect(mock.chart.setOption.mock.lastCall?.[0].animation).toBe(false)
  document.documentElement.dataset.pageVisibility = 'visible'
  await mutations()
  expect(mock.chart.setOption.mock.lastCall?.[0].animation).toBe(false)
})

it('图表DOM移入只读弹窗后按所属文档的可见性控制动画', async () => {
  wrapper = mount(NtEChart, { props: { option: { series: [{ type: 'line', data: [1] }] } } })
  const child = document.implementation.createHTMLDocument('popout')
  child.documentElement.dataset.motion = 'full'
  child.documentElement.dataset.visualQuality = 'full'
  child.documentElement.dataset.pageVisibility = 'visible'
  child.body.appendChild(wrapper.element)
  document.dispatchEvent(new Event(VISUAL_DOCUMENT_EVENT))
  await mutations()
  child.documentElement.dataset.pageVisibility = 'hidden'
  await mutations()
  expect(mock.chart.setOption.mock.lastCall?.[0].animation).toBe(false)
  child.documentElement.dataset.pageVisibility = 'visible'
  await mutations()
  expect(mock.chart.setOption.mock.lastCall?.[0].animation).toBe(true)
})
