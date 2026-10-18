import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'
import { mount, shallowMount, type VueWrapper } from '@vue/test-utils'
import { colorWithAlpha, PALETTE, useChartPalette } from '@/theme/tokens'
import NtSectorBar from '../NtSectorBar.vue'
import NtEChart from '../NtEChart.vue'
import NtKline from '../NtKline.vue'

const mocks = vi.hoisted(() => {
  const makeSeries = () => ({ setData: vi.fn(), setMarkers: vi.fn(), applyOptions: vi.fn() })
  const candle = makeSeries()
  const volume = makeSeries()
  const lines = [makeSeries(), makeSeries(), makeSeries()]
  const fitContent = vi.fn()
  return {
    candle,
    volume,
    lines,
    fitContent,
    chart: {
      applyOptions: vi.fn(),
      addCandlestickSeries: vi.fn(() => candle),
      addHistogramSeries: vi.fn(() => volume),
      addLineSeries: vi.fn(),
      priceScale: vi.fn(() => ({ applyOptions: vi.fn() })),
      timeScale: vi.fn(() => ({ fitContent })),
      subscribeCrosshairMove: vi.fn(),
      remove: vi.fn(),
    },
    echarts: { setOption: vi.fn(), clear: vi.fn(), resize: vi.fn(), dispose: vi.fn() },
  }
})

vi.mock('lightweight-charts', () => ({
  ColorType: { Solid: 'solid' },
  CrosshairMode: { Normal: 0 },
  createChart: vi.fn(() => mocks.chart),
}))
vi.mock('echarts/core', () => ({ use: vi.fn(), init: vi.fn(() => mocks.echarts) }))

const wrappers: VueWrapper[] = []
let style: HTMLStyleElement

async function switchTheme(theme: string) {
  document.documentElement.dataset.theme = theme
  await new Promise<void>((resolve) => queueMicrotask(resolve))
  await nextTick()
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.chart.addLineSeries.mockImplementationOnce(() => mocks.lines[0])
    .mockImplementationOnce(() => mocks.lines[1]).mockImplementationOnce(() => mocks.lines[2])
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  document.documentElement.removeAttribute('data-theme')
  style = document.createElement('style')
  // Deliberately distinct test themes: assertions verify CSS ownership, not a duplicated production color table.
  style.textContent = `
    :root { --nt-bg-2: #182938; --nt-text-1: #e1e2e3; --nt-text-2: #a1a2a3;
      --nt-text-3: #818283; --nt-border-1: #213243; --nt-border-2: #314253;
      --nt-accent: #123456; --nt-accent-2: #234567; --nt-warn: #edba32;
      --nt-up: #eb1234; --nt-down: #12ab34; }
    :root[data-theme='light'] { --nt-bg-2: #edf4f8; --nt-text-1: #212223;
      --nt-text-2: #414243; --nt-text-3: #616263; --nt-border-1: #b1c2d3;
      --nt-border-2: #a1b2c3; --nt-accent: #654321; --nt-accent-2: #765432;
      --nt-up: rgb(182, 23, 49); --nt-down: #008855; }
  `
  document.head.append(style)
})

afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  style.remove()
  document.documentElement.removeAttribute('data-theme')
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('live chart theme', () => {
  it('shares one observer, reads CSS changes and releases it after the last consumer', async () => {
    const observe = vi.spyOn(MutationObserver.prototype, 'observe')
    const disconnect = vi.spyOn(MutationObserver.prototype, 'disconnect')
    const first = effectScope()
    const second = effectScope()
    const colors = first.run(() => useChartPalette())!
    second.run(() => useChartPalette())
    expect(observe).toHaveBeenCalledTimes(1)
    expect(colors.value.accent).toBe('#123456')
    first.stop()
    expect(disconnect).not.toHaveBeenCalled()
    await switchTheme('light')
    expect(colors.value.accent).toBe('#654321')
    expect(PALETTE.accent).toBe(colors.value.accent)
    second.stop()
    expect(disconnect).toHaveBeenCalledTimes(1)
  })

  it('rebuilds ECharts series and axes on a theme switch without altering data', async () => {
    const wrapper = shallowMount(NtSectorBar, { props: { rows: [{ name: '半导体', avgPct: 2.5 }, { name: '银行', avgPct: -1 }] } })
    wrappers.push(wrapper)
    const option = () => wrapper.getComponent(NtEChart).props('option') as {
      xAxis: { axisLabel: { color: string } }
      series: { data: { value: number; itemStyle: { color: string } }[] }[]
    }
    expect(option().series[0].data[0].itemStyle.color).toBe('#eb1234')
    await switchTheme('light')
    expect(option().xAxis.axisLabel.color).toBe('#616263')
    expect(option().series[0].data.map((row) => row.value)).toEqual([2.5, -1])
    expect(option().series[0].data[0].itemStyle.color).toBe(getComputedStyle(document.documentElement).getPropertyValue('--nt-up').trim())
    expect(option().series[0].data[1].itemStyle.color).toBe('#008855')
  })

  it('refreshes tooltip surfaces and text while preserving caller options', async () => {
    const wrapper = mount(NtEChart, { props: { option: { tooltip: { trigger: 'axis' }, series: [{ type: 'line', data: [1, 2] }] } } })
    wrappers.push(wrapper)
    await switchTheme('light')
    const rendered = mocks.echarts.setOption.mock.lastCall?.[0]
    expect(rendered.tooltip).toMatchObject({ trigger: 'axis', backgroundColor: '#edf4f8', textStyle: { color: '#212223' } })
    expect(rendered.series[0].data).toEqual([1, 2])
    expect(rendered.color.slice(0, 2)).toEqual(['#654321', '#765432'])
    await wrapper.setProps({ option: { color: ['#aabbcc'], series: [{ type: 'line', data: [1, 2] }] } })
    expect(mocks.echarts.setOption.mock.lastCall?.[0].color).toEqual(['#aabbcc'])
  })

  it('updates K-line, volume and default markers but keeps zoom and market values', async () => {
    const wrapper = mount(NtKline, { props: {
      bars: [{ time: '2026-10-14', open: 10, high: 12, low: 9, close: 11, volume: 100 }],
      markers: [{ time: '2026-10-14' }],
    } })
    wrappers.push(wrapper)
    const originalData = mocks.candle.setData.mock.lastCall?.[0]
    expect(mocks.fitContent).toHaveBeenCalledTimes(1)
    await switchTheme('light')
    expect(mocks.chart.applyOptions.mock.lastCall?.[0]).toMatchObject({ layout: { textColor: '#414243' }, grid: { vertLines: { color: '#b1c2d3' } } })
    expect(mocks.candle.applyOptions.mock.lastCall?.[0]).toMatchObject({ upColor: getComputedStyle(document.documentElement).getPropertyValue('--nt-up').trim(), downColor: '#008855' })
    expect(mocks.volume.setData.mock.lastCall?.[0][0].color).toBe('rgba(182, 23, 49, 0.4)')
    expect(mocks.candle.setMarkers.mock.lastCall?.[0][0].color).toBe('#654321')
    expect(mocks.candle.setData.mock.lastCall?.[0]).toEqual(originalData)
    expect(mocks.fitContent).toHaveBeenCalledTimes(1)
    wrapper.unmount()
    wrappers.pop()
    expect(mocks.chart.remove).toHaveBeenCalledTimes(1)
  })

  it('accepts both hex and rgb tokens when adding volume transparency', () => {
    expect(colorWithAlpha('#abc', 0.4)).toBe('rgba(170, 187, 204, 0.4)')
    expect(colorWithAlpha('rgb(1, 2, 3)', 0.4)).toBe('rgba(1, 2, 3, 0.4)')
    expect(colorWithAlpha('rgba(1, 2, 3, 0.5)', 0.4)).toBe('rgba(1, 2, 3, 0.2)')
  })
})
