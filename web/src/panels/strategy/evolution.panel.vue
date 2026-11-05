<script setup lang="ts">
/**
 * 影子·自省·进化·准入面板（M07）。
 * 四个 tab：影子统计、自省记录、进化提案（owner 审批）、模拟盘 20 交易日准入检查表。
 */
import { computed, onMounted, ref, watch } from 'vue'
import {
  approveProposal,
  getAdmission,
  getShadowStats,
  listIntrospections,
  listProposals,
  listStrategies,
  rejectProposal,
  runIntrospection,
  type Admission,
  type StrategyMeta,
  type Introspection,
  type Proposal,
  type ShadowStats,
} from '@/api/backtest'
import { useAuthStore } from '@/stores/auth'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtTabs from '@/ui/NtTabs.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import { statusOf } from '@/ui/status-map'
import { clock, intText, messageOf, pnlClass, ratioPct } from '@/utils/format'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'

const auth = useAuthStore()

// W3-2 面板偏好持久化：键版勿改，旧档自然失效。
const PREFS_KEY = 'nt.prefs.evolution.v1'
const PREFS_ALLOW = { tab: ['shadow', 'intro', 'proposal', 'admission'], strategy: '*' } as const
const prefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)

const tab = ref(prefs.tab ?? 'shadow')
const loading = ref(false)
const error = ref('')

const shadow = ref<ShadowStats | null>(null)
const introspections = ref<Introspection[]>([])
const proposals = ref<Proposal[]>([])
const admission = ref<Admission | null>(null)
const strategies = ref<StrategyMeta[]>([])
const strategy = ref(prefs.strategy ?? '')

const running = ref(false)
const actingId = ref('')

/* 影子统计：horizons（持有窗口）与 buckets（分数桶）两张表，都是比率口径。 */
interface HorizonRow extends Record<string, unknown> {
  horizon: number
  count: number | null
  avgReturn: number | null
  winRate: number | null
}
const horizonColumns: NtTableColumn<HorizonRow>[] = [
  { key: 'horizon', title: '持有窗口', align: 'right' },
  { key: 'count', title: '样本', align: 'right' },
  { key: 'avgReturn', title: '平均收益', align: 'right' },
  { key: 'winRate', title: '胜率', align: 'right' },
]
const horizonRows = computed<HorizonRow[]>(() =>
  (shadow.value?.horizons ?? []).map((h) => ({ ...h })).sort((a, b) => a.horizon - b.horizon),
)

interface BucketRow extends Record<string, unknown> {
  bucket: string
  count: number | null
  avgT1: number | null
  avgT3: number | null
  avgT5: number | null
}
const bucketColumns: NtTableColumn<BucketRow>[] = [
  { key: 'bucket', title: '分数桶' },
  { key: 'count', title: '样本', align: 'right' },
  { key: 'avgT1', title: 'T+1 均值', align: 'right' },
  { key: 'avgT3', title: 'T+3 均值', align: 'right' },
  { key: 'avgT5', title: 'T+5 均值', align: 'right' },
]
const bucketRows = computed<BucketRow[]>(() => (shadow.value?.buckets ?? []).map((b) => ({ ...b })))

const introEntries = computed<TimelineEntry[]>(() =>
  introspections.value.map((r) => ({
    id: r.id || r.tradeDate + r.book,
    time: r.tradeDate || clock(r.createdAt),
    title: `${r.book || '—'} · 窗口 ${r.windowDays ?? '—'} 日 · 样本 ${r.sampleCount ?? '—'} · 胜率 ${ratioPct(r.winRate)} · 漏选 ${r.missedCount ?? 0}`,
    body: r.note || r.triggerReason || '',
    /* 触发章词取自 alert 册（§7.1，一字锁定）；tone 走 NtTimeline 自枚举（含 up/down），与册 warn 同值。 */
    tag: r.triggered ? statusOf('alert.triggered').text : '',
    tone: r.triggered ? 'warn' : 'neutral',
  })),
)

type ProposalRow = Proposal & Record<string, unknown>
const proposalColumns: NtTableColumn<ProposalRow>[] = [
  { key: 'title', title: '提案' },
  { key: 'strategy', title: '策略', mono: true },
  { key: 'status', title: '状态' },
  { key: 'createdAt', title: '提出时间', mono: true },
  { key: 'decidedAt', title: '决定时间', mono: true },
  { key: 'reason', title: '审批理由' },
]
const proposalRows = computed(() => proposals.value as unknown as ProposalRow[])

/* ---------- 批次 A7：提案 diff 与审批理由（盲签→明签） ---------- */

/** 待审提案（只给 pending 出操作；rolled_back 等终态不再复活审批入口——旧实现把 rolled_back 也当可批，本批收正）。 */
const pendingProposals = computed(() => proposals.value.filter((p) => p.status === 'pending'))
/** 每条待审提案的审批理由（必填，写进审计）。 */
const decideReasons = ref<Record<string, string>>({})

interface ParamDiffRow {
  key: string
  base: number | null
  next: number | null
  changed: boolean
  [key: string]: unknown
}
const diffColumns: NtTableColumn<ParamDiffRow>[] = [
  { key: 'key', title: '参数', mono: true },
  { key: 'base', title: '当前值', align: 'right', mono: true },
  { key: 'next', title: '提案值', align: 'right', mono: true },
]
/** 参数 diff：baseParams/params 键并集；新增显「新增」、删除显「删除」，同值不高亮。 */
function diffRowsOf(p: Proposal): ParamDiffRow[] {
  const keys = [...new Set([...Object.keys(p.baseParams), ...Object.keys(p.params)])].sort()
  return keys.map((key) => {
    const base = Object.prototype.hasOwnProperty.call(p.baseParams, key) ? p.baseParams[key] : null
    const next = Object.prototype.hasOwnProperty.call(p.params, key) ? p.params[key] : null
    return { key, base, next, changed: base !== next }
  })
}

interface MetricDiffRow {
  key: string
  base: string
  next: string
  [key: string]: unknown
}
const metricColumns: NtTableColumn<MetricDiffRow>[] = [
  { key: 'key', title: '指标', mono: true },
  { key: 'base', title: '基线', align: 'right', mono: true },
  { key: 'next', title: '提案', align: 'right', mono: true },
]
/** 指标对比：Struct 透传只读——数值保留至多 4 位小数，非数值原样字符串；不猜比率/百分点口径。 */
function metricText(v: unknown): string {
  if (typeof v === 'number' && Number.isFinite(v)) return Math.abs(v) >= 100 ? v.toFixed(2) : String(Number(v.toFixed(4)))
  if (v == null) return '—'
  return String(v)
}
function metricRowsOf(p: Proposal): MetricDiffRow[] {
  const keys = [...new Set([...Object.keys(p.baseMetrics), ...Object.keys(p.newMetrics)])].sort()
  return keys
    .map((key) => ({ key, base: metricText(p.baseMetrics[key]), next: metricText(p.newMetrics[key]) }))
    .filter((row) => row.base !== '—' || row.next !== '—')
}

/** 审批章文案/tone 出自 evolution 册（§7.1，一字锁定）：已批准/已拒绝/待审；
 *  册外值原样回显、tone 取待审同系 warn（不自封文案）。 */
function proposalStatus(status: string | null | undefined) {
  if (status === 'approved') return statusOf('evolution.approved')
  if (status === 'rejected') return statusOf('evolution.rejected')
  if (status === 'pending' || !status) return statusOf('evolution.pending')
  return { text: status, tone: 'warn' as const }
}

async function loadAll() {
  loading.value = true
  error.value = ''
  try {
    if (!strategies.value.length) {
      try {
        strategies.value = await listStrategies()
        if (!strategy.value && strategies.value[0]) strategy.value = strategies.value[0].name
      } catch (err) {
        error.value = messageOf(err)
      }
    }
    const [s, i, p, a] = await Promise.allSettled([
      getShadowStats(),
      listIntrospections(),
      listProposals(),
      strategy.value ? getAdmission(strategy.value) : Promise.resolve(null),
    ])
    if (s.status === 'fulfilled') shadow.value = s.value
    if (i.status === 'fulfilled') introspections.value = i.value
    if (p.status === 'fulfilled') proposals.value = p.value
    if (a.status === 'fulfilled') admission.value = a.value
    const first = [s, i, p].find((r) => r.status === 'rejected')
    if (first && first.status === 'rejected') error.value = messageOf(first.reason)
    else if (a.status === 'rejected') error.value = messageOf(a.reason)
  } finally {
    loading.value = false
  }
}

async function runIntro() {
  if (auth.isViewer()) return
  running.value = true
  error.value = ''
  try {
    await runIntrospection()
    introspections.value = await listIntrospections()
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    running.value = false
  }
}

async function decide(id: string, action: 'approve' | 'reject') {
  if (auth.isViewer()) return
  /* 批次 A7：审批理由必填——盲签收正为明签，理由写进提案 reason 字段随审计留痕。 */
  const reason = (decideReasons.value[id] ?? '').trim()
  if (!reason) {
    error.value = '请先填写审批理由（必填，写入审计）'
    return
  }
  actingId.value = id
  error.value = ''
  try {
    if (action === 'approve') await approveProposal(id, reason)
    else await rejectProposal(id, reason)
    delete decideReasons.value[id]
    proposals.value = await listProposals()
    if (strategy.value) admission.value = await getAdmission(strategy.value)
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    actingId.value = ''
  }
}

watch(strategy, (value, previous) => {
  if (value && value !== previous) void loadAll()
})

// 每次写整档（含未变字段），单字段变化不丢兄弟字段。
watch([tab, strategy], () =>
  savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { tab: tab.value, strategy: strategy.value }),
)

// 动态字段兜底：存档策略不在当前名单即回首项（默认语义同 loadAll 初次回填），陈旧档不卡死 select。
// flush:'sync' 锁序：loadAll 内名单回填后同步纠偏，同轮 getAdmission 读到的就是纠偏值——
// 默认 'pre' 节奏下首轮 loadAll 会拿陈旧 strategy 先发 getAdmission（残留报错）并与纠偏触发的二次 loadAll 竞速。
watch(
  strategies,
  (list) => {
    if (list.length && strategy.value && !list.some((item) => item.name === strategy.value)) {
      strategy.value = list[0].name
    }
  },
  { flush: 'sync' },
)

onMounted(loadAll)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">影子 · 自省 · 进化 · 准入</h1>
        <p class="nt-note">
          模拟盘
          <b>{{ admission?.tradingDays ?? '—' }}</b>
          / {{ admission?.requiredDays ?? 20 }} 交易日
          <!-- 准入就绪章词/tone 取自 evolution.admitted 册（§7.1，一字锁定）：已达准入 success。 -->
          <NtBadge v-if="admission?.ready" :tone="statusOf('evolution.admitted').tone" dot>{{ statusOf('evolution.admitted').text }}</NtBadge>
        </p>
      </div>
      <NtButton size="sm" :loading="loading" @click="loadAll">刷新</NtButton>
    </header>

    <NtTabs
      v-model="tab"
      :items="[
        { value: 'shadow', label: '影子统计' },
        { value: 'intro', label: '自省' },
        { value: 'proposal', label: '提案' },
        { value: 'admission', label: '准入' },
      ]"
    />

    <NtErrorGuide :error="error" />

    <section v-show="tab === 'shadow'" class="nt-panel">
      <h2 class="nt-panel__title">影子统计</h2>
      <div class="nt-cards">
        <NtStatCard label="信号总数" :value="intText(shadow?.total ?? null)" />
        <NtStatCard label="待后验" :value="intText(shadow?.pending ?? null)" />
        <NtStatCard
          label="窗口单调性"
          :value="shadow ? (shadow.monotonic ? '成立' : '不成立') : '—'"
          :tone="shadow ? (shadow.monotonic ? 'accent' : 'warn') : 'neutral'"
          sub="T+1/3/5 均值随窗口变长应当更好"
        />
      </div>
      <h3 class="sub">按持有窗口</h3>
      <NtTable :columns="horizonColumns" :rows="horizonRows" row-key="horizon" dense empty="还没有影子统计">
        <template #cell-horizon="{ row }">T+{{ row.horizon }}</template>
        <template #cell-count="{ row }">{{ row.count ?? '—' }}</template>
        <template #cell-avgReturn="{ row }">
          <span :class="pnlClass(row.avgReturn)">{{ ratioPct(row.avgReturn) }}</span>
        </template>
        <template #cell-winRate="{ row }">{{ ratioPct(row.winRate) }}</template>
      </NtTable>
      <h3 class="sub">按信号分数桶</h3>
      <NtTable :columns="bucketColumns" :rows="bucketRows" row-key="bucket" dense empty="还没有分数桶统计">
        <template #cell-count="{ row }">{{ row.count ?? '—' }}</template>
        <template #cell-avgT1="{ row }">
          <span :class="pnlClass(row.avgT1)">{{ ratioPct(row.avgT1) }}</span>
        </template>
        <template #cell-avgT3="{ row }">
          <span :class="pnlClass(row.avgT3)">{{ ratioPct(row.avgT3) }}</span>
        </template>
        <template #cell-avgT5="{ row }">
          <span :class="pnlClass(row.avgT5)">{{ ratioPct(row.avgT5) }}</span>
        </template>
      </NtTable>
    </section>

    <section v-show="tab === 'intro'" class="nt-panel">
      <h2 class="nt-panel__title">
        自省记录
        <NtButton v-if="!auth.isViewer()" size="sm" :loading="running" @click="runIntro">立即执行</NtButton>
      </h2>
      <NtTimeline :items="introEntries" empty="还没有自省记录" />
    </section>

    <section v-show="tab === 'proposal'" class="nt-panel">
      <h2 class="nt-panel__title">进化提案</h2>
      <NtTable :columns="proposalColumns" :rows="proposalRows" row-key="id" dense empty="还没有进化提案">
        <template #cell-createdAt="{ row }">{{ clock(row.createdAt) }}</template>
        <template #cell-decidedAt="{ row }">{{ row.decidedAt ? clock(row.decidedAt) : '—' }}</template>
        <template #cell-reason="{ row }">{{ row.reason || '—' }}</template>
        <template #cell-status="{ row }">
          <NtBadge :tone="proposalStatus(row.status).tone">{{ proposalStatus(row.status).text }}</NtBadge>
        </template>
      </NtTable>

      <!-- 批次 A7：待审提案卡——参数 diff + 指标对比 + 理由必填（盲签→明签）。 -->
      <template v-if="pendingProposals.length">
        <h3 class="sub">待审批 · {{ pendingProposals.length }} 条</h3>
        <article v-for="p in pendingProposals" :key="p.id" class="pending__card">
          <header class="pending__head">
            <b class="pending__title">{{ p.title }}</b>
            <span class="pending__meta">{{ p.strategy || '—' }} · 提出 {{ clock(p.createdAt) }}</span>
          </header>
          <p v-if="p.body" class="pending__body">{{ p.body }}</p>
          <div class="pending__grids">
            <div class="pending__grid">
              <h4 class="pending__sub">参数改动</h4>
              <NtTable
                :columns="diffColumns"
                :rows="diffRowsOf(p)"
                row-key="key"
                dense
                empty="提案未携带参数（旧格式），无法对比"
              >
                <template #cell-base="{ row }">{{ row.base == null ? '（新增）' : row.base }}</template>
                <template #cell-next="{ row }">
                  <span :class="{ 'diff-changed': row.changed }">{{ row.next == null ? '（删除）' : row.next }}</span>
                </template>
              </NtTable>
            </div>
            <div class="pending__grid">
              <h4 class="pending__sub">指标对比（透传口径见提案正文）</h4>
              <NtTable
                :columns="metricColumns"
                :rows="metricRowsOf(p)"
                row-key="key"
                dense
                empty="提案未携带指标对比"
              />
            </div>
          </div>
          <div v-if="!auth.isViewer()" class="pending__decide">
            <div class="pending__reason">
              <NtInput v-model="decideReasons[p.id]" placeholder="审批理由（必填，写入审计）" />
            </div>
            <NtButton size="sm" variant="success" :loading="actingId === p.id" @click="decide(p.id, 'approve')"
              >通过</NtButton
            >
            <NtButton size="sm" variant="danger" :loading="actingId === p.id" @click="decide(p.id, 'reject')"
              >拒绝</NtButton
            >
          </div>
        </article>
      </template>
    </section>

    <section v-show="tab === 'admission'" class="nt-panel">
      <h2 class="nt-panel__title">准入检查表</h2>
      <NtSelect
        v-if="strategies.length"
        v-model="strategy"
        label="策略"
        :options="strategies.map((item) => ({ value: item.name, label: item.title || item.name }))"
      />
      <p class="nt-note">模拟盘连续满 {{ admission?.requiredDays ?? 20 }} 个交易日且全部项通过才允许半自动实盘。</p>
      <ol v-if="admission?.items.length" class="checks">
        <li v-for="item in admission.items" :key="item.key" class="checks__item" :class="{ fail: item.status === 'fail' }">
          <span class="checks__verdict">{{ item.status === 'pass' ? '✓' : item.status === 'unknown' ? '·' : '✗' }}</span>
          <span class="checks__label">{{ item.label }}</span>
          <span v-if="item.detail" class="checks__detail">{{ item.detail }}</span>
        </li>
      </ol>
      <p v-else class="nt-note">还没有准入数据。</p>
    </section>
  </div>
</template>

<style scoped>
.sub {
  margin: 14px 0 0;
  font-size: 12.5px;
  font-weight: 600;
}

/* 批次 A7：待审提案卡——diff 两栏栅格 + 理由必填行。 */
.pending__card {
  margin-top: 10px;
  display: grid;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-2);
}

.pending__head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
}

.pending__title {
  font-size: 13px;
  color: var(--nt-text-1);
}

.pending__meta {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--nt-text-3);
}

.pending__body {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--nt-text-2);
  white-space: pre-wrap;
}

.pending__grids {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 10px;
}

.pending__grid {
  min-width: 0;
}

.pending__sub {
  margin: 0 0 6px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--nt-text-2);
}

.pending__decide {
  display: flex;
  align-items: center;
  gap: 8px;
}

.pending__reason {
  flex: 1;
  min-width: 0;
}

.pending__reason :deep(input) {
  width: 100%;
}

/* diff 变更格高亮走 accent——参数改动无涨跌语义，不用 up/down 色。 */
.diff-changed {
  color: var(--nt-accent);
  font-weight: 600;
}

.checks {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 6px;
}

.checks__item {
  display: flex;
  gap: 10px;
  align-items: baseline;
  padding: 8px 12px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-2);
  font-size: 12.5px;
}

.checks__item.fail {
  border-color: color-mix(in srgb, var(--nt-danger) 40%, transparent);
}

.checks__verdict {
  color: var(--nt-success);
  font-family: var(--nt-font-mono);
}

.checks__item.fail .checks__verdict {
  color: var(--nt-danger);
}

.checks__label {
  color: var(--nt-text-1);
  flex: 1;
}

.checks__detail {
  color: var(--nt-text-3);
  font-size: 11.5px;
}
</style>
