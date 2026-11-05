<script setup lang="ts">
/**
 * 模型运行链路面板（M05）：分层（tier）调用统计 + 令牌用量 + 手动触发分析链路观察。
 * FE-B1/W0-3 合并：原「模型统计」面板的失败率汇总与数据时点锚已并入本面板，不再单开面板。
 * 数据走 useBrainStore.tiers（connection-watchdog 周期刷新），分析进度走 brain 主题 WS 推送。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { analyzeBrain, brainUsage, type Analysis, type BrainTierUsage, type BrainUsage } from '@/api/brain'
import { useBrainStore } from '@/stores/brain'
import NtBadge, { type BadgeTone } from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtStatCard, { type StatTone } from '@/display/NtStatCard.vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import SymbolName from '@/display/SymbolName.vue'
import { linkStatus } from '@/ui/status-map'
import { clock, compactAmount, intText, messageOf, num, ratioPct } from '@/utils/format'
import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'

const brain = useBrainStore()

const symbol = ref('')
const analyzing = ref(false)
const result = ref<Analysis | null>(null)
const error = ref('')
const progress = ref<TimelineEntry[]>([])
/** 推理进度 done/total（WS brain 主题帧里带）。 */
const progDone = ref<number | null>(null)
const progTotal = ref<number | null>(null)

const progPct = computed(() => {
  if (progDone.value == null || progTotal.value == null || progTotal.value <= 0) return null
  return Math.min(100, Math.round((progDone.value / progTotal.value) * 100))
})

interface TierRow {
  tier: string
  model: string
  calls: number | null
  failures: number | null
  p50Ms: number | null
  p95Ms: number | null
  kvCacheUsage: number | null
  inFlight: number | null
  queued: number | null
  state: string
  [key: string]: unknown
}

const columns: NtTableColumn<TierRow>[] = [
  { key: 'tier', title: '层级' },
  { key: 'model', title: '模型', mono: true },
  { key: 'calls', title: '调用', align: 'right' },
  { key: 'failures', title: '失败', align: 'right' },
  { key: 'p50Ms', title: 'P50(ms)', align: 'right' },
  { key: 'p95Ms', title: 'P95(ms)', align: 'right' },
  { key: 'kvCacheUsage', title: 'KV缓存', align: 'right' },
  { key: 'inFlight', title: '在飞', align: 'right' },
  { key: 'queued', title: '排队', align: 'right' },
  { key: 'state', title: '状态' },
]

const rows = computed<TierRow[]>(() =>
  brain.tiers.map((t) => ({
    tier: t.tier || '—',
    model: t.model || '—',
    calls: t.calls,
    failures: t.failures,
    p50Ms: t.p50Ms,
    p95Ms: t.p95Ms,
    kvCacheUsage: t.kvCacheUsage,
    inFlight: t.inFlight,
    queued: t.queued,
    state: t.circuitOpen ? '熔断' : t.configured ? '正常' : '未配置',
  })),
)

const lastScore = computed(() => (result.value?.score == null ? '—' : String(Math.round(result.value.score))))
/** 评分着色与四维同一阈值（≥60 行动主色，<60 琥珀）——分数在同屏两套标尺是撒谎。 */
const scoreTone = computed(() => (result.value?.score == null ? '' : result.value.score >= 60 ? 'ok' : 'low'))

/** 汇总口径：逐层缺失不计入、全缺返回 null（数据纪律「没取到≠0」，null 由 intText 显「—」）。 */
function sumNullable(vals: (number | null)[]): number | null {
  let acc = 0
  let seen = false
  for (const v of vals) {
    if (v != null) {
      acc += v
      seen = true
    }
  }
  return seen ? acc : null
}

/** 层级状态章色调（词随 status-map 在途波入 brain.state 册后迁 statusOf）。 */
function tierBadgeTone(state: string): BadgeTone {
  if (state === '熔断') return 'danger'
  if (state === '正常') return 'success'
  return 'neutral'
}

const totalCalls = computed(() => sumNullable(brain.tiers.map((t) => t.calls)))
const totalFailures = computed(() => sumNullable(brain.tiers.map((t) => t.failures)))
const circuitCount = computed(() => brain.tiers.filter((t) => t.circuitOpen).length)
const failuresTone = computed<StatTone>(() => (totalFailures.value ? 'danger' : 'neutral'))
const circuitTone = computed<StatTone>(() => (circuitCount.value ? 'danger' : 'neutral'))
/** FE-B1 并入：总失败率=已知层之和/总调用（调用全缺为 null 显「—」；失败缺失层按 0 参与，口径同原模型统计）。 */
const failRate = computed(() =>
  totalCalls.value != null && totalCalls.value > 0 ? (totalFailures.value ?? 0) / totalCalls.value : null,
)
const failRateTone = computed<StatTone>(() => (failRate.value != null && failRate.value > 0.05 ? 'danger' : 'neutral'))

/* ---------- 批次 A2：令牌用量（Brain.Usage 接口早已就绪，本批接线） ---------- */
/** 私有化部署的算力账：窗口内 prompt/completion tokens 与调用/失败计数，逐层可拆开。 */
const usageWindow = ref<string>('today')
const usageWindowOptions = [
  { value: 'today', label: '今日' },
  { value: '1h', label: '近 1 小时' },
  { value: '24h', label: '近 24 小时' },
]
const usage = ref<BrainUsage | null>(null)
/** 独立降级：用量接口失败不拖死链路统计（ready 旗标同 limit-ladder 口径）。 */
const usageReady = ref(false)
const usageLoading = ref(false)

type UsageRow = BrainTierUsage & Record<string, unknown>
const usageColumns: NtTableColumn<UsageRow>[] = [
  { key: 'tier', title: '层级' },
  { key: 'model', title: '模型', mono: true },
  { key: 'promptTokens', title: '输入 tokens', align: 'right', mono: true },
  { key: 'completionTokens', title: '输出 tokens', align: 'right', mono: true },
  { key: 'totalTokens', title: '合计 tokens', align: 'right', mono: true },
  { key: 'calls', title: '调用', align: 'right' },
  { key: 'failures', title: '失败', align: 'right' },
]
const usageRows = computed<UsageRow[]>(() => (usage.value?.tiers ?? []) as UsageRow[])

async function loadUsage() {
  usageLoading.value = true
  try {
    usage.value = await brainUsage(usageWindow.value as 'today' | '1h' | '24h')
    usageReady.value = true
  } catch {
    usage.value = null
    usageReady.value = false
  } finally {
    usageLoading.value = false
  }
}

function refreshAll() {
  void brain.refresh()
  void loadUsage()
}

watch(usageWindow, () => void loadUsage())

/** §10-15：四维得分→链路状态键（无分 off / <60 stale / 其余 ok），tone 由 NtStatusDot 承接，文案走 linkStatus 册。 */
function dimStatus(dim: { score: number | null }): 'off' | 'stale' | 'ok' {
  if (dim.score == null) return 'off'
  return dim.score >= 60 ? 'ok' : 'stale'
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'brain') return
  const data = (msg.data ?? {}) as Record<string, unknown>
  const stage = String(data.stage || data.phase || 'progress')
  const text = String(data.message || data.symbol || '')
  const done = num(data.done)
  const total = num(data.total)
  if (done != null) progDone.value = done
  if (total != null) progTotal.value = total
  progress.value = [
    { id: `${Date.now()}-${progress.value.length}`, time: stage, title: text || stage, tone: 'accent' },
    ...progress.value.slice(0, 19),
  ]
}

async function runAnalysis() {
  const code = symbol.value.trim()
  if (!code) return
  analyzing.value = true
  error.value = ''
  progress.value = []
  progDone.value = null
  progTotal.value = null
  try {
    result.value = await analyzeBrain({ symbol: code })
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    analyzing.value = false
  }
}

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined

onMounted(() => {
  refreshAll()
  unsubscribe = subscribe('brain')
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
  offPush?.()
  unsubscribe?.()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">模型运行链路</h1>
        <p class="nt-note">分层调用统计随连接看门狗周期刷新（10s）；手动分析可观察推理过程。</p>
      </div>
      <div class="head-side">
        <!-- 直觉5：10s 周期刷新的健康数字必须有「数据多老」锚点，否则分不清现场与尸体 -->
        <NtFreshness label="模型" :at="brain.lastOkAt" :stale-after-ms="15000" />
        <NtButton size="sm" :loading="brain.loading || usageLoading" @click="refreshAll">刷新统计</NtButton>
      </div>
    </header>

    <section class="nt-panel">
      <h2 class="nt-panel__title">分层统计</h2>
      <!-- 先给结论再给明细：总量/失败/熔断三卡收口，熔断红章在明细行里点名 -->
      <div class="nt-cards kpi">
        <NtStatCard label="总调用" :value="intText(totalCalls)" sub="全层合计" />
        <NtStatCard label="失败" :value="intText(totalFailures)" sub="全层合计" :tone="failuresTone" />
        <!-- FE-B1：原模型统计的失败率口径移此——一张卡顶一个面板。 -->
        <NtStatCard label="失败率" :value="ratioPct(failRate)" :tone="failRateTone" sub="失败 / 总调用" />
        <NtStatCard label="熔断层" :value="`${circuitCount} / ${rows.length}`" sub="熔断 / 已上报" :tone="circuitTone" />
      </div>
      <NtTable
        :columns="columns"
        :rows="rows"
        row-key="tier"
        dense
        :empty="brain.loading ? '读取中…' : brain.ready ? '还没有模型层上报统计' : '统计未返回——模型服务无应答，点右上刷新统计重试'"
      >
        <template #cell-kvCacheUsage="{ row }">
          <!-- kvCacheUsage 是份额口径（0.32=32%），走 ratioPct；null=服务未上报，不伪装 0%。 -->
          {{ row.kvCacheUsage == null ? '—' : ratioPct(row.kvCacheUsage, 0) }}
        </template>
        <template #cell-state="{ row }">
          <NtBadge :tone="tierBadgeTone(row.state)" dot>{{ row.state }}</NtBadge>
        </template>
      </NtTable>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">
        令牌用量
        <span v-if="usage?.since" class="nt-note">窗口自 {{ clock(usage.since) }}</span>
      </h2>
      <p class="usage-head">
        <NtSelect v-model="usageWindow" label="窗口" :options="usageWindowOptions" />
      </p>
      <p v-if="!usageReady" class="nt-empty">用量未返回——模型服务无应答或 llm_call_log 未开启。</p>
      <template v-else-if="usage">
        <div class="nt-cards kpi">
          <NtStatCard label="合计 tokens" :value="compactAmount(usage.total.totalTokens)" sub="输入 + 输出" />
          <NtStatCard label="输入 tokens" :value="compactAmount(usage.total.promptTokens)" />
          <NtStatCard label="输出 tokens" :value="compactAmount(usage.total.completionTokens)" />
          <NtStatCard
            label="调用 / 失败"
            :value="`${intText(usage.total.calls)} / ${intText(usage.total.failures)}`"
            :tone="usage.total.failures ? 'danger' : 'neutral'"
            sub="全层合计"
          />
        </div>
        <NtTable :columns="usageColumns" :rows="usageRows" row-key="tier" dense empty="窗口内没有模型调用">
          <template #cell-promptTokens="{ row }">{{ compactAmount(row.promptTokens) }}</template>
          <template #cell-completionTokens="{ row }">{{ compactAmount(row.completionTokens) }}</template>
          <template #cell-totalTokens="{ row }">{{ compactAmount(row.totalTokens) }}</template>
        </NtTable>
      </template>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">手动触发分析</h2>
      <div class="run">
        <NtInput v-model="symbol" placeholder="如 000001.SZ" class="run__symbol" @keydown.enter="runAnalysis" />
        <NtButton size="sm" :loading="analyzing" :disabled="!symbol.trim()" @click="runAnalysis">分析</NtButton>
        <span v-if="result" class="run__score">
          得分 <b :class="scoreTone">{{ lastScore }}</b> · {{ result.verdict || '—' }}
        </span>
      </div>
      <NtErrorGuide :error="error" />
      <div v-if="progPct != null" class="prog">
        <div class="prog__track"><i class="prog__bar" :style="{ width: `${progPct}%` }" /></div>
        <span class="prog__text">{{ progDone }}/{{ progTotal }}</span>
      </div>
      <NtTimeline :items="progress" :empty="analyzing ? '等待推理进度推送…' : '暂无推理进度（WS brain 主题）'" />
    </section>

    <section v-if="result" class="nt-panel">
      <h2 class="nt-panel__title result__title">
        <!-- §10-15：自制「代码+名称」双件统一 SymbolName(inline)，缺名显「名称未返回」 -->
        最近结果 · <SymbolName :code="result.symbol" mode="inline" />
      </h2>
      <p class="nt-note">
        模型 {{ result.model || '—' }}<template v-if="result.generatedAt"> · 生成于 {{ clock(result.generatedAt) }}</template>
      </p>
      <ul class="dims">
        <li v-for="dim in result.dimensions" :key="dim.name">
          <NtStatusDot :status="dimStatus(dim)" :label="dim.name" />
          <b>{{ intText(dim.score) }}</b>
          <!-- 四维状态文案走 status-map 链路册（正常/陈旧/关闭），tone 仍由 NtStatusDot 键承担 -->
          <span class="dim-state">{{ linkStatus(dimStatus(dim)).text }}</span>
          <span>{{ dim.comment }}</span>
        </li>
      </ul>
      <template v-if="result.reasons.length">
        <h3 class="sub">理由</h3>
        <ul class="lines">
          <li v-for="(line, index) in result.reasons.slice(0, 5)" :key="index">{{ line }}</li>
        </ul>
      </template>
      <template v-if="result.risks.length">
        <h3 class="sub warn">风险（模型的反对意见）</h3>
        <ul class="lines warn">
          <li v-for="(line, index) in result.risks" :key="index">{{ line }}</li>
        </ul>
      </template>
    </section>
  </div>
</template>

<style scoped>
/* SymbolName 嵌进 h2：跟随标题字号、基线对齐。 */
.result__title :deep(.sn) {
  font-size: inherit;
}

.head-side {
  display: flex;
  align-items: center;
  gap: 10px;
}

.run {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.run__symbol {
  width: 160px;
}

.run__score {
  font-size: 12.5px;
  color: var(--nt-text-2);
}

.run__score b {
  font-family: var(--nt-font-mono);
  color: var(--nt-accent);
}

.run__score b.low {
  color: var(--nt-warn);
}

.kpi {
  margin-bottom: 10px;
}

.usage-head {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--nt-text-2);
}

/* 令牌用量卡内 select：宽度按内容给，不顶满整行。 */
.usage-head :deep(.nt-select select) {
  min-width: 110px;
}

.dims {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 8px;
}

.dims li {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 12.5px;
  color: var(--nt-text-2);
}

.dims b {
  font-family: var(--nt-font-mono);
  min-width: 2.2em;
  color: var(--nt-text-1);
}

.dim-state {
  font-size: 11px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

.prog {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.prog__track {
  flex: 1;
  height: 6px;
  background: var(--nt-bg-2);
  border-radius: 999px;
  overflow: hidden;
}

.prog__bar {
  display: block;
  height: 100%;
  background: var(--nt-accent);
  transition: width 0.4s ease;
}

.prog__text {
  font-size: 11px;
  color: var(--nt-text-3);
  font-family: var(--nt-font-mono);
  min-width: 48px;
  text-align: right;
}

.sub {
  margin: 12px 0 0;
  font-size: 12.5px;
  font-weight: 600;
}

.sub.warn {
  color: var(--nt-warn);
}

.lines {
  list-style: none;
  margin: 6px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
  color: var(--nt-text-2);
}

.lines.warn {
  color: var(--nt-warn);
}
</style>
