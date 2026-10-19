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
  type IPaneApi,
  type IPriceLine,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type ITextWatermarkPluginApi,
  type Time,
} from 'lightweight-charts'
import { colorWithAlpha, useChartPalette } from '@/theme/tokens'
import { chartTime, price } from '@/utils/format'
import { limitState } from '@/utils/limit'
import { createMeasurePrimitive, measureStats, type MeasurePrimitive, type MeasureStats } from './measure-box'
import { createSessionBandPaneView, sessionBandData, type BandSeriesApi } from './session-bands'
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
  /** 证券代码：推断涨跌幅档做「封板」高亮；空串不推断。与 watermark 各自独立传参。 */
  symbol: { type: String, default: '' },
  freq: { type: String, default: '1d' },
  /** 框选测量开关：true 时图上拖拽画测量框（measure-box primitive）。 */
  measure: { type: Boolean, default: false },
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

/** 副图（v5 pane）：MACD 柱+DIF/DEA 线一窗，RSI6 一窗；随指标数据有无建/拆。 */
let macdPane: IPaneApi<Time> | null = null
let rsiPane: IPaneApi<Time> | null = null
let macdHist: ISeriesApi<'Histogram'> | null = null
let difLine: ISeriesApi<'Line'> | null = null
let deaLine: ISeriesApi<'Line'> | null = null
let rsiLine: ISeriesApi<'Line'> | null = null
/** 集合竞价色带序列（v5 custom series）：挂载即建（保证最底层），数据按 freq 在 apply() 维护。 */
let bandSeries: BandSeriesApi | null = null

/** 色带颜色在 draw 时实时读：换主题不需要重建序列，下一次重绘自动吃新 token。 */
function bandColors() {
  const p = palette.value
  return {
    morning: colorWithAlpha(p.accent, 0.07),
    quiet: colorWithAlpha(p.text3, 0.06),
    closing: colorWithAlpha(p.accent2, 0.07),
  }
}

/** 封板行的单项着色：琥珀描边+影线（实体仍吃系列默认的红涨绿跌）。 */
function limitFrame(close: number | null, high: number | null, low: number | null, prevClose: number | null) {
  return limitState(close, high, low, prevClose, props.symbol)
    ? { borderColor: palette.value.warn, wickColor: palette.value.warn }
    : {}
}

/** —— 框选测量：拖拽状态在组件侧，画框走 measure-box primitive，读数走 overlay chip。 —— */
let measurePrim: MeasurePrimitive | null = null
const measureReadout = ref<MeasureStats | null>(null)
/** 最近一次 apply() 的全量行：行数组下标 = lwc 逻辑索引，测量统计按它对齐。 */
let lastRows: (Bar & { time: string | number })[] = []
let measureAnchor: { logical: number; price: number } | null = null

function measureStyle() {
  const p = palette.value
  return { fill: colorWithAlpha(p.accent, 0.08), stroke: colorWithAlpha(p.accent, 0.55) }
}

function clearMeasure() {
  measureAnchor = null
  measureReadout.value = null
  if (measurePrim && candle) candle.detachPrimitive(measurePrim)
  measurePrim = null
}

/** 指针位置 → (逻辑索引, 价格)；越出刻度区返回 null（本次移动不更新框）。 */
function measurePoint(event: PointerEvent): { logical: number; price: number } | null {
  if (!chart || !candle || !el.value) return null
  const frame = el.value.getBoundingClientRect()
  const logical = chart.timeScale().coordinateToLogical(event.clientX - frame.left)
  const rawPrice = candle.coordinateToPrice(event.clientY - frame.top)
  const point = rawPrice == null ? NaN : Number(rawPrice)
  if (logical == null || !Number.isFinite(point)) return null
  return { logical: logical as number, price: point }
}

function pushMeasure(point: { logical: number; price: number }) {
  if (!measureAnchor) return
  if (!measurePrim && candle) {
    measurePrim = createMeasurePrimitive(measureStyle)
    candle.attachPrimitive(measurePrim)
  }
  measurePrim?.setBox({
    fromLogical: measureAnchor.logical,
    toLogical: point.logical,
    fromPrice: measureAnchor.price,
    toPrice: point.price,
  })
  measureReadout.value = measureStats(lastRows, measureAnchor.logical, point.logical)
}

// 测量拖拽用捕获相拦截：lwc 的平移/缩放手势挂在内层 canvas 上，bubble 相抢不过。
function onMeasureDown(event: PointerEvent) {
  if (!props.measure) return
  const point = measurePoint(event)
  if (!point) return
  event.stopPropagation()
  event.preventDefault()
  measureAnchor = point
  pushMeasure(point)
}

function onMeasureMove(event: PointerEvent) {
  if (!measureAnchor) return
  const point = measurePoint(event)
  if (!point) return
  event.stopPropagation()
  pushMeasure(point)
}

function onMeasureUp(event: PointerEvent) {
  if (!measureAnchor) return
  event.stopPropagation()
  measureAnchor = null
}

function onMeasureKey(event: KeyboardEvent) {
  if (event.key === 'Escape' && props.measure) clearMeasure()
}

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

function ensureSubPanes() {
  if (!chart || macdPane) return
  const p = palette.value
  macdPane = chart.addPane()
  macdPane.setStretchFactor(0.35)
  macdHist = macdPane.addSeries(HistogramSeries, {})
  difLine = macdPane.addSeries(LineSeries, { lineWidth: 1, color: p.accent2, priceLineVisible: false, lastValueVisible: false })
  deaLine = macdPane.addSeries(LineSeries, { lineWidth: 1, color: p.warn, priceLineVisible: false, lastValueVisible: false })
  rsiPane = chart.addPane()
  rsiPane.setStretchFactor(0.25)
  rsiLine = rsiPane.addSeries(LineSeries, { lineWidth: 1, color: p.accent, priceLineVisible: false })
  const guide = { color: colorWithAlpha(p.text3, 0.5), lineWidth: 1 as const, lineStyle: LineStyle.Dotted, axisLabelVisible: true, title: '' }
  rsiLine.createPriceLine({ price: 80, ...guide })
  rsiLine.createPriceLine({ price: 20, ...guide })
}

/** 拆副图：先拆高索引 pane（指数实时取，防错位）。 */
function destroySubPanes() {
  if (!chart || !macdPane) return
  if (rsiPane) chart.removePane(rsiPane.paneIndex())
  chart.removePane(macdPane.paneIndex())
  macdPane = rsiPane = macdHist = difLine = deaLine = rsiLine = null
}

/** 指标行 → 副图序列；mac/dif/dea/rsi6 全空的指标数据视为无副图。 */
function feedSubPanes(data: (Bar & { time: string | number })[], byTime: Map<string | number, MarketIndicator>) {
  const hasSub = props.indicators.some((point) => point.macd != null || point.dif != null || point.dea != null || point.rsi6 != null)
  if (!hasSub) {
    destroySubPanes()
    return
  }
  ensureSubPanes()
  const rows = (key: 'dif' | 'dea' | 'rsi6') =>
    data
      .map((row) => {
        const value = byTime.get(row.time)?.[key]
        return value == null ? null : { time: row.time as Time, value }
      })
      .filter((point): point is { time: Time; value: number } => point !== null)
  macdHist?.setData(
    data
      .map((row) => {
        const value = byTime.get(row.time)?.macd
        if (value == null) return null
        return { time: row.time as Time, value, color: colorWithAlpha(value >= 0 ? palette.value.up : palette.value.down, 0.55) }
      })
      .filter((point): point is { time: Time; value: number; color: string } => point !== null),
  )
  difLine?.setData(rows('dif'))
  deaLine?.setData(rows('dea'))
  rsiLine?.setData(rows('rsi6'))
}

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
  lastRows = data
  candle.setData(
    data.map((row, index) => ({
      time: row.time as Time,
      open: row.open,
      high: row.high,
      low: row.low,
      close: row.close,
      ...limitFrame(row.close, row.high, row.low, index > 0 ? data[index - 1].close : null),
    })),
  )
  // 分钟图才铺集合竞价色带；切回日线立即清空，不残留。
  bandSeries?.setData(props.freq === '1m' ? sessionBandData(data.map((row) => row.time)) : [])
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
  feedSubPanes(data, byTime)
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
  // 封板判定的昨收：同根替换取前一根、新根追加取当前末根；须在 closes 账本更新前算。
  const prevClose =
    lastTimeNum != null && keyNum === lastTimeNum
      ? closes.length >= 2
        ? closes[closes.length - 2]
        : null
      : closes.length
        ? closes[closes.length - 1]
        : null
  candle.update({
    time: time as Time,
    open: bar.open,
    high: bar.high,
    low: bar.low,
    close: bar.close,
    ...limitFrame(bar.close, bar.high, bar.low, prevClose),
  })
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
    layout: {
      textColor: p.text2,
      panes: { separatorColor: p.border1, separatorHoverColor: colorWithAlpha(p.accent, 0.25) },
    },
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
      panes: { separatorColor: palette.value.border1, separatorHoverColor: colorWithAlpha(palette.value.accent, 0.25) },
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
  // 色带必须第一个建：v5 序列按创建顺序分层（无 zIndex），先建者渲染在最底层。
  bandSeries = chart.addCustomSeries(createSessionBandPaneView(bandColors))
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
  // 测量手势用捕获相绑定（先于 lwc 自己的输入处理触发）。
  el.value.addEventListener('pointerdown', onMeasureDown, true)
  el.value.addEventListener('pointermove', onMeasureMove, true)
  el.value.addEventListener('pointerup', onMeasureUp, true)
  window.addEventListener('keydown', onMeasureKey)
})

// 非 deep 监听：数组整体替换（首载/重载/切 symbol/freq）才全量套用；
// 盘中 tick 在同一个引用上原地改，由父级走 updateLast 增量通道。
watch(
  () => [props.bars, props.indicators, props.markers, props.levels, props.watermark, props.symbol, props.freq] as const,
  () => apply(),
)

watch(palette, () => {
  applyTheme()
  // 副图序列颜色在建图时固化，换主题先拆掉，下面的 apply(false) 按新色重建。
  destroySubPanes()
  // A color switch must preserve the user's zoom and visible time window.
  apply(false)
})

watch(
  () => props.measure,
  (on) => {
    if (!on) clearMeasure()
  },
)

onBeforeUnmount(() => {
  el.value?.removeEventListener('pointerdown', onMeasureDown, true)
  el.value?.removeEventListener('pointermove', onMeasureMove, true)
  el.value?.removeEventListener('pointerup', onMeasureUp, true)
  window.removeEventListener('keydown', onMeasureKey)
  clearMeasure()
  markersApi = null
  watermarkApi = null
  priceLines = []
  destroySubPanes()
  bandSeries = null
  chart?.remove()
  chart = null
})
</script>

<template>
  <div class="nt-kline-frame" :style="{ height }">
    <div ref="el" class="nt-kline" />
    <div v-if="measureReadout" class="nt-kline__chip">
      <b>{{ measureReadout.bars }} 根</b>
      <span :style="{ color: measureReadout.delta >= 0 ? palette.up : palette.down }">
        {{ measureReadout.delta >= 0 ? '+' : '' }}{{ price(measureReadout.delta) }}
        ({{ measureReadout.deltaPct >= 0 ? '+' : '' }}{{ measureReadout.deltaPct.toFixed(2) }}%)
      </span>
      <span class="nt-kline__chip-dim">振幅 {{ measureReadout.amplitudePct.toFixed(2) }}%</span>
      <button type="button" class="nt-kline__chip-clear" aria-label="清除测量" @click="clearMeasure">×</button>
    </div>
  </div>
</template>

<style scoped>
.nt-kline-frame {
  position: relative;
  width: 100%;
}

.nt-kline {
  width: 100%;
  height: 100%;
}

/* 测量读数气泡：bg-1 背板 + mono 数字，数据区可读性合同内 */
.nt-kline__chip {
  position: absolute;
  top: 8px;
  left: 8px;
  z-index: 5;
  display: flex;
  gap: 10px;
  align-items: baseline;
  padding: 4px 10px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-m);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11.5px;
  color: var(--nt-text-2);
}

.nt-kline__chip b {
  color: var(--nt-text-1);
  font-weight: 600;
}

.nt-kline__chip-dim {
  color: var(--nt-text-3);
}

.nt-kline__chip-clear {
  margin-left: 2px;
  padding: 0 4px;
  color: var(--nt-text-3);
  background: none;
  border: none;
  cursor: pointer;
  font-size: 13px;
  line-height: 1;
}

.nt-kline__chip-clear:hover {
  color: var(--nt-text-1);
}
</style>
