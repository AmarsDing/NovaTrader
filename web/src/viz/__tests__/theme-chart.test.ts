import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'
import { mount, shallowMount, type VueWrapper } from '@vue/test-utils'
import {
  createChart as lcmCreateChart,
  createTextWatermark as lcmCreateTextWatermark,
} from 'lightweight-charts'
import { colorWithAlpha, PALETTE, useChartPalette } from '@/theme/tokens'
import NtSectorBar from '../NtSectorBar.vue'
import NtEChart from '../NtEChart.vue'
import NtKline from '../NtKline.vue'

const mocks = vi.hoisted(() => {
  const makeSeries = () => ({
    setData: vi.fn(),
    applyOptions: vi.fn(),
    update: vi.fn(),
    createPriceLine: vi.fn((options: unknown) => ({ options })),
    removePriceLine: vi.fn(),
  })
  const candle = makeSeries()
  const volume = makeSeries()
  const lines = [makeSeries(), makeSeries(), makeSeries()]
  const fitContent = vi.fn()
  /** v5: createSeriesMarkers 返回插件 API，setMarkers 落在它身上。 */
  const markers = { setMarkers: vi.fn() }
  const watermark = { applyOptions: vi.fn() }
  let lineCursor = 0
  return {
    candle,
    volume,
    lines,
    fitContent,
    markers,
    watermark,
    resetLineCursor: () => { lineCursor = 0 },
    nextLine: () => lines[(lineCursor++) % 3],
    defs: { CandlestickSeries: 'Candlestick', HistogramSeries: 'Histogram', LineSeries: 'Line' },
    chart: {
      applyOptions: vi.fn(),
      addSeries: vi.fn(),
      panes: vi.fn(() => [{}]),
      priceScale: vi.fn(() => ({ applyOptions: vi.fn() })),
      timeScale: vi.fn(() => ({ fitContent })),
      subscribeCrosshairMove: vi.fn(),
      remove: vi.fn(),
    },
    echarts: { setOption: vi.fn(), clear: vi.fn(), resize: vi.fn(), dispose: vi.fn() },
  }
})

vi.mock('lightweight-charts', () => ({
  CandlestickSeries: mocks.defs.CandlestickSeries,
  HistogramSeries: mocks.defs.HistogramSeries,
  LineSeries: mocks.defs.LineSeries,
  ColorType: { Solid: 'solid' },
  CrosshairMode: { Normal: 0, Magnet: 1, Hidden: 2, MagnetOHLC: 3 },
  LineStyle: { Solid: 0, Dotted: 1, Dashed: 2 },
  createChart: vi.fn(() => mocks.chart),
  createSeriesMarkers: vi.fn(() => mocks.markers),
  createTextWatermark: vi.fn(() => mocks.watermark),
}))
vi.mock('echarts/core', () => ({ use: vi.fn(), init: vi.fn(() => mocks.echarts) }))

// vi.mock 提升后此处拿到的是 mock 工厂的 vi.fn 替身。
const createChartMock = vi.mocked(lcmCreateChart)
const createTextWatermarkMock = vi.mocked(lcmCreateTextWatermark)

const wrappers: VueWrapper[] = []
let style: HTMLStyleElement

async function switchTheme(theme: string) {
  document.documentElement.dataset.theme = theme
  await new Promise<void>((resolve) => queueMicrotask(resolve))
  await nextTick()
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.resetLineCursor()
  mocks.chart.addSeries.mockImplementation((def: unknown) => {
    if (def === mocks.defs.LineSeries) return mocks.nextLine()
    return def === mocks.defs.HistogramSeries ? mocks.volume : mocks.candle
  })
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
    expect(mocks.markers.setMarkers.mock.lastCall?.[0][0].color).toBe('#654321')
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

  it('feeds live ticks through updateLast without resetting data or zoom', () => {
    const bars = [10, 11, 12, 13, 14].map((close, index) => ({
      time: `2026-10-0${index + 5}`,
      open: close,
      high: close + 1,
      low: close - 1,
      close,
      volume: 100,
    }))
    const wrapper = mount(NtKline, { props: { bars } })
    wrappers.push(wrapper)
    const api = wrapper.vm as unknown as {
      updateLast: (bar: { time: string; open: number; high: number; low: number; close: number; volume: number }) => boolean
      refit: () => void
    }
    expect(mocks.fitContent).toHaveBeenCalledTimes(1)
    expect(mocks.candle.setData).toHaveBeenCalledTimes(1)

    // 同根替换：同一天的新帧只走 series.update；MA5 用本地收盘价续算（10+11+12+13+20)/5 = 13.2。
    expect(api.updateLast({ time: '2026-10-09', open: 13, high: 15, low: 12, close: 20, volume: 200 })).toBe(true)
    expect(mocks.candle.update).toHaveBeenLastCalledWith({ time: '2026-10-09', open: 13, high: 15, low: 12, close: 20 })
    expect(mocks.volume.update).toHaveBeenLastCalledWith(expect.objectContaining({ time: '2026-10-09', value: 200 }))
    expect(mocks.lines[0].update).toHaveBeenLastCalledWith({ time: '2026-10-09', value: 13.2 })
    expect(mocks.candle.setData).toHaveBeenCalledTimes(1)
    expect(mocks.fitContent).toHaveBeenCalledTimes(1)

    // 新根追加：共 6 根，不足 MA10/MA20，两条长均线不落 update。
    expect(api.updateLast({ time: '2026-10-12', open: 20, high: 21, low: 19, close: 21, volume: 300 })).toBe(true)
    expect(mocks.candle.update).toHaveBeenLastCalledWith({ time: '2026-10-12', open: 20, high: 21, low: 19, close: 21 })
    expect(mocks.lines[0].update).toHaveBeenLastCalledWith({ time: '2026-10-12', value: 15.4 })
    expect(mocks.lines[1].update).not.toHaveBeenCalled()
    expect(mocks.lines[2].update).not.toHaveBeenCalled()

    // 旧帧乱序：拒绝且不再产生任何 update 调用。
    const updateCalls = mocks.candle.update.mock.calls.length
    expect(api.updateLast({ time: '2026-10-05', open: 1, high: 1, low: 1, close: 1, volume: 1 })).toBe(false)
    expect(mocks.candle.update.mock.calls.length).toBe(updateCalls)

    // 视口只允许显式复位。
    api.refit()
    expect(mocks.fitContent).toHaveBeenCalledTimes(2)
  })

  it('uses magnet crosshair, draws a themed code watermark and rebuilds level price lines', async () => {
    const wrapper = mount(NtKline, {
      props: {
        bars: [{ time: '2026-10-14', open: 10, high: 12, low: 9, close: 11, volume: 100 }],
        watermark: '600519.SH',
        levels: [
          { price: 9.86, color: '#edba32', title: '摊薄成本', lineStyle: 'dashed' },
          { price: 9.86, title: '重复的去重' },
          { price: -1, title: '非法不画' },
        ],
      },
    })
    wrappers.push(wrapper)
    // 磁吸 OHLC（mode=3）：十字线贴最近 K 线开高低收。
    expect(createChartMock.mock.lastCall?.[1]).toMatchObject({ crosshair: { mode: 3 } })
    // 水印建图即注入，不用等 apply。
    expect(createTextWatermarkMock.mock.lastCall?.[1]).toMatchObject({
      visible: true,
      lines: [{ text: '600519.SH', fontSize: 44 }],
    })
    // 参考线：重复价去重、非法价跳过、样式映射 dashed→2。
    const priceOpts = mocks.candle.createPriceLine.mock.calls.map((call) => call[0])
    expect(priceOpts).toHaveLength(1)
    expect(priceOpts[0]).toMatchObject({ price: 9.86, color: '#edba32', lineWidth: 1, lineStyle: 2, title: '摊薄成本' })
    // 主题切换：旧线先移除再按新主题重建；水印颜色随 --nt-text-3 更新。
    await switchTheme('light')
    expect(mocks.candle.removePriceLine).toHaveBeenCalled()
    expect(mocks.candle.createPriceLine.mock.lastCall?.[0]).toMatchObject({ price: 9.86 })
    expect((mocks.watermark.applyOptions.mock.lastCall?.[0] as { lines: { color: string }[] }).lines[0].color).toBe('rgba(97, 98, 99, 0.28)')
  })
})
