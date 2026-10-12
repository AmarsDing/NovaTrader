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
import NtSelect from '@/ui/NtSelect.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtTabs from '@/ui/NtTabs.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import { clock, intText, messageOf, pnlClass, ratioPct } from '@/utils/format'

const auth = useAuthStore()

const tab = ref('shadow')
const loading = ref(false)
const error = ref('')

const shadow = ref<ShadowStats | null>(null)
const introspections = ref<Introspection[]>([])
const proposals = ref<Proposal[]>([])
const admission = ref<Admission | null>(null)
const strategies = ref<StrategyMeta[]>([])
const strategy = ref('')

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
    tag: r.triggered ? '已触发' : '',
    tone: r.triggered ? 'warn' : 'neutral',
  })),
)

type ProposalRow = Proposal & Record<string, unknown>
const proposalColumns: NtTableColumn<ProposalRow>[] = [
  { key: 'title', title: '提案' },
  { key: 'strategy', title: '策略', mono: true },
  { key: 'status', title: '状态' },
  { key: 'createdAt', title: '提出时间', mono: true },
]
const proposalRows = computed(() => proposals.value as unknown as ProposalRow[])

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
  actingId.value = id
  error.value = ''
  try {
    if (action === 'approve') await approveProposal(id)
    else await rejectProposal(id)
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
          <NtBadge v-if="admission?.ready" tone="success" dot>已达准入</NtBadge>
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

    <p v-if="error" class="nt-err">{{ error }}</p>

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
        <template #cell-status="{ row }">
          <NtBadge :tone="row.status === 'approved' ? 'success' : row.status === 'rejected' ? 'danger' : 'warn'">
            {{ row.status || 'pending' }}
          </NtBadge>
        </template>
      </NtTable>
      <div v-if="!auth.isViewer()" class="actions">
        <template v-for="p in proposals" :key="p.id">
          <div v-if="p.status !== 'approved' && p.status !== 'rejected'" class="actions__row">
            <span class="actions__title">{{ p.title }}</span>
            <NtButton size="sm" variant="success" :loading="actingId === p.id" @click="decide(p.id, 'approve')"
              >通过</NtButton
            >
            <NtButton size="sm" variant="danger" :loading="actingId === p.id" @click="decide(p.id, 'reject')"
              >拒绝</NtButton
            >
          </div>
        </template>
      </div>
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

.actions {
  margin-top: 10px;
  display: grid;
  gap: 8px;
}

.actions__row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.actions__title {
  flex: 1;
  font-size: 12.5px;
  color: var(--nt-text-1);
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
  border-color: rgba(255, 58, 58, 0.4);
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
