<script setup lang="ts">
/**
 * 涨跌广度三段条（TSP 涨跌分布直方图的口径内替代）：涨/平/跌家数按占比分段 + 三格计数。
 * 数据纪律：任一计数为 null = 快照未取到 → missing 态（不拿缺段冒充 0 家）。
 */
import { computed } from 'vue'
import NtEmpty from '@/ui/NtEmpty.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import { intText } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    advance: number | null
    flat: number | null
    decline: number | null
    loading?: boolean
  }>(),
  { loading: false },
)

const missing = computed(() => props.advance == null || props.flat == null || props.decline == null)

const total = computed(() => (missing.value ? 0 : props.advance! + props.flat! + props.decline!))

function segWidth(part: number): string {
  if (!total.value) return '0%'
  return `${((part / total.value) * 100).toFixed(1)}%`
}
</script>

<template>
  <NtSkeleton v-if="loading && missing" :rows="2" />
  <NtEmpty
    v-else-if="missing"
    state="missing"
    title="涨跌家数未返回"
    hint="情绪快照恢复后自动刷新"
  />
  <div v-else class="breadth">
    <div class="breadth__bar" role="img" :aria-label="`涨 ${advance} 家、平 ${flat} 家、跌 ${decline} 家`">
      <i class="breadth__seg breadth__seg--up" :style="{ width: segWidth(advance!) }" />
      <i class="breadth__seg breadth__seg--flat" :style="{ width: segWidth(flat!) }" />
      <i class="breadth__seg breadth__seg--down" :style="{ width: segWidth(decline!) }" />
    </div>
    <div class="breadth__counts">
      <span class="up">涨 <b>{{ intText(advance) }}</b></span>
      <span class="flat">平 <b>{{ intText(flat) }}</b></span>
      <span class="down">跌 <b>{{ intText(decline) }}</b></span>
    </div>
  </div>
</template>

<style scoped>
.breadth__bar {
  display: flex;
  height: 10px;
  border-radius: var(--nt-radius-s);
  overflow: hidden;
  background: var(--nt-bg-2);
}

.breadth__seg {
  display: block;
  height: 100%;
  transition: width var(--nt-transition);
}

.breadth__seg--up {
  background: var(--nt-up);
}

.breadth__seg--flat {
  background: var(--nt-text-3);
}

.breadth__seg--down {
  background: var(--nt-down);
}

.breadth__counts {
  display: flex;
  gap: 14px;
  margin-top: 8px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.breadth__counts b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.breadth__counts .up b {
  color: var(--nt-up);
}

.breadth__counts .flat b {
  color: var(--nt-text-3);
}

.breadth__counts .down b {
  color: var(--nt-down);
}
</style>
