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
import NtSourceTable from '@/display/NtSourceTable.vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { linkStatus, severityEntry } from '@/ui/status-map'
import { clock, messageOf, ratioPct } from '@/utils/format'
import { SOURCE_STALE_MS, sourceStatus, toHealthRow, type SourceHealthRow } from '@/display/source-rows'

const auth = useAuthStore()

const sources = ref<DataSource[]>([])
const quality = ref<QualityIssue[]>([])
const loading = ref(false)
const error = ref('')
const hint = ref('')
const toggling = ref('')
const collecting = ref('')

/** 状态灯口径（W0-3b/FE-D1）：唯一出处在 display/source-rows.ts，本面板与中心面板共用一份。 */
const STALE_MS = SOURCE_STALE_MS

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

/** W0-3b/FE-D1：源表归一行（状态灯/点带字/缺位口径在 NtSourceTable 收口），本面板只喂 listSources 数据。 */
const sourceRows = computed(() => sources.value.map(toHealthRow))

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
/** 行点击开抽屉（W0-3b：归一行以 key 为身份——row-click 的 row 形参取 NtSourceTable 的归一行）。 */
function openSource(row: Record<string, unknown>) {
  selectedSourceId.value = String(row.key ?? '')
  selectedQualityId.value = ''
  drawerOpen.value = true
}

/**
 * 槽内归一行 → 原始 DataSource 再写操作（启停/采集需要行体里的 id/kind；
 * 归一行只带读面字段）。行必出自 sources（归一即由它而来），查找落空静默不写——绝不拿假行打 API。
 */
function toggleRow(row: SourceHealthRow, enabled: boolean) {
  const src = sources.value.find((s) => s.id === row.key)
  if (!src) return
  void toggle(src, enabled)
}
function collectRow(row: SourceHealthRow) {
  const src = sources.value.find((s) => s.id === row.key)
  if (!src) return
  void collect(src)
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
        <!-- W0-3b/FE-D1：源表=NtSourceTable 单一事实组件（列组/状态灯/缺位口径唯一出处）；
             启停/采集写操作经槽注入且 @click.stop，不触发行点击开抽屉。 -->
        <NtSourceTable
          variant="health"
          :health="sourceRows"
          clickable
          empty="还没有数据源"
          @row-click="openSource"
        >
          <template #enabled="{ row }">
            <span @click.stop>
              <NtSwitch v-if="!auth.isViewer()" :model-value="row.enabled" @update:model-value="toggleRow(row, $event)" />
              <span v-else>{{ row.enabled ? '开' : '关' }}</span>
            </span>
          </template>
          <template #ops="{ row }">
            <NtButton
              v-if="!auth.isViewer() && row.enabled"
              size="sm"
              :loading="collecting === row.key"
              @click.stop="collectRow(row)"
            >
              采集
            </NtButton>
          </template>
        </NtSourceTable>
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
          <div><dt>状态</dt><dd>{{ linkStatus(sourceStatus(selectedSource)).text }}</dd></div>
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
