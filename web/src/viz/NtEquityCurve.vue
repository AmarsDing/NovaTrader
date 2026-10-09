<script setup lang="ts">
import { computed } from 'vue'
import NtEChart from './NtEChart.vue'
import { PALETTE } from '@/theme/tokens'
import type { EquityPoint } from '@/api/backtest'
import type { EChartsCoreOption } from 'echarts/core'

const props = withDefaults(
  defineProps<{
    points?: EquityPoint[]
    height?: string
  }>(),
  { points: () => [], height: '300px' },
)

const option = computed<EChartsCoreOption | null>(() => {
  if (!props.points.length) return null
  const days = props.points.map((p) => p.day)
  return {
    animation: false,
    grid: { top: 24, right: 16, bottom: 24, left: 52 },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(3) : '—'),
    },
    legend: { top: 0, textStyle: { color: PALETTE.text3, fontSize: 11 }, itemWidth: 14 },
    xAxis: {
      type: 'category',
      data: days,
      axisLine: { lineStyle: { color: PALETTE.border2 } },
      axisLabel: { color: PALETTE.text3, fontSize: 10 },
    },
    yAxis: {
      type: 'value',
      scale: true,
      splitLine: { lineStyle: { color: 'rgba(255,255,255,0.06)' } },
      axisLabel: { color: PALETTE.text3, fontSize: 10 },
    },
    series: [
      {
        name: '策略权益',
        type: 'line',
        showSymbol: false,
        data: props.points.map((p) => p.equity),
        lineStyle: { width: 1.6, color: PALETTE.accent },
        areaStyle: { color: 'rgba(0,229,255,0.08)' },
      },
      {
        name: '基准',
        type: 'line',
        showSymbol: false,
        data: props.points.map((p) => p.benchmark),
        lineStyle: { width: 1.2, color: PALETTE.text3 },
      },
    ],
  }
})
</script>

<template>
  <NtEChart v-if="option" :option="option" :height="height" />
  <p v-else class="nt-echart-empty">报告未返回权益序列</p>
</template>

<style scoped>
.nt-echart-empty {
  margin: 0;
  padding: 16px;
  font-size: 12px;
  color: var(--nt-text-3);
}
</style>
