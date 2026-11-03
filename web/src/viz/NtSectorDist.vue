<script setup lang="ts">
import { computed } from 'vue'
import NtEChart from './NtEChart.vue'
import { useChartPalette } from '@/theme/tokens'
import type { EChartsCoreOption } from 'echarts/core'

/** 板块分布行：name 板块名（「未归属」「其他」为合成桶），count 持仓+候选落入次数。 */
export interface SectorDistRow {
  name: string
  count: number
}

const props = withDefaults(
  defineProps<{
    rows?: SectorDistRow[]
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
    grid: { left: 72, right: 24, top: 16, bottom: 24 },
    xAxis: {
      type: 'value',
      minInterval: 1,
      axisLabel: { color: palette.value.text3, fontSize: 10 },
      axisLine: { lineStyle: { color: palette.value.border2 } },
      splitLine: { lineStyle: { color: palette.value.border1 } },
    },
    yAxis: {
      type: 'category',
      // echarts category 轴首行在底部：反转让计数最高的板块显示在最上方
      data: props.rows.map((row) => row.name).reverse(),
      axisLabel: { color: palette.value.text2, fontSize: 11 },
      axisLine: { lineStyle: { color: palette.value.border2 } },
      splitLine: { lineStyle: { color: palette.value.border1 } },
    },
    series: [
      {
        type: 'bar',
        // 分布计数无涨跌语义：统一主色，「未归属」桶用弱化色提示数据缺口
        data: props.rows
          .map((row) => ({
            value: row.count,
            itemStyle: {
              color: row.name === '未归属' ? palette.value.text3 : palette.value.accent,
            },
          }))
          .reverse(),
      },
    ],
    textStyle: { color: palette.value.text2 },
  }
})
</script>

<template>
  <NtEChart v-if="option" :option="option" :height="height" />
  <!-- 持仓与候选均为空或归属接口未返回：显态占位，不渲染空气 -->
  <p v-else class="nt-empty" :style="{ height }">暂无持仓与候选的板块归属数据</p>
</template>

<style scoped>
.nt-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0;
}
</style>
