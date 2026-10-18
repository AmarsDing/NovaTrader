<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch, type PropType } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart, HeatmapChart, LineChart, PieChart, RadarChart } from 'echarts/charts'
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TitleComponent,
  ToolboxComponent,
  TooltipComponent,
  VisualMapComponent,
  type TooltipComponentOption,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { useChartPalette } from '@/theme/tokens'
import { useVisualRendering } from '@/theme/visual-quality'

echarts.use([
  LineChart,
  BarChart,
  PieChart,
  RadarChart,
  HeatmapChart,
  TitleComponent,
  TooltipComponent,
  GridComponent,
  LegendComponent,
  ToolboxComponent,
  DataZoomComponent,
  VisualMapComponent,
  CanvasRenderer,
])

const props = defineProps({
  option: { type: Object as PropType<echarts.EChartsCoreOption | null>, default: null },
  height: { type: String, default: '240px' },
})

const el = ref<HTMLDivElement | null>(null)
const palette = useChartPalette()
const visual = useVisualRendering(el)
let chart: echarts.ECharts | null = null
let observer: ResizeObserver | null = null

type MotionSeries = { id?: string | number; animation?: boolean; [key: string]: unknown }

function motionOption(includeData: boolean) {
  const allowed = visual.value.visible && !visual.value.reducedMotion
  const rootAnimation = typeof props.option?.animation === 'boolean' ? props.option.animation : true
  const raw = props.option?.series as MotionSeries | MotionSeries[] | undefined
  const series = raw == null ? undefined : (Array.isArray(raw) ? raw : [raw]).map((item) => ({
    ...(includeData ? item : item.id == null ? {} : { id: item.id }),
    animation: allowed && (item.animation ?? rootAnimation),
  }))
  return { animation: allowed && rootAnimation, ...(series ? { series } : {}) }
}

function render() {
  if (!chart) return
  if (!props.option) {
    chart.clear()
    return
  }
  const p = palette.value
  const tooltipTheme = { backgroundColor: p.bg2, borderColor: p.border2, textStyle: { color: p.text1 } }
  const tooltip = props.option.tooltip as TooltipComponentOption | TooltipComponentOption[] | undefined
  chart.setOption({
    color: [p.accent, p.accent2, p.warn, p.info, p.success, p.danger],
    ...props.option,
    ...motionOption(true),
    textStyle: { color: p.text2, ...props.option.textStyle },
    tooltip: Array.isArray(tooltip)
      ? tooltip.map((item) => ({ ...tooltipTheme, ...item, textStyle: { color: p.text1, ...item.textStyle } }))
      : { ...tooltipTheme, ...tooltip, textStyle: { color: p.text1, ...tooltip?.textStyle } },
  }, true)
}

onMounted(() => {
  if (!el.value) return
  chart = echarts.init(el.value, null, { renderer: 'canvas' })
  render()
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(el.value)
})

watch(() => props.option, render)
watch(palette, render)
watch(visual, () => {
  if (!chart || !props.option) return
  // Merge only motion flags; preserve dataZoom and all user navigation state.
  chart.setOption(motionOption(false))
  if (visual.value.reducedMotion || !visual.value.visible) {
    const renderer = chart.getZr?.()
    renderer?.storage.getDisplayList().forEach((item) => item.stopAnimation(undefined, true))
    renderer?.refreshImmediately()
  }
})

onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.dispose()
  chart = null
})
</script>

<template>
  <div ref="el" class="nt-echart" :style="{ height }" />
</template>

<style scoped>
.nt-echart {
  width: 100%;
}
</style>
