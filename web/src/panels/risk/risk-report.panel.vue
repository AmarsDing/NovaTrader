<script setup lang="ts">
/**
 * 风控日报面板（M08）：当日风控指标汇总（买入次数、拦截次数、Kill Switch 状态等）。
 */
import { computed, onMounted, ref } from 'vue'
import { getRiskReport, type RiskReport } from '@/api/risk'
import NtButton from '@/ui/NtButton.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, messageOf } from '@/utils/format'

const report = ref<RiskReport | null>(null)
const loading = ref(false)
const error = ref('')

type Row = { metric: string; value: string; note: string } & Record<string, unknown>
const columns: NtTableColumn<Row>[] = [
  { key: 'metric', title: '指标' },
  { key: 'value', title: '值', mono: true },
  { key: 'note', title: '备注' },
]
const rows = computed(() => (report.value?.rows ?? []) as unknown as Row[])

async function load() {
  loading.value = true
  error.value = ''
  try {
    report.value = await getRiskReport()
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">风控日报</h1>
        <p class="nt-note">
          {{ report?.date || '—' }}
          <span v-if="report?.generatedAt"> · 生成于 {{ clock(report.generatedAt) }}</span>
        </p>
      </div>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </header>

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section v-if="report?.summary" class="nt-panel">
      <h2 class="nt-panel__title">摘要</h2>
      <p class="summary">{{ report.summary }}</p>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">指标明细</h2>
      <NtTable :columns="columns" :rows="rows" row-key="metric" dense empty="今天还没有风控日报" />
    </section>
  </div>
</template>

<style scoped>
.summary {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--nt-text-1);
  white-space: pre-wrap;
}
</style>
