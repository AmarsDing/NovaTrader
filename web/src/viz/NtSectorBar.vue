<script setup lang="ts">
import { computed } from 'vue'
import NtEChart from './NtEChart.vue'
import { useChartPalette } from '@/theme/tokens'
import type { EChartsCoreOption } from 'echarts/core'

export interface SectorRow {
  name: string
  avgPct: number | null
}

const props = withDefaults(
  defineProps<{
    rows?: SectorRow[]
    height?: string
  }>(),
  { rows: () => [], height: '280px' },
)

const palette = useChartPalette()

const option = computed<EChartsCoreOption | null>(() => {
  if (!props.rows.length) return null
  return {
    animation: false,
    tooltip: { trigger: 'axis' },
    grid: { left: 72, right: 16, top: 16, bottom: 24 },
    xAxis: {
      type: 'value',
      axisLabel: { color: palette.value.text3, fontSize: 10 },
      axisLine: { lineStyle: { color: palette.value.border2 } },
      splitLine: { lineStyle: { color: palette.value.border1 } },
    },
    yAxis: {
      type: 'category',
      data: props.rows.map((row) => row.name),
      axisLabel: { color: palette.value.text2, fontSize: 11 },
      axisLine: { lineStyle: { color: palette.value.border2 } },
      splitLine: { lineStyle: { color: palette.value.border1 } },
    },
    series: [
      {
        type: 'bar',
        data: props.rows.map((row) => ({
          value: row.avgPct,
          // 红涨绿跌：涨红、跌绿
          itemStyle: { color: (row.avgPct ?? 0) >= 0 ? palette.value.up : palette.value.down },
        })),
      },
    ],
    textStyle: { color: palette.value.text2 },
  }
})
</script>

<template>
  <NtEChart v-if="option" :option="option" :height="height" />
</template>
