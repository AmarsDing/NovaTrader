<script setup lang="ts">
import { computed } from 'vue'
import NtEChart from './NtEChart.vue'
import { PALETTE } from '@/theme/tokens'
import type { EChartsCoreOption } from 'echarts/core'

export interface HeatCell {
  x: string
  y: string
  value: number | null
}

const props = withDefaults(
  defineProps<{
    cells?: HeatCell[]
    xLabels?: string[]
    yLabels?: string[]
    height?: string
  }>(),
  { cells: () => [], xLabels: () => [], yLabels: () => [], height: '240px' },
)

const option = computed<EChartsCoreOption | null>(() => {
  if (!props.cells.length || !props.xLabels.length || !props.yLabels.length) return null
  const xs = props.xLabels
  const ys = props.yLabels
  const data = props.cells.map((cell) => [xs.indexOf(cell.x), ys.indexOf(cell.y), cell.value ?? null])
  const values = props.cells.map((c) => c.value).filter((v): v is number => typeof v === 'number')
  const vmax = Math.max(1, ...values.map((v) => Math.abs(v)))
  return {
    animation: false,
    grid: { top: 8, right: 12, bottom: 8, left: 56, containLabel: true },
    tooltip: { position: 'top' },
    xAxis: {
      type: 'category',
      data: xs,
      axisLabel: { color: PALETTE.text3, fontSize: 10 },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'category',
      data: ys,
      axisLabel: { color: PALETTE.text3, fontSize: 10 },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    visualMap: {
      show: false,
      min: -vmax,
      max: vmax,
      inRange: { color: [PALETTE.down, PALETTE.bg1, PALETTE.up] },
    },
    series: [
      {
        type: 'heatmap',
        data,
        label: { show: false },
        itemStyle: { borderColor: PALETTE.bg0, borderWidth: 1 },
      },
    ],
  }
})
</script>

<template>
  <NtEChart v-if="option" :option="option" :height="height" />
  <p v-else class="nt-heatmap-empty">暂无热力数据</p>
</template>

<style scoped>
.nt-heatmap-empty {
  margin: 0;
  padding: 16px;
  font-size: 12px;
  color: var(--nt-text-3);
}
</style>
