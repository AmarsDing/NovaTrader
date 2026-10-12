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
  type ShadowStat,
} from '@/api/backtest'
import { useAuthStore } from '@/stores/auth'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTabs from '@/ui/NtTabs.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import { clock, messageOf, pctText } from '@/utils/format'

const auth = useAuthStore()

const tab = ref('shadow')
const loading = ref(false)
const error = ref('')

const shadow = ref<ShadowStat[]>([])
const introspections = ref<Introspection[]>([])
const proposals = ref<Proposal[]>([])
const admission = ref<Admission | null>(null)
const strategies = ref<StrategyMeta[]>([])
const strategy = ref('')

const running = ref(false)
const actingId = ref('')

type ShadowRow = ShadowStat & Record<string, unknown>
const shadowColumns: NtTableColumn<ShadowRow>[] = [
  { key: 'name', title: '指标' },
  { key: 'live', title: '实盘', align: 'right' },
  { key: 'shadow', title: '影子', align: 'right' },
  { key: 'delta', title: '差值', align: 'right' },
]
const shadowRows = computed(() => shadow.value as unknown as ShadowRow[])

const introEntries = computed<TimelineEntry[]>(() =>
  introspections.value.map((r) => ({
    id: r.id,
    time: clock(r.at),
    title: r.title || '（无标题）',
    body: r.body,
    tag: r.kind,
    tone: 'neutral',
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
      <NtTable :columns="shadowColumns" :rows="shadowRows" row-key="name" dense empty="还没有影子统计">
        <template #cell-live="{ row }">{{ pctText(row.live) }}</template>
        <template #cell-shadow="{ row }">{{ pctText(row.shadow) }}</template>
        <template #cell-delta="{ row }">
          <span :class="(row.delta ?? 0) >= 0 ? 'up' : 'down'">{{ pctText(row.delta) }}</span>
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
