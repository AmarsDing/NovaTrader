import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'
import { mount, shallowMount, type VueWrapper } from '@vue/test-utils'
import {
  createChart as lcmCreateChart,
  createTextWatermark as lcmCreateTextWatermark,
} from 'lightweight-charts'
import { colorWithAlpha, PALETTE, useChartPalette } from '@/theme/tokens'
import { chartTime } from '@/utils/time'
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
    attachPrimitive: vi.fn(),
    // 仿真 lwc 行为：detach 时回调 primitive 自己的 detached 生命周期。
    detachPrimitive: vi.fn((prim: { detached?: () => void } | null) => prim?.detached?.()),
    coordinateToPrice: vi.fn((y: number) => 20 - y / 10),
    priceToCoordinate: vi.fn((value: number) => (20 - value) * 10),
  })
  const candle = makeSeries()
  const volume = makeSeries()
  const lines = [makeSeries(), makeSeries(), makeSeries()]
  const fitContent = vi.fn()
  /** v5: createSeriesMarkers 返回插件 API，setMarkers 落在它身上。 */
  const markers = { setMarkers: vi.fn() }
  const watermark = { applyOptions: vi.fn() }
  /** 集合竞价色带 custom series：挂载即建（最底层），数据在 apply() 里按 freq 维护。 */
  const custom = makeSeries()
  /** addPane 生成的副图 mock：记录建出的序列，paneIndex 随 removePane 动态位移。 */
  const panes: {
    addSeries: ReturnType<typeof vi.fn>
    setStretchFactor: ReturnType<typeof vi.fn>
    paneIndex: ReturnType<typeof vi.fn>
    seriesCreated: ReturnType<typeof makeSeries>[]
  }[] = []
  let lineCursor = 0
  return {
    candle,
    volume,
    lines,
    fitContent,
    markers,
    watermark,
    custom,
    panes,
    resetLineCursor: () => { lineCursor = 0 },
    nextLine: () => lines[(lineCursor++) % 3],
    defs: { CandlestickSeries: 'Candlestick', HistogramSeries: 'Histogram', LineSeries: 'Line' },
    chart: {
      applyOptions: vi.fn(),
      addSeries: vi.fn(),
      addPane: vi.fn(() => {
        const seriesCreated: ReturnType<typeof makeSeries>[] = []
        const pane = {
          addSeries: vi.fn(() => { const series = makeSeries(); seriesCreated.push(series); return series }),
          setStretchFactor: vi.fn(),
          paneIndex: vi.fn((): number => panes.indexOf(pane) + 1),
          seriesCreated,
        }
        panes.push(pane)
        return pane
      }),
      removePane: vi.fn((index: number) => { panes.splice(index - 1, 1) }),
      addCustomSeries: vi.fn(() => mocks.custom),
      panes: vi.fn(() => [{}]),
      priceScale: vi.fn(() => ({ applyOptions: vi.fn() })),
      timeScale: vi.fn(() => ({
        fitContent,
        coordinateToLogical: vi.fn((x: number) => Math.round(x / 10)),
        logicalToCoordinate: vi.fn((logical: number) => logical * 10),
      })),
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
  customSeriesDefaultOptions: {},
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
  mocks.panes.length = 0
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

  it('feeds MACD/RSI into v5 sub-panes with themed colors and removes them when indicators vanish', async () => {
    const times = ['2026-10-05', '2026-10-06', '2026-10-07', '2026-10-08', '2026-10-09']
    const macd = [-1, 2, -3, 4, -5]
    const bars = times.map((time, index) => ({ time, open: 10, high: 12, low: 9, close: 11, volume: 100 + index }))
    const indicators = times.map((time, index) => ({
      time, ma5: null, ma10: null, ma20: null,
      dif: 0.1 * index, dea: 0.05 * index, macd: macd[index], rsi6: 55 + index,
    }))
    const wrapper = mount(NtKline, { props: { bars, indicators } })
    wrappers.push(wrapper)
    // 两窗格：MACD（0.35）+ RSI（0.25）。
    expect(mocks.chart.addPane).toHaveBeenCalledTimes(2)
    const [macdPane, rsiPane] = mocks.panes
    expect(macdPane.setStretchFactor).toHaveBeenCalledWith(0.35)
    expect(rsiPane.setStretchFactor).toHaveBeenCalledWith(0.25)
    const [hist, dif, dea] = macdPane.seriesCreated
    expect(hist.setData.mock.lastCall?.[0]).toHaveLength(5)
    expect(hist.setData.mock.lastCall?.[0][0]).toMatchObject({ value: -1, color: 'rgba(18, 171, 52, 0.55)' })
    expect(hist.setData.mock.lastCall?.[0][1]).toMatchObject({ value: 2, color: 'rgba(235, 18, 52, 0.55)' })
    expect(dif.setData.mock.lastCall?.[0]).toHaveLength(5)
    expect(dea.setData.mock.lastCall?.[0]).toHaveLength(5)
    // RSI 线与 80/20 参考线。
    const rsi = rsiPane.seriesCreated[0]
    expect(rsi.setData.mock.lastCall?.[0]).toHaveLength(5)
    expect(rsi.setData.mock.lastCall?.[0][0]).toMatchObject({ value: 55 })
    expect(rsi.createPriceLine).toHaveBeenCalledWith(expect.objectContaining({ price: 80 }))
    expect(rsi.createPriceLine).toHaveBeenCalledWith(expect.objectContaining({ price: 20 }))
    // 指标消失 → 两窗格拆除。
    await wrapper.setProps({ indicators: [] })
    expect(mocks.chart.removePane).toHaveBeenCalledTimes(2)
    expect(mocks.panes).toHaveLength(0)
  })

  it('paints auction bands only on 1m and frames sealed limit bars in warning color', async () => {
    const day10 = { open: 9.9, high: 10.2, low: 9.8, close: 10, volume: 100 }
    // —— 色带：分钟图铺窗内的根（09:18∈竞价，09:31 窗外被滤），切日线立即清空 ——
    const minute = mount(NtKline, {
      props: {
        freq: '1m',
        bars: [
          { time: '2026-10-19 09:18', ...day10 },
          { time: '2026-10-19 09:31', ...day10 },
        ],
      },
    })
    wrappers.push(minute)
    expect(mocks.custom.setData.mock.lastCall?.[0]).toEqual([
      { time: chartTime('2026-10-19 09:18', '1m'), customValues: { band: 'morning' } },
    ])
    await minute.setProps({ freq: '1d' })
    expect(mocks.custom.setData.mock.lastCall?.[0]).toEqual([])
    minute.unmount()
    wrappers.pop()

    // —— 封板：琥珀描边+影线；未封板与普通涨幅保持系列默认 ——
    const wrapper = mount(NtKline, {
      props: {
        symbol: '600519.SH',
        bars: [
          { time: '2026-10-13', open: 9.9, high: 10.2, low: 9.8, close: 10, volume: 100 },
          { time: '2026-10-14', open: 10.5, high: 11, low: 10.4, close: 11, volume: 200 },
          { time: '2026-10-15', open: 11.2, high: 11.4, low: 11, close: 11.3, volume: 300 },
        ],
      },
    })
    wrappers.push(wrapper)
    const rows = mocks.candle.setData.mock.lastCall?.[0] as { close: number; borderColor?: string; wickColor?: string }[]
    expect(rows[0]?.borderColor).toBeUndefined()
    expect(rows[1]).toMatchObject({ close: 11, borderColor: '#edba32', wickColor: '#edba32' })
    expect(rows[2]?.borderColor).toBeUndefined()

    const api = wrapper.vm as unknown as {
      updateLast: (bar: { time: string; open: number; high: number; low: number; close: number; volume: number }) => boolean
    }
    // 新根封板：11.3×1.1=12.43 精确落在理论涨停价
    expect(api.updateLast({ time: '2026-10-16', open: 12.2, high: 12.43, low: 12.1, close: 12.43, volume: 400 })).toBe(true)
    expect(mocks.candle.update.mock.lastCall?.[0]).toMatchObject({ close: 12.43, borderColor: '#edba32', wickColor: '#edba32' })
    // 同根开板回落：描边回到系列默认
    expect(api.updateLast({ time: '2026-10-16', open: 12.2, high: 12.43, low: 12, close: 12.3, volume: 500 })).toBe(true)
    const replaced = mocks.candle.update.mock.lastCall?.[0] as { close: number; borderColor?: string }
    expect(replaced.close).toBe(12.3)
    expect(replaced.borderColor).toBeUndefined()
  })

  it('measure ruler draws via an attached primitive and reports directional stats', async () => {
    const bars = [10, 11, 12, 13, 14].map((close, index) => ({
      time: `2026-10-0${index + 5}`,
      open: close,
      high: close + 1,
      low: close - 1,
      close,
      volume: 100,
    }))
    const wrapper = mount(NtKline, { props: { bars, measure: true } })
    wrappers.push(wrapper)
    const zone = wrapper.element.querySelector('.nt-kline') as HTMLElement
    const fire = (type: string, x: number, y: number) => zone.dispatchEvent(new MouseEvent(type, { bubbles: true, clientX: x, clientY: y }))
    fire('pointerdown', 10, 100) // 逻辑索引 1、价格 10
    fire('pointermove', 30, 60) // 逻辑索引 3、价格 14
    fire('pointerup', 30, 60)
    await nextTick()

    expect(mocks.candle.attachPrimitive).toHaveBeenCalledTimes(1)
    const prim = mocks.candle.attachPrimitive.mock.calls[0][0] as {
      box: () => { fromLogical: number; toLogical: number; fromPrice: number; toPrice: number } | null
    }
    expect(prim.box()).toEqual({ fromLogical: 1, toLogical: 3, fromPrice: 10, toPrice: 14 })
    const chip = wrapper.find('.nt-kline__chip')
    expect(chip.exists()).toBe(true)
    expect(chip.text()).toContain('3 根')
    expect(chip.text()).toContain('+18.18%')
    expect(chip.text()).toContain('36.36%')

    // Esc 清框并摘下 primitive。
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(mocks.candle.detachPrimitive).toHaveBeenCalledTimes(1)
    expect(prim.box()).toBeNull()
    expect(wrapper.find('.nt-kline__chip').exists()).toBe(false)

    // 关闭开关后再拖不再产生新框。
    await wrapper.setProps({ measure: false })
    fire('pointerdown', 10, 100)
    expect(mocks.candle.attachPrimitive).toHaveBeenCalledTimes(1)
  })
})
