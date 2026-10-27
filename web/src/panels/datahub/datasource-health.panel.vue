<script setup lang="ts">
/**
 * 数据源治理面板（M01）：数据源启停（owner）+ 手动触发采集（owner）+ 质量报告。
 * 状态灯：enabled + lastAt 新鲜度 → live/ok/stale/down。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  collectDomain,
  getQuality,
  listSources,
  setSourceEnabled,
  type DataSource,
  type QualityIssue,
} from '@/api/datahub'
import { useAuthStore } from '@/stores/auth'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { linkStatus, severityEntry } from '@/ui/status-map'
import { clock, messageOf, ratioPct } from '@/utils/format'
import { serverNow } from '@/utils/server-clock'

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
  /* P2-10：lastAt 是服务端戳——新鲜度与后端时源对齐（未校准回退本地钟）。 */
  return serverNow() - at < STALE_MS ? 'live' : 'stale'
}

/** 状态点双编码（§7.2-1）：dot 带字，状态词取自链路五态册（§7.1，一字锁定）。 */
function dotLabel(s: DataSource): string {
  return `${s.name || s.id} · ${linkStatus(statusOf(s)).text}`
}

/** 面板头徽注（§7.2-2/§7.3）：时间证据取各源最近一次成功采集的最大值；全缺传 null 显「时点未知」。 */
const latestFreshAt = computed<number | null>(() => {
  let best = 0
  for (const s of sources.value) {
    if (!s.lastAt) continue
    const t = new Date(s.lastAt).getTime()
    if (Number.isFinite(t) && t > best) best = t
  }
  return best > 0 ? best : null
})

type SourceRow = DataSource & Record<string, unknown>
// W4b：说明长文出列进抽屉——主表 8 列；行点击开 320 抽屉放 lastError 全文+采集操作同闸副本。
const sourceColumns: NtTableColumn<SourceRow>[] = [
  { key: 'name', title: '数据源' },
  { key: 'kind', title: '域', mono: true },
  { key: 'successRate', title: '成功率', align: 'right' },
  { key: 'latencyMs', title: '最近延迟(ms)', align: 'right' },
  { key: 'fails', title: '成功/失败', align: 'right' },
  { key: 'lastAt', title: '最近成功', mono: true },
  { key: 'enabled', title: '启停' },
  { key: 'collect', title: '' },
]
const sourceRows = computed(() => sources.value as unknown as SourceRow[])

type QualityRow = QualityIssue & Record<string, unknown>
// W4b：说明长文出列进抽屉——主表 8 列；行点击开 320 抽屉放 message 全文+otherSource 对照。
const qualityColumns: NtTableColumn<QualityRow>[] = [
  { key: 'domain', title: '域', mono: true },
  { key: 'checkName', title: '检查项' },
  { key: 'symbol', title: '证券', mono: true },
  { key: 'tradeDate', title: '交易日', mono: true },
  { key: 'severity', title: '严重度' },
  { key: 'source', title: '来源', mono: true },
  { key: 'hits', title: '条数', align: 'right' },
  { key: 'lastSeen', title: '最近出现', mono: true },
]
const qualityRows = computed(() => quality.value as unknown as QualityRow[])

/* 严重度章走 status-map severityEntry 共享出口（W4b review F4：双面板同一份查册逻辑，禁复制漂移）。 */

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

/**
 * 静默追帧（W3-1 值守面）：30s 重拉，不翻 loading 章、失败保留旧数据。
 * 该面板状态灯由「当前时刻 − lastAt」判定（STALE_MS=30min），即使数据无变化也需周期重估，
 * 否则新鲜度灯停在挂载时刻、永不转陈旧。
 */
async function refreshQuiet() {
  const [s, q] = await Promise.allSettled([listSources(), getQuality({ limit: 50 })])
  if (s.status === 'fulfilled') sources.value = s.value
  if (q.status === 'fulfilled') quality.value = q.value
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

const QUIET_POLL_MS = 30_000
let pollTimer: ReturnType<typeof setInterval> | undefined

/** W4b：双表行点击开 320 抽屉（detail/message 长文收抽屉，主表瘦身）。选中源/选中问题互斥，抽屉按 selectedSource 优先切换。 */
const drawerOpen = ref(false)
const selectedSourceId = ref('')
const selectedQualityId = ref('')
/* W4b review F10：选中存 id 不存快照——refreshQuiet 30s 换源后抽屉跟随最新行（事件时抽屉不读旧信）。
 * 行掉出列表（源注销/问题消除）→ 抽屉显「已不在当前列表」诚实缺位，不冒充仍在。 */
const selectedSource = computed(() => sources.value.find((s) => s.id === selectedSourceId.value) ?? null)
const selectedQuality = computed(() => quality.value.find((q) => q.id === selectedQualityId.value) ?? null)
function openSource(row: SourceRow) {
  selectedSourceId.value = String(row.id)
  selectedQualityId.value = ''
  drawerOpen.value = true
}
function openQuality(row: QualityRow) {
  selectedQualityId.value = String(row.id)
  selectedSourceId.value = ''
  drawerOpen.value = true
}

onMounted(() => {
  void load()
  pollTimer = setInterval(() => {
    if (document.hidden) return
    void refreshQuiet()
  }, QUIET_POLL_MS)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <!-- W4b 三段式：筛选条空（本面板无筛选控件，留位供后续）→ 主体（双表，行点击开抽屉）→ 320 抽屉（行画像+长文全文+操作同闸副本）。 -->
  <NtAdminScaffold v-model:drawer-open="drawerOpen" title="数据源治理" :drawer-width="320">
    <template #actions>
      <NtFreshness label="数据源" :at="latestFreshAt" :stale-after-ms="STALE_MS" />
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </template>
    <template #filter>
      <p class="nt-note">状态灯按 enabled + 最近成功时间判定；启停与手动采集仅 owner。点行看详情。</p>
    </template>
    <div class="dh-body">
      <NtErrorGuide :error="error" />
      <p v-if="hint" class="nt-note">{{ hint }}</p>

      <section class="nt-panel">
        <h2 class="nt-panel__title">数据源</h2>
        <NtTable
          :columns="sourceColumns"
          :rows="sourceRows"
          row-key="id"
          dense
          clickable
          empty="还没有数据源"
          @row-click="openSource"
        >
          <template #cell-name="{ row }">
            <NtStatusDot :status="statusOf(row)" :label="dotLabel(row)" />
          </template>
          <template #cell-successRate="{ row }">
            <!-- 「没取到≠0」（P3-10c 复核）：原 (row.successRate ?? 1) < 0.9 把缺失冒充满分、告警静默消失；
                 null = 统计窗口无采样，显「—」不参与告警（不虚警），title 写明原因 -->
            <span v-if="row.successRate != null" :class="{ warn: row.successRate < 0.9 }">{{ ratioPct(row.successRate, 0) }}</span>
            <span v-else class="mid" title="统计窗口内无采样，成功率无法计算">—</span>
          </template>
          <template #cell-latencyMs="{ row }">{{ row.latencyMs != null ? Math.round(row.latencyMs) : '—' }}</template>
          <template #cell-fails="{ row }">
            <span :class="{ warn: (row.consecutiveFailures ?? 0) > 0 }">
              {{ row.successCount != null ? row.successCount : '—' }} / {{ row.failureCount != null ? row.failureCount : '—' }}
            </span>
          </template>
          <template #cell-lastAt="{ row }">{{ clock(row.lastAt) || '—' }}</template>
          <template #cell-enabled="{ row }">
            <!-- 启停开关写操作，勿触发行点击开抽屉 -->
            <span @click.stop>
              <NtSwitch v-if="!auth.isViewer()" :model-value="row.enabled" @update:model-value="toggle(row, $event)" />
              <span v-else>{{ row.enabled ? '开' : '关' }}</span>
            </span>
          </template>
          <template #cell-collect="{ row }">
            <NtButton
              v-if="!auth.isViewer() && row.enabled"
              size="sm"
              :loading="collecting === row.id"
              @click.stop="collect(row)"
            >
              采集
            </NtButton>
          </template>
        </NtTable>
      </section>

      <section class="nt-panel">
        <h2 class="nt-panel__title">质量问题（逐条）</h2>
        <NtTable
          :columns="qualityColumns"
          :rows="qualityRows"
          row-key="id"
          dense
          clickable
          empty="没有质量问题记录"
          @row-click="openQuality"
        >
          <template #cell-severity="{ row }">
            <NtBadge :tone="severityEntry(row.severity)?.tone ?? 'neutral'">
              {{ severityEntry(row.severity)?.text ?? (row.severity || '—') }}
            </NtBadge>
          </template>
          <template #cell-hits="{ row }">{{ row.hits != null ? row.hits : '—' }}</template>
          <template #cell-lastSeen="{ row }">{{ clock(row.lastSeen) || '—' }}</template>
        </NtTable>
      </section>
    </div>
    <template #drawer>
      <div v-if="selectedSource" class="dh-drawer">
        <h4>数据源详情</h4>
        <dl>
          <div><dt>数据源</dt><dd>{{ selectedSource.name || selectedSource.id }}</dd></div>
          <div><dt>域</dt><dd>{{ selectedSource.kind }}</dd></div>
          <div><dt>状态</dt><dd>{{ linkStatus(statusOf(selectedSource)).text }}</dd></div>
          <div><dt>成功率</dt><dd>{{ selectedSource.successRate != null ? ratioPct(selectedSource.successRate, 0) : '—' }}</dd></div>
          <div><dt>最近延迟</dt><dd>{{ selectedSource.latencyMs != null ? Math.round(selectedSource.latencyMs) + ' ms' : '—' }}</dd></div>
          <div><dt>平均延迟</dt><dd>{{ selectedSource.avgLatencyMs != null ? Math.round(selectedSource.avgLatencyMs) + ' ms' : '—' }}</dd></div>
          <div><dt>最近成功</dt><dd>{{ clock(selectedSource.lastAt) || '—' }}</dd></div>
          <div><dt>最近失败</dt><dd>{{ clock(selectedSource.lastFailureAt) || '—' }}</dd></div>
          <div><dt>配额</dt><dd>{{ selectedSource.quotaUsed != null ? selectedSource.quotaUsed : '—' }} / {{ selectedSource.quotaLimit != null ? selectedSource.quotaLimit : '—' }}</dd></div>
          <div><dt>优先级</dt><dd>{{ selectedSource.priority != null ? selectedSource.priority : '—' }}</dd></div>
          <div><dt>官方源</dt><dd>{{ selectedSource.official == null ? '—' : selectedSource.official ? '是' : '否' }}</dd></div>
          <div><dt>在册</dt><dd>{{ selectedSource.active == null ? '—' : selectedSource.active ? '在册' : '未在册' }}</dd></div>
        </dl>
        <h4>说明 / 最近错误</h4>
        <p class="detail-full">{{ selectedSource.lastError || selectedSource.detail || '—' }}</p>
        <div v-if="!auth.isViewer() && selectedSource.enabled" class="drawer-ops">
          <NtButton size="sm" :loading="collecting === selectedSource.id" @click="collect(selectedSource)">采集</NtButton>
        </div>
      </div>
      <div v-else-if="selectedQuality" class="dh-drawer">
        <h4>质量问题详情</h4>
        <dl>
          <div><dt>域</dt><dd>{{ selectedQuality.domain }}</dd></div>
          <div><dt>检查项</dt><dd>{{ selectedQuality.checkName }}</dd></div>
          <div><dt>证券</dt><dd>{{ selectedQuality.symbol || '—' }}</dd></div>
          <div><dt>交易日</dt><dd>{{ selectedQuality.tradeDate || '—' }}</dd></div>
          <div><dt>严重度</dt><dd>{{ severityEntry(selectedQuality.severity)?.text ?? (selectedQuality.severity || '—') }}</dd></div>
          <div><dt>来源</dt><dd>{{ selectedQuality.source }}</dd></div>
          <div v-if="selectedQuality.otherSource"><dt>对照源</dt><dd>{{ selectedQuality.otherSource }}</dd></div>
          <div><dt>条数</dt><dd>{{ selectedQuality.hits != null ? selectedQuality.hits : '—' }}</dd></div>
          <div><dt>最近出现</dt><dd>{{ clock(selectedQuality.lastSeen) || '—' }}</dd></div>
        </dl>
        <h4>说明</h4>
        <p class="detail-full">{{ selectedQuality.message || '—' }}</p>
      </div>
      <p v-else class="dh-drawer-empty">
        {{ (selectedSourceId || selectedQualityId) ? '该行已不在当前列表（源注销或问题消除）。' : '在左侧点一行看详情。' }}
      </p>
    </template>
  </NtAdminScaffold>
</template>

<style scoped>
.dh-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 12px;
}

.dh-drawer {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
}

.dh-drawer h4 {
  margin: 4px 0 2px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.dh-drawer dl {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: var(--nt-fs-caption);
}

.dh-drawer dl > div {
  display: flex;
  gap: 8px;
}

.dh-drawer dt {
  flex: none;
  width: 52px;
  color: var(--nt-text-3);
}

.dh-drawer dd {
  margin: 0;
  color: var(--nt-text-1);
  word-break: break-all;
}

.detail-full {
  margin: 0;
  font-size: var(--nt-fs-caption);
  line-height: 1.6;
  color: var(--nt-text-1);
  white-space: pre-wrap;
  word-break: break-all;
}

.drawer-ops {
  display: flex;
  gap: 8px;
}

.dh-drawer-empty {
  margin: 0;
  padding: 12px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

/* G4 棘轮候选（§7.1 lint 候选方向）：scoped 散写状态色 class 待 G4 收编为册导出；
   severity 章已率先改走册（NtBadge + quality.*），successRate/fails 告警着色仍用 .warn（P3-10c 守卫区不动）。 */
.warn {
  color: var(--nt-warn);
  font-weight: 600;
}
.mid {
  color: var(--nt-text-2);
}
</style>
