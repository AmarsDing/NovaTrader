<script setup lang="ts">
import { computed } from 'vue'
import NtEChart from './NtEChart.vue'
import { PALETTE } from '@/theme/tokens'
import type { EChartsCoreOption } from 'echarts/core'

export interface IndicatorPoint {
  time: string
  macd: number | null
  rsi6: number | null
}

const props = withDefaults(
  defineProps<{
    points?: IndicatorPoint[]
    height?: string
  }>(),
  { points: () => [], height: '220px' },
)

const option = computed<EChartsCoreOption | null>(() => {
  if (!props.points.length) return null
  const axis = (extra: Record<string, unknown> = {}) => ({
    axisLabel: { color: PALETTE.text3, fontSize: 10 },
    axisLine: { lineStyle: { color: PALETTE.border2 } },
    splitLine: { lineStyle: { color: PALETTE.border1 } },
    ...extra,
  })
  return {
    animation: false,
    tooltip: { trigger: 'axis' },
    legend: { data: ['MACD', 'RSI6'], textStyle: { color: PALETTE.text2, fontSize: 11 } },
    grid: [
      { left: 48, right: 16, top: 28, height: '38%' },
      { left: 48, right: 16, top: '62%', height: '24%' },
    ],
    xAxis: [
      { type: 'category', data: props.points.map((point) => point.time), gridIndex: 0, ...axis() },
      { type: 'category', data: props.points.map((point) => point.time), gridIndex: 1, ...axis() },
    ],
    yAxis: [{ gridIndex: 0, scale: true, ...axis() }, { gridIndex: 1, scale: true, ...axis() }],
    series: [
      {
        name: 'MACD',
        type: 'bar',
        xAxisIndex: 0,
        yAxisIndex: 0,
        data: props.points.map((point) => ({
          value: point.macd,
          // 红涨绿跌：MACD 正红负绿
          itemStyle: { color: (point.macd ?? 0) >= 0 ? PALETTE.up : PALETTE.down },
        })),
      },
      {
        name: 'RSI6',
        type: 'line',
        xAxisIndex: 1,
        yAxisIndex: 1,
        showSymbol: false,
        data: props.points.map((point) => point.rsi6),
        lineStyle: { color: PALETTE.warn },
      },
    ],
    textStyle: { color: PALETTE.text2 },
  }
})
</script>

<template>
  <NtEChart v-if="option" :option="option" :height="height" />
</template>
