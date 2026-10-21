<script setup lang="ts">
/**
 * 信号中枢：全部信号的卡片流。
 * 订阅 signal 主题 2 秒节流刷新；支持 方向/状态/模板 过滤与排序；
 * 有效期倒计时，过期置灰；85 分以上按发光预算高亮。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { SignalItem } from '@/api/signals'
import { listSources } from '@/api/datahub'
import { useAccountStore } from '@/stores/account'
import { useMarketStore } from '@/stores/market'
import { useRiskStore } from '@/stores/risk'
import { useSignalsStore } from '@/stores/signals'
import { createPageRefresh } from './page-refresh'
import {
  buildDecisionAction,
  hasAvailableSource,
  isPendingStatus,
  type DecisionContext,
} from './decision-desk'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import ConditionalPlans from './ConditionalPlans.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtProgressRing from '@/ui/NtProgressRing.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtRadar from '@/viz/NtRadar.vue'
import SymbolName from '@/display/SymbolName.vue'
import { statusOf } from '@/ui/status-map'
import {
  clock,
  dimValue,
  isValidExpired,
  messageOf,
  pnlClass,
  price,
  pctText,
  ratioPct,
  sideLabel,
  strategyLabel,
  validRemainText,
} from '@/utils/format'
import { isLive, onPush, onResync, type WsPushMessage } from '@/ws/hub'
import { getWorkbench } from '@/workspace/inject'

type SideFilter = '' | 'buy' | 'sell'
type StateFilter = '' | 'active' | 'executed' | 'expired' | 'closed'
type SortKey = 'score' | 'time'

const signalStore = useSignalsStore()
const rows = computed(() => signalStore.ready ? signalStore.items : [])
const account = useAccountStore()
const market = useMarketStore()
const risk = useRiskStore()
const decisionContext = ref<DecisionContext | null>(null)
const error = ref('')
const loading = ref(false)
const sideFilter = ref<SideFilter>('')
const stateFilter = ref<StateFilter>('')
const strategyFilter = ref('')
const sortKey = ref<SortKey>('score')
const planSignalId = ref('')
/** 有效期与按钮状态同一时钟，避免过期后仍保留可执行入口。 */
const nowTick = ref(Date.now())
const labels = ['技术面', '消息面', '资金面', '外围']

const sideOptions = [
  { value: '', label: '全部方向' },
  { value: 'buy', label: '买入' },
  { value: 'sell', label: '卖出' },
]
const stateOptions = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '有效' },
  { value: 'executed', label: '已提交 / 已成交' },
  { value: 'expired', label: '已过期' },
  { value: 'closed', label: '已平仓' },
]
const sortOptions = [
  { value: 'score', label: '按综合分' },
  { value: 'time', label: '按信号时间' },
]

const strategies = computed(() => [...new Set(rows.value.map((row) => row.strategy).filter(Boolean))])
const strategyOptions = computed(() => [
  { value: '', label: '全部模板' },
  ...strategies.value.map((code) => ({ value: code, label: strategyLabel(code) })),
])

function isClosed(item: SignalItem): boolean {
  return item.status.toLowerCase() === 'closed'
}

function isHandled(item: SignalItem): boolean {
  return ['executing', 'done', 'closed'].includes(item.status.toLowerCase())
}

function isExpired(item: SignalItem): boolean {
  return !isHandled(item) && (item.status.toLowerCase() === 'expired' || isValidExpired(item.validUntil, nowTick.value))
}

const filtered = computed(() => {
  const list = rows.value.filter((item) => {
    if (sideFilter.value && item.side !== sideFilter.value) return false
    if (strategyFilter.value && item.strategy !== strategyFilter.value) return false
    if (stateFilter.value === 'active' && (!isPendingStatus(item.status) || isExpired(item))) return false
    if (stateFilter.value === 'executed' && !['executing', 'done'].includes(item.status.toLowerCase())) return false
    if (stateFilter.value === 'expired' && !isExpired(item)) return false
    if (stateFilter.value === 'closed' && !isClosed(item)) return false
    return true
  })
  return [...list].sort((a, b) => {
    if (sortKey.value === 'time') return String(b.time).localeCompare(String(a.time))
    return (b.score ?? 0) - (a.score ?? 0)
  })
})

const activeCount = computed(() => rows.value.filter((item) => isPendingStatus(item.status) && !isExpired(item)).length)
let loadSequence = 0
let contextVersion = 0
let disposed = false
let offResync: (() => void) | undefined
const refreshKey = () => `${account.book}:${contextVersion}`
const refreshes = createPageRefresh({ key: refreshKey, read: readPage, pushIntervalMs: 2000 })

function load() { return refreshes.refresh() }

async function readPage(key: string): Promise<boolean> {
  const sequence = ++loadSequence
  const checkedBook = account.book
  loading.value = true
  error.value = ''
  const [signalRes, accountRes, riskRes, marketRes, sourceRes] = await Promise.allSettled([
    signalStore.refresh(),
    account.refresh(),
    risk.refresh(),
    market.refresh(),
    listSources(),
  ])
  if (disposed || key !== refreshKey() || sequence !== loadSequence || checkedBook !== account.book) return false
  nowTick.value = Date.now()
  const failed = [signalRes, accountRes, riskRes, marketRes].find((result) => result.status === 'rejected')
  const ready = [signalRes, accountRes, riskRes, marketRes].every((result) => result.status === 'fulfilled' && result.value === true)
  if (!ready) error.value = failed?.status === 'rejected' ? messageOf(failed.reason) : '核心状态未恢复，不能执行信号'
  decisionContext.value = {
    checkedAt: nowTick.value,
    accountBook: checkedBook,
    accountReady: accountRes.status === 'fulfilled' && accountRes.value && account.accountOk,
    positionsReady: account.positionsOk,
    signalsReady: signalRes.status === 'fulfilled' && signalRes.value && signalStore.ready,
    riskReady: riskRes.status === 'fulfilled' && riskRes.value && risk.ready,
    riskMode: risk.ready ? risk.mode : '',
    killActive: risk.ready && risk.killActive,
    openHalted: account.accountOk && account.openHalted,
    haltReason: account.accountOk ? account.haltReason : '',
    marketReady: marketRes.status === 'fulfilled' && marketRes.value && market.ready,
    marketAsOf: market.sentimentAsOf,
    marketStale: !market.ready || market.sentimentStale,
    sourcesReady: sourceRes.status === 'fulfilled' && hasAvailableSource(sourceRes.value),
    live: isLive(),
    positions: account.positionsOk ? account.positions : [],
  }
  loading.value = false
  return ready
}

function actionOf(item: SignalItem) {
  return buildDecisionAction(
    item,
    decisionContext.value && { ...decisionContext.value, accountBook: account.book, live: isLive() },
    nowTick.value,
  )
}

function openTrade(item: SignalItem) {
  if (
    !buildDecisionAction(
      item,
      decisionContext.value && { ...decisionContext.value, accountBook: account.book, live: isLive() },
      Date.now(),
    ).canOpenTrade
  )
    return
  getWorkbench()?.openPanel('trade', {
    symbol: item.symbol,
    side: item.side,
    price: item.entry != null ? String(item.entry) : '',
    signalId: item.id,
    strategyVersionId: item.strategyVersionId,
    book: item.book,
    validUntil: item.validUntil,
    traceId: item.traceId,
    requestedPositionPct: item.positionPct,
  })
}

function dimsOf(item: SignalItem) {
  return labels.map((label) => ({ label, value: dimValue(item.dims, label) }))
}

/** 信号行自带时间戳 → epoch ms；丢给 NtFreshness，无时点证据显「时点未知」（不冒充 0s）。 */
function timeOf(value: string): number | null {
  const ts = new Date(value).getTime()
  return Number.isFinite(ts) ? ts : null
}

/** 状态章文案/tone 出自 signal 册（§7.1，一字锁定）：终态优先于过期判定，未终态且 validUntil 已过按「已过期」论；
 *  remain 仅未终态未过期信号给出（章侧附剩余时效，format 工具函数非自封文案）；册外状态返回 null 文案，模板原样回显。 */
function stateBadge(item: SignalItem) {
  const status = item.status.toLowerCase()
  if (status === 'rejected') return { entry: statusOf('signal.rejected'), remain: '' }
  if (status === 'cancelled' || status === 'canceled') return { entry: statusOf('signal.cancelled'), remain: '' }
  if (status === 'executing') return { entry: statusOf('signal.executing'), remain: '' }
  if (status === 'done') return { entry: statusOf('signal.done'), remain: '' }
  if (status === 'closed') return { entry: statusOf('signal.closed'), remain: '' }
  if (isExpired(item)) return { entry: statusOf('signal.expired'), remain: '' }
  const remain = validRemainText(item.validUntil, nowTick.value)
  if (status === 'pending') return { entry: statusOf('signal.pending'), remain }
  if (status === 'risk_checking') return { entry: statusOf('signal.risk_checking'), remain }
  if (status === 'approved') return { entry: statusOf('signal.approved'), remain }
  return { entry: null, remain }
}

let offPush: (() => void) | undefined
let tickTimer: ReturnType<typeof setInterval> | null = null
let refreshTimer: ReturnType<typeof setInterval> | null = null

async function onPushMessage(msg: WsPushMessage) {
  if (!['signal', 'position', 'alert'].includes(msg.topic)) return
  const key = refreshKey()
  const ready = await refreshes.fromPush()
  if (!disposed && key === refreshKey() && !ready) throw new Error('信号面板核心状态尚未恢复')
}

function onVisible() {
  if (!document.hidden) { nowTick.value = Date.now(); void load() }
}

onMounted(() => {
  void load()
  // signal 属常驻主题（BASE_TOPICS），只需 onPush，不必再 subscribe。
  offPush = onPush(onPushMessage)
  tickTimer = setInterval(() => {
    if (!document.hidden) nowTick.value = Date.now()
  }, 1000)
  refreshTimer = setInterval(() => {
    if (!document.hidden) void load()
  }, 30_000)
  offResync = onResync(['signal', 'position', 'alert'], async () => {
    const key = refreshKey()
    const ready = await load()
    if (!disposed && key === refreshKey() && !ready) throw new Error('信号面板核心状态尚未恢复')
  })
  document.addEventListener('visibilitychange', onVisible)
})

onBeforeUnmount(() => {
  disposed = true
  contextVersion += 1
  refreshes.dispose()
  offPush?.()
  offResync?.()
  document.removeEventListener('visibilitychange', onVisible)
  if (tickTimer) clearInterval(tickTimer)
  if (refreshTimer) clearInterval(refreshTimer)
})
watch(
  () => account.book,
  () => {
    contextVersion += 1
    decisionContext.value = null
    void load()
  },
  { flush: 'sync' },
)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">信号中枢</h1>
        <p class="nt-note">
          先读行动计划，再核对四维与证据。策略评分不是胜率，提交前重新校验价格、数量与风控。
          <span v-if="rows.length">有效 {{ activeCount }} / 共 {{ rows.length }}</span>
        </p>
      </div>
      <NtButton :loading="loading" @click="load">刷新</NtButton>
    </header>

    <div class="filters">
      <NtSelect v-model="sideFilter" :options="sideOptions" />
      <NtSelect v-model="stateFilter" :options="stateOptions" />
      <NtSelect v-model="strategyFilter" :options="strategyOptions" />
      <NtSelect v-model="sortKey" :options="sortOptions" />
    </div>
    <ConditionalPlans :signal-id="planSignalId" />

    <p v-if="loading" class="nt-empty">读取中…</p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="!loading && !rows.length" class="nt-empty">{{ signalStore.ready ? '今日没有信号。' : '信号状态尚未恢复，不能判断是否有机会。' }}</p>
    <p v-else-if="!loading && !filtered.length" class="nt-empty">当前过滤条件下没有信号。</p>
    <div v-if="filtered.length" class="grid">
      <article
        v-for="item in filtered"
        :key="item.id || item.symbol + item.time"
        class="card"
        :class="{
          hot: item.highValue,
          stale: isHandled(item) || isExpired(item) || ['rejected', 'cancelled'].includes(item.status.toLowerCase()),
        }"
      >
        <header>
          <!-- §6.3 R2 信号行：证券位统一 SymbolName；名称取 security 字典（signals API 已回填），不再双数据源 -->
          <SymbolName :code="item.symbol" mode="stacked" />
          <span class="tm">{{ clock(item.time) }}</span>
          <!-- 红涨绿跌：买入红、卖出绿 -->
          <em :class="item.side === 'buy' ? 'up' : item.side === 'sell' ? 'down' : ''">{{ sideLabel(item.side) }}</em>
          <!-- 状态章文案/tone 出自 signal 册（§7.1，一字锁定）；未终态且有效期已过按「已过期」论，
               册外状态码原样回显（禁自封）；未终态信号章侧附剩余时效（非状态文案）。 -->
          <NtBadge :tone="stateBadge(item).entry?.tone ?? 'neutral'">{{ stateBadge(item).entry?.text ?? (item.status || '—') }}</NtBadge>
          <span v-if="stateBadge(item).remain" class="tm">{{ stateBadge(item).remain }}</span>
          <!-- §7.3 首批挂载：卡头新鲜度取信号时间戳，无时点证据显「时点未知」 -->
          <NtFreshness label="信号" :at="timeOf(item.time)" />
        <section class="action-plan" :class="actionOf(item).state" aria-label="当前行动计划">
        </header>
          <h3>当前动作：{{ actionOf(item).title }}</h3>
          <p><strong>什么时候行动</strong>{{ actionOf(item).timing }}</p>
          <p><strong>失效条件</strong>{{ actionOf(item).invalidation }}</p>
          <p v-if="actionOf(item).blocker" class="action-plan__blocker">
            <strong>主要阻断</strong>{{ actionOf(item).blocker }}
          </p>
          <p class="action-plan__caution"><strong>先看反证</strong>{{ actionOf(item).caution }}</p>
        </section>
        <div class="body">
          <!-- hero 档（方案 §5.2-4 第 3 处）：评分环中央大数字走 --nt-text-0，≥85 由 hot 发光预算接管 -->
          <NtProgressRing :value="item.score" :hot="item.highValue" :size="56" hero />
          <template v-if="!item.degraded">
            <NtRadar :dims="dimsOf(item)" :size="84" />
            <ul>
              <li v-for="label in labels" :key="label">
                <span>{{ label }}</span>
                <b>{{ dimValue(item.dims, label) ?? '—' }}</b>
              </li>
            </ul>
          </template>
          <p v-else class="degraded-note">模型降级：四维未复核，仅有规则分 {{ item.ruleScore ?? '—' }}</p>
        </div>
        <p v-if="isHandled(item)" class="nt-note">账本已核成交 {{ item.executedVolume ?? '—' }} 股 · 均价 {{ price(item.executedPrice ?? null) }}。已有成交不代表整单完成，余量以原订单为准。</p>
        <dl>
          <div>
            <dt>入场</dt>
            <dd>{{ price(item.entryLow) }} – {{ price(item.entryHigh) }}</dd>
          </div>
          <div>
            <dt>止损</dt>
            <dd class="down">{{ price(item.stop) }}</dd>
          </div>
          <div>
            <dt>止盈</dt>
            <dd class="up">{{ price(item.take) }}</dd>
          </div>
          <div>
            <dt>建议仓位</dt>
            <dd>{{ item.positionPct != null ? ratioPct(item.positionPct, 0) : '—' }}</dd>
          </div>
          <div>
          <div>
            <!-- R2 列位补齐：有效期 text-2；validUntil 缺失显「—」（数据纪律） -->
            <dt>有效期</dt>
            <dd class="t2">{{ item.validUntil ? clock(item.validUntil) : '—' }}</dd>
          </div>
            <dt>ATR</dt>
            <dd>{{ price(item.atr) }}</dd>
          </div>
          <div v-if="item.hasPnl">
            <dt>后验盈亏</dt>
            <dd :class="pnlClass(item.pnlPct)">
              {{ pctText(item.pnlPct)
              }}<template v-if="item.holdingDays != null"> · {{ item.holdingDays }} 日</template>
            </dd>
          </div>
        </dl>
        <p class="meta">
          {{ strategyLabel(item.strategy) }} · 规则 {{ item.ruleScore ?? '—' }} · AI {{ item.aiScore ?? '—' }} · 账本
          {{ item.book || '未返回' }} · 版本 {{ item.strategyVersionId || '未返回' }}
          <template v-if="item.holdDaysMax != null"> · 最长持有 {{ item.holdDaysMax }} 日</template>
          <template v-if="item.traceId"> · {{ item.traceId }}</template>
        </p>
        <p v-if="item.reason" class="reason">{{ item.reason }}</p>
        <ul v-if="item.evidence.length" class="evidence">
          <li v-for="(line, index) in item.evidence" :key="index">{{ line }}</li>
        </ul>
        <button
          v-if="!isHandled(item) && !isExpired(item)" type="button" class="plan-open" @click="planSignalId = item.id"
        >查看 / 冻结条件计划</button>
        <button
          v-if="!isHandled(item)"
          type="button"
          class="go"
          :disabled="!actionOf(item).canOpenTrade"
          @click="openTrade(item)"
        >
          准备模拟盘确认
        </button>
      </article>
    </div>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.filters :deep(.nt-select) {
  min-width: 120px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 12px;
}

.card {
  padding: var(--nt-pad-panel);
  display: flex;
  flex-direction: column;
  gap: 10px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

/* AI 信号 ≥85：发光预算允许的高亮。 */
.card.hot {
  border-color: var(--nt-accent-border);
  box-shadow: var(--nt-glow-accent);
}

/* 已过期/已平仓：置灰，不再可执行。 */
.card.stale {
  background: var(--nt-bg-0);
  border-style: dashed;
}

header {
  display: flex;
  gap: 8px;
  align-items: baseline;
  flex-wrap: wrap;
}

.tm {
  color: var(--nt-text-3);
  font-size: 11px;
  font-family: var(--nt-font-mono);
}

em {
  margin-left: auto;
  font-style: normal;
  font-weight: 600;
}

.body {
  display: grid;
  grid-template-columns: 56px 84px 1fr;
  gap: 10px;
  align-items: center;
}

.degraded-note {
  grid-column: 2 / 4;
  margin: 0;
  font-size: 12px;
  color: var(--nt-warn);
}

ul {
  list-style: none;
  margin: 0;
  padding: 0;
}

.body li,
dl {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
}

dl {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px 12px;
  margin: 0;
  font-family: var(--nt-font-mono);
}

dt {
  color: var(--nt-text-3);
}

dd {
  margin: 0;
}

.meta,
/* R2 有效期位降一档（text-2），不打断 mono 价格列的节奏。 */
dd.t2 {
  color: var(--nt-text-2);
}

.reason {
  margin: 0;
  color: var(--nt-text-2);
  font-size: 11.5px;
}

.evidence {
  color: var(--nt-text-2);
  font-size: 11.5px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.go, .plan-open {
  align-self: flex-start;
  padding: 5px 12px;
  font-size: 12px;
  font-weight: 600;
  font-family: inherit;
  color: var(--nt-accent);
  background: var(--nt-accent-soft);
  border: 1px solid var(--nt-accent-border);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.go:hover, .plan-open:hover {
  background: var(--nt-accent-hover);
}

.go:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.action-plan {
  border-top: 1px solid var(--nt-border-1);
  border-bottom: 1px solid var(--nt-border-1);
  padding: 8px 0;
}

.action-plan h3 {
  margin: 0 0 6px;
  font-size: 12.5px;
  color: var(--nt-text-1);
}

.action-plan p {
  margin: 4px 0;
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--nt-text-2);
}

.action-plan strong {
  margin-right: 6px;
  color: var(--nt-text-3);
  font-weight: 500;
}

.action-plan .action-plan__blocker,
.action-plan .action-plan__caution {
  color: var(--nt-warn);
}
</style>
