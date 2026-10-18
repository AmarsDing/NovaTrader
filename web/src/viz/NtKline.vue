<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch, type PropType } from 'vue'
import {
  CandlestickSeries,
  ColorType,
  createChart,
  createSeriesMarkers,
  createTextWatermark,
  CrosshairMode,
  HistogramSeries,
  LineSeries,
  LineStyle,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type ITextWatermarkPluginApi,
  type Time,
} from 'lightweight-charts'
import { colorWithAlpha, useChartPalette } from '@/theme/tokens'
import { chartTime } from '@/utils/format'
import type { Bar, MarketIndicator } from '@/api/market'

export interface KlineMarker {
  time: string | number
  position?: 'aboveBar' | 'belowBar' | 'inBar'
  color?: string
  shape?: 'arrowUp' | 'arrowDown' | 'circle' | 'square'
  text?: string
}

/** 图上水平参考线（成本/止损止盈/关键价）；price 非法或 ≤0 的条目不画。 */
export interface KlineLevel {
  price: number
  color?: string
  title?: string
  lineStyle?: 'solid' | 'dashed' | 'dotted'
}

const props = defineProps({
  bars: { type: Array as PropType<Bar[]>, default: () => [] },
  indicators: { type: Array as PropType<MarketIndicator[]>, default: () => [] },
  markers: { type: Array as PropType<KlineMarker[]>, default: () => [] },
  levels: { type: Array as PropType<KlineLevel[]>, default: () => [] },
  /** 图表中央的法式水印文本（通常传证券代码）；空串不画。 */
  watermark: { type: String, default: '' },
  freq: { type: String, default: '1d' },
  height: { type: String, default: '420px' },
})

const emit = defineEmits<{
  (e: 'crosshair', time: number | null, price: number | null): void
}>()

const el = ref<HTMLDivElement | null>(null)
const palette = useChartPalette()
let chart: IChartApi | null = null
let candle: ISeriesApi<'Candlestick'> | null = null
let volume: ISeriesApi<'Histogram'> | null = null
let ma5: ISeriesApi<'Line'> | null = null
let ma10: ISeriesApi<'Line'> | null = null
let ma20: ISeriesApi<'Line'> | null = null
/** v5：标记层是挂在 series 上的插件，不再是 series.setMarkers。 */
let markersApi: ISeriesMarkersPluginApi<Time> | null = null
/** 法式水印（pane 级 primitive）与价格参考线，随 apply()/主题切换整体重建。 */
let watermarkApi: ITextWatermarkPluginApi<Time> | null = null
let priceLines: IPriceLine[] = []

/** 增量更新的本地账本：收盘价序列（算 MA）与末根时间（判 新根/同根/旧帧）。 */
let closes: number[] = []
let lastTimeNum: number | null = null
/** fitContent 只跑一次 + 换 freq 再跑；用户缩放不再被数据回流重置。 */
let fittedFreq: string | null = null

function timeNum(time: string | number): number {
  if (typeof time === 'number') return time
  const parsed = Date.parse(time)
  return Number.isFinite(parsed) ? parsed : NaN
}

function seriesRows(): (Bar & { time: string | number })[] {
  const list = props.bars
    .map((bar) => {
      const time = chartTime(bar.time, props.freq)
      if (time == null || bar.open == null || bar.high == null || bar.low == null || bar.close == null) return null
      return { ...bar, time }
    })
    .filter((row): row is Bar & { time: string | number } => row !== null)
    .sort((a, b) => (a.time > b.time ? 1 : a.time < b.time ? -1 : 0))
  const unique: (Bar & { time: string | number })[] = []
  list.forEach((row) => {
    if (unique.length && unique[unique.length - 1].time === row.time) unique[unique.length - 1] = row
    else unique.push(row)
  })
  return unique
}

const CHART_FONT = 'Segoe UI, PingFang SC, Microsoft YaHei, sans-serif'
function watermarkOptions(text: string) {
  const label = text.trim()
  return {
    visible: label.length > 0,
    horzAlign: 'center' as const,
    vertAlign: 'center' as const,
    lines: label
      ? [{ text: label, color: colorWithAlpha(palette.value.text3, 0.28), fontSize: 44, fontFamily: CHART_FONT, fontStyle: 'bold' }]
      : [],
  }
}

const LEVEL_STYLE = { solid: LineStyle.Solid, dashed: LineStyle.Dashed, dotted: LineStyle.Dotted } as const
/** 价格参考线整体重建：lines 数量少（≤一手数），重建比逐条 diff 简单且无残留。 */
function applyLevels() {
  if (!candle) return
  priceLines.forEach((line) => candle!.removePriceLine(line))
  priceLines = []
  const seen = new Set<number>()
  props.levels.forEach((level) => {
    if (!Number.isFinite(level.price) || level.price <= 0 || seen.has(level.price)) return
    seen.add(level.price)
    priceLines.push(
      candle!.createPriceLine({
        price: level.price,
        color: level.color || palette.value.accent,
        lineWidth: 1,
        lineStyle: LEVEL_STYLE[level.lineStyle ?? 'dashed'],
        axisLabelVisible: true,
        title: level.title ?? '',
      }),
    )
  })
}

function apply(fitViewport = true) {
  if (!candle || !volume || !ma5 || !ma10 || !ma20) return
  const data = seriesRows()
  candle.setData(data.map(({ time, open, high, low, close }) => ({ time: time as Time, open, high, low, close })))
  volume.setData(
    data.map((row) => ({
      time: row.time as Time,
      value: row.volume || 0,
      color: colorWithAlpha((row.close ?? 0) >= (row.open ?? 0) ? palette.value.up : palette.value.down, 0.4),
    })),
  )
  const byTime = new Map<string | number, MarketIndicator>()
  props.indicators.forEach((point) => {
    const time = chartTime(point.time, props.freq)
    if (time != null) byTime.set(time, point)
  })
  const line = (series: ISeriesApi<'Line'>, key: keyof MarketIndicator) => {
    series.setData(
      data
        .map((row) => {
          const value = byTime.get(row.time)?.[key]
          return value == null ? null : { time: row.time as Time, value }
        })
        .filter((point): point is { time: Time; value: number } => point !== null),
    )
  }
  line(ma5, 'ma5')
  line(ma10, 'ma10')
  line(ma20, 'ma20')
  const marks = props.markers
    .map((mark) => {
      const time = chartTime(mark.time, props.freq)
      if (time == null) return null
      return {
        time: time as Time,
        position: mark.position || 'aboveBar',
        color: mark.color || palette.value.accent,
        shape: mark.shape || 'circle',
        text: mark.text || '',
      }
    })
    .filter((mark): mark is NonNullable<typeof mark> => mark !== null)
    .sort((a, b) => (a.time > b.time ? 1 : -1))
  markersApi?.setMarkers(marks)
  watermarkApi?.applyOptions(watermarkOptions(props.watermark))
  applyLevels()
  // 全量套用后签收增量账本，updateLast 从此接着走。
  closes = data.map((row) => row.close).filter((close): close is number => close != null)
  lastTimeNum = data.length ? timeNum(data[data.length - 1].time) : null
  if (fitViewport && data.length && fittedFreq !== props.freq) {
    fittedFreq = props.freq
    chart?.timeScale().fitContent()
  }
}

function updateMa(time: Time) {
  const mean = (n: number): number | null => {
    if (closes.length < n) return null
    let sum = 0
    for (let i = closes.length - n; i < closes.length; i++) sum += closes[i]
    return sum / n
  }
  const rows: [ISeriesApi<'Line'> | null, number][] = [
    [ma5, 5],
    [ma10, 10],
    [ma20, 20],
  ]
  rows.forEach(([series, n]) => {
    const value = mean(n)
    if (series && value != null) series.update({ time, value })
  })
}

/**
 * 推送帧直接喂末根：同根替换、新根追加、旧帧忽略。
 * 只做 update()，不 setData、不动视口；MA 用本地收盘价即时续算。
 */
function updateLast(bar: Bar): boolean {
  if (!candle || !volume) return false
  const time = chartTime(bar.time, props.freq)
  if (time == null || bar.open == null || bar.high == null || bar.low == null || bar.close == null) return false
  const keyNum = timeNum(time)
  if (!Number.isFinite(keyNum)) return false
  if (lastTimeNum != null && keyNum < lastTimeNum) return false
  candle.update({ time: time as Time, open: bar.open, high: bar.high, low: bar.low, close: bar.close })
  volume.update({
    time: time as Time,
    value: bar.volume || 0,
    color: colorWithAlpha(bar.close >= bar.open ? palette.value.up : palette.value.down, 0.4),
  })
  if (lastTimeNum != null && keyNum === lastTimeNum && closes.length) closes[closes.length - 1] = bar.close
  else closes.push(bar.close)
  lastTimeNum = keyNum
  // 盘中一天最多几百根，账本封顶防长会话漂移；超限走下一次全量重载对齐。
  if (closes.length > 2000) closes.splice(0, closes.length - 2000)
  updateMa(time as Time)
  return true
}

/** 显式复位视口（切 symbol / 手动读取调用）；数据回流不再隐式重置。 */
function refit() {
  fittedFreq = props.freq
  chart?.timeScale().fitContent()
}

defineExpose({ updateLast, refit })

function applyTheme() {
  const p = palette.value
  chart?.applyOptions({
    layout: { textColor: p.text2 },
    grid: { vertLines: { color: p.border1 }, horzLines: { color: p.border1 } },
    crosshair: { vertLine: { color: p.accent2 }, horzLine: { color: p.accent2 } },
    rightPriceScale: { borderColor: p.border2 },
    timeScale: { borderColor: p.border2 },
  })
  candle?.applyOptions({
    upColor: p.up,
    downColor: p.down,
    borderUpColor: p.up,
    borderDownColor: p.down,
    wickUpColor: p.up,
    wickDownColor: p.down,
  })
  ma5?.applyOptions({ color: p.accent })
  ma10?.applyOptions({ color: p.accent2 })
  ma20?.applyOptions({ color: p.warn })
}

onMounted(() => {
  if (!el.value) return
  chart = createChart(el.value, {
    autoSize: true,
    layout: {
      background: { type: ColorType.Solid, color: 'transparent' },
      textColor: palette.value.text2,
      fontFamily: CHART_FONT,
      fontSize: 10,
    },
    grid: {
      vertLines: { color: palette.value.border1 },
      horzLines: { color: palette.value.border1 },
    },
    // 磁吸 OHLC：十字线贴到最近 K 线的开高低收，信号标注精确对价。
    crosshair: { mode: CrosshairMode.MagnetOHLC },
    rightPriceScale: { borderColor: palette.value.border2 },
    timeScale: { borderColor: palette.value.border2 },
  })
  watermarkApi = createTextWatermark(chart.panes()[0], watermarkOptions(props.watermark))
  candle = chart.addSeries(CandlestickSeries, {
    upColor: palette.value.up,
    downColor: palette.value.down,
    borderUpColor: palette.value.up,
    borderDownColor: palette.value.down,
    wickUpColor: palette.value.up,
    wickDownColor: palette.value.down,
  })
  markersApi = createSeriesMarkers(candle, [])
  volume = chart.addSeries(HistogramSeries, {
    priceFormat: { type: 'volume' },
    priceScaleId: 'vol',
  })
  chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.8, bottom: 0 } })
  const lineOpts = { lineWidth: 1, priceLineVisible: false, lastValueVisible: false } as const
  ma5 = chart.addSeries(LineSeries, { ...lineOpts, color: palette.value.accent })
  ma10 = chart.addSeries(LineSeries, { ...lineOpts, color: palette.value.accent2 })
  ma20 = chart.addSeries(LineSeries, { ...lineOpts, color: palette.value.warn })
  chart.subscribeCrosshairMove((param) => {
    const data = candle ? (param.seriesData.get(candle) as { close?: number } | undefined) : undefined
    emit('crosshair', (param.time as number) ?? null, data?.close ?? null)
  })
  applyTheme()
  apply()
})

// 非 deep 监听：数组整体替换（首载/重载/切 symbol/freq）才全量套用；
// 盘中 tick 在同一个引用上原地改，由父级走 updateLast 增量通道。
watch(
  () => [props.bars, props.indicators, props.markers, props.levels, props.watermark, props.freq] as const,
  () => apply(),
)

watch(palette, () => {
  applyTheme()
  // A color switch must preserve the user's zoom and visible time window.
  apply(false)
})

onBeforeUnmount(() => {
  markersApi = null
  watermarkApi = null
  priceLines = []
  chart?.remove()
  chart = null
})
</script>

<template>
  <div ref="el" class="nt-kline" :style="{ height }" />
</template>

<style scoped>
.nt-kline {
  width: 100%;
}
</style>
