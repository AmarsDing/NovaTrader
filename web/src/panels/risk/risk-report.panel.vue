<script setup lang="ts">
/**
 * 风控日报面板（M08）：当日风控指标汇总（校验/放行/拒绝、规则命中、风控事件）。
 * 数据来自 GET /api/risk/report（DailyReportReply）：后端只给计数与事件，
 * 指标行由 api/risk.ts 按计数拼出，缺的计数写「未返回」。
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

const KIND_LABEL: Record<string, string> = {
  killswitch: '急停',
  mode: '模式变更',
  breaker: '熔断',
  reject_rate: '拒绝率告警',
  param: '参数变更',
}

type EventRow = { at: string; kind: string; state: string; detail: string } & Record<string, unknown>
const eventColumns: NtTableColumn<EventRow>[] = [
  { key: 'at', title: '时间', mono: true },
  { key: 'kind', title: '类别' },
  { key: 'state', title: '状态' },
  { key: 'detail', title: '详情' },
]
const eventRows = computed<EventRow[]>(() =>
  (report.value?.events ?? []).map((item) => ({
    at: item.at ? clock(item.at) : '时刻未返回',
    kind: KIND_LABEL[item.kind] ?? (item.kind || '未返回'),
    state: item.active ? '生效中' : '已解除',
    detail: [item.mode, item.source, item.operator, item.reason].filter(Boolean).join(' · ') || '没有更多字段',
  })),
)

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
      <NtTable :columns="columns" :rows="rows" row-key="metric" dense empty="今天还没有风控校验记录" />
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">风控事件（急停 / 模式 / 熔断）</h2>
      <NtTable :columns="eventColumns" :rows="eventRows" row-key="at" dense empty="今天没有风控事件" />
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
