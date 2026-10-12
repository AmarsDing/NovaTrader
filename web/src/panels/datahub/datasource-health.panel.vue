<script setup lang="ts">
/**
 * 数据源治理面板（M01）：数据源启停（owner）+ 手动触发采集（owner）+ 质量报告。
 * 状态灯：enabled + lastAt 新鲜度 → live/ok/stale/down。
 */
import { computed, onMounted, ref } from 'vue'
import {
  collectDomain,
  getQuality,
  listSources,
  setSourceEnabled,
  type DataSource,
  type QualityIssue,
} from '@/api/datahub'
import { useAuthStore } from '@/stores/auth'
import NtButton from '@/ui/NtButton.vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, messageOf, ratioPct } from '@/utils/format'

const auth = useAuthStore()

const sources = ref<DataSource[]>([])
const quality = ref<QualityIssue[]>([])
const loading = ref(false)
const error = ref('')
const hint = ref('')
const toggling = ref('')
const collecting = ref('')

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
  { key: 'kind', title: '域', mono: true },
  { key: 'successRate', title: '成功率', align: 'right' },
  { key: 'latencyMs', title: '最近延迟(ms)', align: 'right' },
  { key: 'fails', title: '成功/失败', align: 'right' },
  { key: 'lastAt', title: '最近成功', mono: true },
  { key: 'detail', title: '说明' },
  { key: 'enabled', title: '启停' },
  { key: 'collect', title: '' },
]
const sourceRows = computed(() => sources.value as unknown as SourceRow[])

type QualityRow = QualityIssue & Record<string, unknown>
const qualityColumns: NtTableColumn<QualityRow>[] = [
  { key: 'domain', title: '域', mono: true },
  { key: 'checkName', title: '检查项' },
  { key: 'symbol', title: '代码', mono: true },
  { key: 'tradeDate', title: '交易日', mono: true },
  { key: 'severity', title: '严重度' },
  { key: 'source', title: '来源', mono: true },
  { key: 'hits', title: '条数', align: 'right' },
  { key: 'lastSeen', title: '最近出现', mono: true },
  { key: 'message', title: '说明' },
]
const qualityRows = computed(() => quality.value as unknown as QualityRow[])

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [s, q] = await Promise.allSettled([listSources(), getQuality({ limit: 50 })])
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

/** 手动触发该源域的立即采集（owner），完成后刷新状态。 */
async function collect(source: DataSource) {
  if (auth.isViewer()) return
  const domain = String(source.kind || source.id)
  collecting.value = source.id
  error.value = ''
  hint.value = ''
  try {
    await collectDomain(domain)
    hint.value = `${source.name || source.id} 已触发采集，稍等片刻看最近成功时间`
    await load()
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    collecting.value = ''
  }
}

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">数据源治理</h1>
        <p class="nt-note">状态灯按 enabled + 最近成功时间判定；启停与手动采集仅 owner。</p>
      </div>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </header>

    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="hint" class="nt-note">{{ hint }}</p>

    <section class="nt-panel">
      <h2 class="nt-panel__title">数据源</h2>
      <NtTable :columns="sourceColumns" :rows="sourceRows" row-key="id" dense empty="还没有数据源">
        <template #cell-name="{ row }">
          <NtStatusDot :status="statusOf(row)" :label="row.name || row.id" />
        </template>
        <template #cell-successRate="{ row }">
          <span :class="{ warn: (row.successRate ?? 1) < 0.9 }">{{ row.successRate != null ? ratioPct(row.successRate, 0) : '—' }}</span>
        </template>
        <template #cell-latencyMs="{ row }">{{ row.latencyMs != null ? Math.round(row.latencyMs) : '—' }}</template>
        <template #cell-fails="{ row }">
          <span :class="{ warn: (row.consecutiveFailures ?? 0) > 0 }">
            {{ row.successCount != null ? row.successCount : '—' }} / {{ row.failureCount != null ? row.failureCount : '—' }}
          </span>
        </template>
        <template #cell-lastAt="{ row }">{{ clock(row.lastAt) }}</template>
        <template #cell-enabled="{ row }">
          <NtSwitch v-if="!auth.isViewer()" :model-value="row.enabled" @update:model-value="toggle(row, $event)" />
          <span v-else>{{ row.enabled ? '开' : '关' }}</span>
        </template>
        <template #cell-collect="{ row }">
          <NtButton
            v-if="!auth.isViewer() && row.enabled"
            size="sm"
            :loading="collecting === row.id"
            @click="collect(row)"
          >
            采集
          </NtButton>
        </template>
      </NtTable>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">质量问题（逐条）</h2>
      <NtTable :columns="qualityColumns" :rows="qualityRows" row-key="id" dense empty="没有质量问题记录">
        <template #cell-severity="{ row }">
          <span :class="{ warn: row.severity === 'high', mid: row.severity === 'medium' }">{{ row.severity || '—' }}</span>
        </template>
        <template #cell-hits="{ row }">{{ row.hits != null ? row.hits : '—' }}</template>
        <template #cell-lastSeen="{ row }">{{ clock(row.lastSeen) }}</template>
      </NtTable>
    </section>
  </div>
</template>

<style scoped>
.warn {
  color: var(--nt-warn);
  font-weight: 600;
}
.mid {
  color: var(--nt-text-2);
}
</style>
