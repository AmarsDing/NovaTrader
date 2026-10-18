<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch, type PropType } from 'vue'
import { ColorType, createChart, CrosshairMode, type IChartApi, type ISeriesApi, type Time } from 'lightweight-charts'
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

const props = defineProps({
  bars: { type: Array as PropType<Bar[]>, default: () => [] },
  indicators: { type: Array as PropType<MarketIndicator[]>, default: () => [] },
  markers: { type: Array as PropType<KlineMarker[]>, default: () => [] },
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
  candle.setMarkers(marks)
  if (fitViewport && data.length) chart?.timeScale().fitContent()
}

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
      fontFamily: 'Segoe UI, PingFang SC, Microsoft YaHei, sans-serif',
      fontSize: 10,
    },
    grid: {
      vertLines: { color: palette.value.border1 },
      horzLines: { color: palette.value.border1 },
    },
    crosshair: { mode: CrosshairMode.Normal },
    rightPriceScale: { borderColor: palette.value.border2 },
    timeScale: { borderColor: palette.value.border2 },
  })
  candle = chart.addCandlestickSeries({
    upColor: palette.value.up,
    downColor: palette.value.down,
    borderUpColor: palette.value.up,
    borderDownColor: palette.value.down,
    wickUpColor: palette.value.up,
    wickDownColor: palette.value.down,
  })
  volume = chart.addHistogramSeries({
    priceFormat: { type: 'volume' },
    priceScaleId: 'vol',
  })
  chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.8, bottom: 0 } })
  const lineOpts = { lineWidth: 1, priceLineVisible: false, lastValueVisible: false } as const
  ma5 = chart.addLineSeries({ ...lineOpts, color: palette.value.accent })
  ma10 = chart.addLineSeries({ ...lineOpts, color: palette.value.accent2 })
  ma20 = chart.addLineSeries({ ...lineOpts, color: palette.value.warn })
  chart.subscribeCrosshairMove((param) => {
    const data = candle ? (param.seriesData.get(candle) as { close?: number } | undefined) : undefined
    emit('crosshair', (param.time as number) ?? null, data?.close ?? null)
  })
  applyTheme()
  apply()
})

watch(
  () => [props.bars, props.indicators, props.markers, props.freq] as const,
  () => apply(),
  { deep: true },
)

watch(palette, () => {
  applyTheme()
  // A color switch must preserve the user's zoom and visible time window.
  apply(false)
})

onBeforeUnmount(() => {
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
