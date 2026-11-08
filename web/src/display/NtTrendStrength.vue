<script setup lang="ts">
/**
 * 趋势强度（TSP 六格强弱指标的口径内替代）：MA20 上占比大数 + 20 日占比 sparkline + 站上/跌破只数。
 * 数据纪律：ready=false（快照整源未取到）显 missing，禁把取数失败说成「任务未产出」；
 * ready=true 但 found=false / latest=null 才是真空态（广度快照未产出）→ NtEmpty 保卡位。
 * ratio 是份额口径走 ratioPct。
 */
import { computed } from 'vue'
import type { BreadthSummary } from '@/api/market'
import NtEmpty from '@/ui/NtEmpty.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import NtSparkline from '@/viz/NtSparkline.vue'
import { intText, ratioPct } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    breadth: BreadthSummary | null
    /** false = 广度快照未取到（missing 态，与「任务未产出」的真空态区分）。 */
    ready?: boolean
    loading?: boolean
  }>(),
  { ready: true, loading: false },
)

const usable = computed(() => !!props.breadth?.found && !!props.breadth.latest)

/** sparkline 时序：days 按交易日升序；当日 live 截面若不在 days 内则补尾（占比口径 ratio 原值）。 */
const sparkValues = computed<(number | null)[]>(() => {
  if (!props.breadth) return []
  const days = [...props.breadth.days].sort((a, b) => a.tradeDate.localeCompare(b.tradeDate))
  const values = days.map((d) => d.ratio)
  const latest = props.breadth.latest
  if (latest && !days.some((d) => d.tradeDate === latest.tradeDate)) values.push(latest.ratio)
  return values
})
</script>

<template>
  <NtSkeleton v-if="loading && !ready" :rows="3" />
  <NtEmpty
    v-else-if="!ready"
    state="missing"
    title="广度快照未返回"
    hint="行情源恢复后自动刷新"
  />
  <NtEmpty
    v-else-if="!usable"
    title="还没有广度数据"
    hint="盘后广度快照任务产出后展示"
  />
  <div v-else class="trend">
    <div class="trend__main">
      <p class="trend__k">MA{{ breadth!.latest!.window }} 上占比</p>
      <p class="trend__v">{{ ratioPct(breadth!.latest!.ratio, 1) }}</p>
      <p class="trend__s">
        <span class="up">站上 <b>{{ intText(breadth!.latest!.above) }}</b></span>
        <span class="down">跌破 <b>{{ intText(breadth!.latest!.below) }}</b></span>
        <span v-if="breadth!.latest!.live" class="trend__live">实时</span>
      </p>
    </div>
    <NtSparkline :values="sparkValues" :width="120" :height="36" class="trend__spark" />
  </div>
</template>

<style scoped>
.trend {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.trend__k {
  margin: 0;
  font-size: 11px;
  color: var(--nt-text-3);
}

.trend__v {
  margin: 4px 0 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-num-lg);
  font-weight: 700;
  color: var(--nt-text-1);
}

.trend__s {
  display: flex;
  gap: 12px;
  margin: 3px 0 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.trend__s b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.trend__s .up b {
  color: var(--nt-up);
}

.trend__s .down b {
  color: var(--nt-down);
}

.trend__live {
  color: var(--nt-text-3);
}

.trend__spark {
  flex: none;
}
</style>
