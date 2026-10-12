<script setup lang="ts">
/**
 * 模型统计面板（M05）：轻量只读视图，直接吃 useBrainStore。
 * 供右栏常驻观察模型健康（调用/失败/延迟/熔断）。
 */
import { computed } from 'vue'
import { useBrainStore } from '@/stores/brain'
import NtStatCard from '@/display/NtStatCard.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { intText, ratioPct } from '@/utils/format'

const brain = useBrainStore()

interface Row {
  tier: string
  model: string
  calls: number | null
  failures: number | null
  p50Ms: number | null
  p95Ms: number | null
  kvCacheUsage: number | null
  state: string
  [key: string]: unknown
}

const columns: NtTableColumn<Row>[] = [
  { key: 'tier', title: '层级' },
  { key: 'model', title: '模型', mono: true },
  { key: 'calls', title: '调用', align: 'right' },
  { key: 'failures', title: '失败', align: 'right' },
  { key: 'p50Ms', title: 'P50(ms)', align: 'right' },
  { key: 'p95Ms', title: 'P95(ms)', align: 'right' },
  { key: 'kvCacheUsage', title: 'KV缓存', align: 'right' },
  { key: 'state', title: '状态' },
]

const rows = computed<Row[]>(() =>
  brain.tiers.map((t) => ({
    tier: t.tier || '—',
    model: t.model || '—',
    calls: t.calls,
    failures: t.failures,
    p50Ms: t.p50Ms,
    p95Ms: t.p95Ms,
    kvCacheUsage: t.kvCacheUsage,
    state: t.circuitOpen ? '熔断' : t.configured ? '正常' : '未配置',
  })),
)

const totals = computed(() => {
  const calls = rows.value.reduce((acc, r) => acc + (r.calls ?? 0), 0)
  const failures = rows.value.reduce((acc, r) => acc + (r.failures ?? 0), 0)
  const breaker = rows.value.filter((r) => r.state === '熔断').length
  return { calls, failures, breaker, failRate: calls > 0 ? failures / calls : null }
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">模型统计</h1>
        <p class="nt-note">随连接看门狗周期刷新（10s）。</p>
      </div>
    </header>

    <div class="nt-cards">
      <NtStatCard label="总调用" :value="intText(totals.calls)" />
      <NtStatCard label="总失败" :value="intText(totals.failures)" :tone="totals.failures > 0 ? 'warn' : 'neutral'" />
      <NtStatCard
        label="失败率"
        :value="ratioPct(totals.failRate)"
        :tone="(totals.failRate ?? 0) > 0.05 ? 'danger' : 'neutral'"
      />
      <NtStatCard label="熔断层级" :value="intText(totals.breaker)" :tone="totals.breaker > 0 ? 'danger' : 'neutral'" />
    </div>

    <section class="nt-panel">
      <h2 class="nt-panel__title">分层明细</h2>
      <NtTable :columns="columns" :rows="rows" row-key="tier" dense empty="还没有模型统计">
        <template #cell-kvCacheUsage="{ row }">
          <span :class="{ 'kv-hot': (row.kvCacheUsage ?? 0) > 0.85 }">{{ ratioPct(row.kvCacheUsage, 0) }}</span>
        </template>
      </NtTable>
    </section>
  </div>
</template>

<style scoped>
.kv-hot {
  color: var(--nt-warn);
  font-weight: 600;
}
</style>
