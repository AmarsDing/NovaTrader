<script setup lang="ts">
/**
 * 数据源治理面板（M01）：数据源启停（owner）+ 质量报告。
 * 状态灯：enabled + lastAt 新鲜度 → live/ok/stale/down。
 */
import { computed, onMounted, ref } from 'vue'
import { getQuality, listSources, setSourceEnabled, type DataSource, type QualityReport } from '@/api/datahub'
import { useAuthStore } from '@/stores/auth'
import NtButton from '@/ui/NtButton.vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, messageOf } from '@/utils/format'

const auth = useAuthStore()

const sources = ref<DataSource[]>([])
const quality = ref<QualityReport[]>([])
const loading = ref(false)
const error = ref('')
const toggling = ref('')

const STALE_MS = 30 * 60 * 1000

function statusOf(s: DataSource): 'live' | 'ok' | 'stale' | 'down' | 'off' {
  if (!s.enabled) return 'off'
  const state = s.status.toLowerCase()
  if (state === 'open' || state === 'unavailable' || state.includes('down') || state.includes('error')) return 'down'
  if (state === 'half_open') return 'stale'
  if (!s.lastAt) return 'stale'
  const at = new Date(s.lastAt).getTime()
  if (!Number.isFinite(at)) return 'stale'
  return Date.now() - at < STALE_MS ? 'live' : 'stale'
}

type SourceRow = DataSource & Record<string, unknown>
const sourceColumns: NtTableColumn<SourceRow>[] = [
  { key: 'name', title: '数据源' },
  { key: 'kind', title: '类型', mono: true },
  { key: 'lastAt', title: '最近采集', mono: true },
  { key: 'detail', title: '说明' },
  { key: 'enabled', title: '启停' },
]
const sourceRows = computed(() => sources.value as unknown as SourceRow[])

type QualityRow = QualityReport & Record<string, unknown>
const qualityColumns: NtTableColumn<QualityRow>[] = [
  { key: 'domain', title: '域', mono: true },
  { key: 'tradeDate', title: '交易日', mono: true },
  { key: 'total', title: '总数', align: 'right' },
  { key: 'ok', title: '正常', align: 'right' },
  { key: 'missing', title: '缺失', align: 'right' },
  { key: 'stale', title: '过期', align: 'right' },
]
const qualityRows = computed(() => quality.value as unknown as QualityRow[])

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [s, q] = await Promise.allSettled([listSources(), getQuality({ limit: 20 })])
    if (s.status === 'fulfilled') sources.value = s.value
    if (q.status === 'fulfilled') quality.value = q.value
    if (s.status === 'rejected') error.value = messageOf(s.reason)
  } finally {
    loading.value = false
  }
}

async function toggle(source: DataSource, enabled: boolean) {
  if (auth.isViewer()) return
  toggling.value = source.id
  error.value = ''
  try {
    await setSourceEnabled(source.id, enabled)
    sources.value = await listSources()
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    toggling.value = ''
  }
}

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">数据源治理</h1>
        <p class="nt-note">状态灯按 enabled + 最近采集时间判定；启停仅 owner。</p>
      </div>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </header>

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <h2 class="nt-panel__title">数据源</h2>
      <NtTable :columns="sourceColumns" :rows="sourceRows" row-key="id" dense empty="还没有数据源">
        <template #cell-name="{ row }">
          <NtStatusDot :status="statusOf(row)" :label="row.name || row.id" />
        </template>
        <template #cell-lastAt="{ row }">{{ clock(row.lastAt) }}</template>
        <template #cell-enabled="{ row }">
          <NtSwitch v-if="!auth.isViewer()" :model-value="row.enabled" @update:model-value="toggle(row, $event)" />
          <span v-else>{{ row.enabled ? '开' : '关' }}</span>
        </template>
      </NtTable>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">质量报告</h2>
      <NtTable :columns="qualityColumns" :rows="qualityRows" row-key="domain" dense empty="还没有质量报告" />
    </section>
  </div>
</template>
