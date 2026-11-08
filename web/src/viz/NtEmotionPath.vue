<script setup lang="ts">
/**
 * 盘中情绪路径（NT 独有口径，TSP 情绪雷达的替代形态）：涨停/跌停/炸板三线时序 + 竞价截面点。
 * 数据纪律：points 空 = 非交易时段或快照任务未产出 → NtEmpty 显式空态保卡位，不画空图装没事；
 * partial=true（当日截面补集中）图照画、附一行注记。
 */
import { computed } from 'vue'
import type { EmotionPathPoint } from '@/api/market'
import NtEmpty from '@/ui/NtEmpty.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import NtEChart from '@/viz/NtEChart.vue'
import { useChartPalette } from '@/theme/tokens'
import { buildEmotionPathOption } from '@/viz/emotion-path-option'

const props = withDefaults(
  defineProps<{
    points: EmotionPathPoint[]
    /** 当日截面未落全（EmotionPath.partial 透传）。 */
    partial?: boolean
    height?: string
    /** false = 情绪路径未取到：显 missing（与「非交易时段无点位」的真空态区分，禁把失败说成非交易时段）。 */
    ready?: boolean
    loading?: boolean
  }>(),
  { partial: false, height: '220px', ready: true, loading: false },
)

const palette = useChartPalette()
const option = computed(() => (props.points.length ? buildEmotionPathOption(props.points, palette.value) : null))
</script>

<template>
  <NtSkeleton v-if="loading && !ready" :rows="3" />
  <NtEmpty
    v-else-if="!ready"
    state="missing"
    title="情绪路径未返回"
    hint="行情源恢复后自动刷新"
  />
  <NtEmpty
    v-else-if="!points.length"
    title="非交易时段暂无情绪路径"
    hint="交易时段每 5 分钟落一个情绪截面，首个截面产出后在这里展开"
  />
  <template v-else>
    <NtEChart :option="option" :height="height" />
    <p v-if="partial" class="emo-path__note">当日截面补集中，曲线随快照产出逐步变长</p>
  </template>
</template>

<style scoped>
.emo-path__note {
  margin: 4px 0 0;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}
</style>
