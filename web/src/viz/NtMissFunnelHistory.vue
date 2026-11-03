<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import NtEChart from './NtEChart.vue'
import { useChartPalette } from '@/theme/tokens'
import { getMissFunnel, type MissFunnelDayItem } from '@/api/strategy'
import type { EChartsCoreOption } from 'echarts/core'

/** BE-12 候选未入选漏斗历史看板：近 N 日「候选 vs 未入选」柱图 + 最近一日未入选原因 Top。 */
const props = withDefaults(defineProps<{ days?: number; height?: string }>(), {
  days: 7,
  height: '220px',
})

const rows = ref<MissFunnelDayItem[]>([])
const error = ref('')
const loading = ref(true)
const palette = useChartPalette()

const option = computed<EChartsCoreOption | null>(() => {
  if (!rows.value.length) return null
  return {
    animation: false,
    tooltip: { trigger: 'axis' },
    legend: {
      data: ['候选', '未入选'],
      textStyle: { color: palette.value.text2, fontSize: 11 },
      top: 0,
    },
    grid: { left: 44, right: 16, top: 28, bottom: 24 },
    xAxis: {
      type: 'category',
      data: rows.value.map((d) => d.tradeDate.slice(5)),
      axisLabel: { color: palette.value.text3, fontSize: 10 },
      axisLine: { lineStyle: { color: palette.value.border2 } },
    },
    yAxis: {
      type: 'value',
      minInterval: 1,
      axisLabel: { color: palette.value.text3, fontSize: 10 },
      splitLine: { lineStyle: { color: palette.value.border1 } },
    },
    series: [
      {
        name: '候选',
        type: 'bar',
        data: rows.value.map((d) => d.total),
        itemStyle: { color: palette.value.accent },
      },
      {
        name: '未入选',
        type: 'bar',
        data: rows.value.map((d) => d.missed),
        itemStyle: { color: palette.value.down },
      },
    ],
    textStyle: { color: palette.value.text2 },
  }
})

/** 最近一个有数据日的未入选原因（取窗口最后一天）。 */
const latestReasons = computed(() => {
  const last = rows.value[rows.value.length - 1]
  return last ? last.reasons.filter((r) => r.count > 0).slice(0, 5) : []
})

const latestDate = computed(() => rows.value[rows.value.length - 1]?.tradeDate ?? '')

onMounted(async () => {
  try {
    rows.value = await getMissFunnel(props.days)
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="miss-history">
    <p v-if="error" class="nt-empty">漏斗历史暂不可用</p>
    <template v-else>
      <NtEChart v-if="option" :option="option" :height="height" />
      <p v-else class="nt-empty" :style="{ height }">{{ loading ? '读取中…' : '近几日没有候选漏斗数据' }}</p>
      <div v-if="latestReasons.length" class="reasons">
        <span class="reasons-label">{{ latestDate }} 未入选原因：</span>
        <span v-for="r in latestReasons" :key="r.reason" class="reason-tag">
          {{ r.reason }} ×{{ r.count }}
        </span>
      </div>
    </template>
  </div>
</template>

<style scoped>
.nt-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0;
  color: var(--nt-text-3);
  font-size: 12px;
}

.reasons {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  margin-top: 8px;
  font-size: 12px;
}

.reasons-label {
  color: var(--nt-text-3);
}

.reason-tag {
  color: var(--nt-text-2);
  border: 1px solid var(--nt-border-1);
  border-radius: 3px;
  padding: 0 6px;
  background: var(--nt-bg-1);
}
</style>
