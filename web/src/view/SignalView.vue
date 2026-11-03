<script setup lang="ts">
/**
 * 信号中枢：全部信号的卡片流。
 * 订阅 signal 主题 2 秒节流刷新；支持 方向/状态/模板 过滤与排序；
 * 有效期倒计时，过期置灰；85 分以上按发光预算高亮。
 * W2 收编：卡片票体全量走 TicketDetail（与决策台同一事实层：头/行动/价格/四维投票+评语/异议/
 * 理由证据/风控）；卡壳只保留评分环+雷达（above 槽）与状态章/新鲜度（head-extra 槽）。
 * FE-O2：接面板参数 symbol 预置代码搜索词（决策台漏选行钻取定位）。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { SignalItem } from '@/api/signals'
import { listSources } from '@/api/datahub'
import { getConfigValues } from '@/api/config'
import { useAccountStore } from '@/stores/account'
import { useMarketStore } from '@/stores/market'
import { useRiskStore } from '@/stores/risk'
import { useSignalsStore } from '@/stores/signals'
import { createPageRefresh } from './page-refresh'
import {
  buildDecisionAction,
  hasAvailableSource,
  isPendingStatus,
  toDeskTicket,
  type DecisionContext,
} from './decision-desk'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import ConditionalPlans from './ConditionalPlans.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtInput from '@/ui/NtInput.vue'
import NtFunnel, { type FunnelStage } from '@/display/NtFunnel.vue'
import NtSignalTimeline from '@/display/NtSignalTimeline.vue'
import NtMissFunnelHistory from '@/viz/NtMissFunnelHistory.vue'
import NtProgressRing from '@/ui/NtProgressRing.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtRadar from '@/viz/NtRadar.vue'
import TicketDetail from '@/display/TicketDetail.vue'
import { statusOf } from '@/ui/status-map'
import {
  clock,
  dimValue,
  isValidExpired,
  messageOf,
  strategyLabel,
  validRemainText,
} from '@/utils/format'
import { serverNow } from '@/utils/server-clock'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'
import { isLive, onPush, onResync, type WsPushMessage } from '@/ws/hub'
import { getWorkbench } from '@/workspace/inject'
import { consumePanelParams, onPanelParams } from '@/workspace/panel-bus'

type SideFilter = '' | 'buy' | 'sell'
type StateFilter = '' | 'active' | 'executed' | 'expired' | 'closed'
type SortKey = 'score' | 'time'

// W3-2 面板偏好持久化：键版勿改，旧档自然失效。
const PREFS_KEY = 'nt.prefs.view.signal.v1'
const PREFS_ALLOW = {
  side: ['', 'buy', 'sell'],
  state: ['', 'active', 'executed', 'expired', 'closed'],
  sort: ['score', 'time'],
  strategy: '*',
} as const
// 浏览筛选回显（非查询输入）；读档须在挂载拉数前完成。
const savedPrefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)

const signalStore = useSignalsStore()
const rows = computed(() => signalStore.ready ? signalStore.items : [])
const account = useAccountStore()
const market = useMarketStore()
const risk = useRiskStore()
const decisionContext = ref<DecisionContext | null>(null)
const error = ref('')
const loading = ref(false)
/** W2 收编：单股上限（risk.max_single_pct，与决策台同源）只喂票体风控块估算，读不到=null 显「以风控实检为准」。 */
const maxSinglePct = ref<number | null>(null)
const sideFilter = ref<SideFilter>((savedPrefs.side as SideFilter | undefined) ?? '')
const stateFilter = ref<StateFilter>((savedPrefs.state as StateFilter | undefined) ?? '')
const strategyFilter = ref(savedPrefs.strategy ?? '')
const sortKey = ref<SortKey>((savedPrefs.sort as SortKey | undefined) ?? 'score')
/** 批次 A3：代码搜索（浏览态输入，不入偏好档——搜索词是会话态不是布局偏好）。 */
const query = ref('')
const planSignalId = ref('')
/** 有效期与按钮状态同一时钟（P2-10 服务端钟，未校准回退本地），避免过期后仍保留可执行入口。 */
const nowTick = ref(serverNow())
const labels = ['技术面', '消息面', '资金面', '外围']
/** 卡内焦点维：点反对维高亮（与决策台票体同一互动），每卡独立一档。 */
const focusDims = ref<Record<string, string>>({})

/** 票体转换（W2 收编）：一条信号一张 DeskTicket，lane 按状态归位；转换是纯计算，按需出票不缓存。 */
function ticketOf(item: SignalItem) {
  return toDeskTicket(item, maxSinglePct.value)
}
/** 卡键：信号无 id 时退 symbol+time（与原卡 v-for key 同口径），焦点维索引同键。 */
function keyOf(item: SignalItem) {
  return item.id || item.symbol + item.time
}

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

// 偏好整档持久化（每次写全字段，动态字段 strategy 原样入档由消费侧兜底）。
watch([sideFilter, stateFilter, strategyFilter, sortKey], () => {
  savePanelPrefs(PREFS_KEY, PREFS_ALLOW, {
    side: sideFilter.value,
    state: stateFilter.value,
    sort: sortKey.value,
    strategy: strategyFilter.value,
  })
})

// 动态字段兜底：模板选项就绪后，陈旧存档值回默认，防 select 卡在已不存在的模板上。
watch(strategies, (list) => {
  if (list.length && strategyFilter.value && !list.includes(strategyFilter.value)) strategyFilter.value = ''
})

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
  const q = query.value.trim().toUpperCase()
  const list = rows.value.filter((item) => {
    if (q && !item.symbol.toUpperCase().includes(q)) return false
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

/** B3 死件接入（信号审核漏斗）：「今日→有效→高分→已执行」全部从同一 rows 枚举计数（监控 S3 光带是全市场
 *  扫描口径，这条是信号→成交口径，两只漏斗互补不复用）；未恢复时值显「—」，不拿 0 冒充（数据纪律）。 */
const funnelStages = computed<FunnelStage[]>(() => {
  if (!signalStore.ready) {
    return [
      { label: '今日信号', value: null, hint: '信号状态未恢复，等待快照重连。' },
      { label: '有效中', value: null },
      { label: '高分（≥85）', value: null },
      { label: '已执行', value: null },
    ]
  }
  return [
    { label: '今日信号', value: rows.value.length },
    { label: '有效中', value: activeCount.value },
    { label: '高分（≥85）', value: rows.value.filter((r) => r.highValue && isPendingStatus(r.status) && !isExpired(r)).length },
    { label: '已执行', value: rows.value.filter(isHandled).length },
  ]
})
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
  const [signalRes, accountRes, riskRes, marketRes, sourceRes, configRes] = await Promise.allSettled([
    signalStore.refresh(),
    account.refresh(),
    risk.refresh(),
    market.refresh(),
    listSources(),
    getConfigValues(),
  ])
  if (disposed || key !== refreshKey() || sequence !== loadSequence || checkedBook !== account.book) return false
  nowTick.value = serverNow()
  // 单股上限是票体风控块的估算参数（辅助位）：读取失败只降级显示，不拖页面的 ready 判定。
  const parsed = configRes.status === 'fulfilled' ? Number(configRes.value['risk.max_single_pct']) : NaN
  maxSinglePct.value = Number.isFinite(parsed) && parsed > 0 ? parsed : null
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
      serverNow(),
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

/** FE-O2 漏选钻取定位：面板参数（symbol）预置代码搜索词——决策台漏选行/通知深链跳进来即过滤到目标票。 */
function applyPanelParams(params: Record<string, unknown>) {
  const symbol = typeof params.symbol === 'string' ? params.symbol.trim() : ''
  if (symbol) query.value = symbol
}
const offPanelParams = onPanelParams('signals', applyPanelParams)

function dimsOf(item: SignalItem) {
  return labels.map((label) => ({ label, value: dimValue(item.dims, label) }))
}

/** 卡内焦点维切换（同键再点取消，与决策台 pointDim 同语义）。 */
function pointDim(item: SignalItem, key: string) {
  const id = keyOf(item)
  focusDims.value[id] = focusDims.value[id] === key ? '' : key
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
  if (!document.hidden) { nowTick.value = serverNow(); void load() }
}

onMounted(() => {
  void load()
  // FE-O2：冷启动暂存的 symbol 参数在首拉前消费，预置代码搜索词（漏选行点击定位）。
  const staged = consumePanelParams('signals')
  if (staged) applyPanelParams(staged)
  // signal 属常驻主题（BASE_TOPICS），只需 onPush，不必再 subscribe。
  offPush = onPush(onPushMessage)
  tickTimer = setInterval(() => {
    if (!document.hidden) nowTick.value = serverNow()
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
  offPanelParams?.()
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
      <!-- 批次 A3：代码搜索——信号多时先按代码收口再读卡 -->
      <NtInput v-model="query" class="filters__search" type="search" icon="search" placeholder="搜代码，如 600519" mono />
      <NtSelect v-model="sideFilter" :options="sideOptions" />
      <NtSelect v-model="stateFilter" :options="stateOptions" />
      <NtSelect v-model="strategyFilter" :options="strategyOptions" />
      <NtSelect v-model="sortKey" :options="sortOptions" />
    </div>
    <ConditionalPlans :signal-id="planSignalId" />

    <!-- B3 死件接入：信号审核漏斗（今日→有效→高分→已执行，同 rows 一枚枚举计数；未恢复显「—」不拿 0 冒充）。 -->
    <section class="nt-panel">
      <h2 class="nt-panel__title">今日漏斗</h2>
      <NtFunnel :stages="funnelStages" />
    </section>

    <!-- BE-12 漏斗历史看板：近 7 日候选 vs 未入选 + 最近一日未入选原因 -->
    <section class="nt-panel">
      <h2 class="nt-panel__title">漏斗历史（近 7 日）</h2>
      <NtMissFunnelHistory :days="7" />
    </section>

    <p v-if="loading" class="nt-empty">读取中…</p>
    <NtErrorGuide :error="error" />
    <p v-if="!loading && !rows.length" class="nt-empty">{{ signalStore.ready ? '今日没有信号。' : '信号状态尚未恢复，不能判断是否有机会。' }}</p>
    <p v-else-if="!loading && !filtered.length" class="nt-empty">当前过滤条件下没有信号。</p>
    <div v-if="filtered.length" class="grid">
      <article
        v-for="item in filtered"
        :key="keyOf(item)"
        class="card"
        :class="{
          hot: item.highValue,
          stale: isHandled(item) || isExpired(item) || ['rejected', 'cancelled'].includes(item.status.toLowerCase()),
        }"
      >
        <!-- W2 收编：卡片全量走 TicketDetail 票体（头/行动/价格/投票+评语/异议/理由证据/风控唯一出处），
             卡壳只留评分环+雷达（above 槽）与状态章/时间/新鲜度（head-extra 槽）。 -->
        <TicketDetail
          :ticket="ticketOf(item)"
          :action="actionOf(item)"
          :now-tick="nowTick"
          :focus-dim="focusDims[keyOf(item)]"
          @point-dim="pointDim(item, $event)"
        >
          <template #head-extra>
            <span class="tm">{{ clock(item.time) }}</span>
            <!-- 状态章文案/tone 出自 signal 册（§7.1，一字锁定）；未终态且有效期已过按「已过期」论，
                 册外状态码原样回显（禁自封）；未终态信号章侧附剩余时效（非状态文案）。 -->
            <NtBadge :tone="stateBadge(item).entry?.tone ?? 'neutral'">{{ stateBadge(item).entry?.text ?? (item.status || '—') }}</NtBadge>
            <span v-if="stateBadge(item).remain" class="tm">{{ stateBadge(item).remain }}</span>
            <!-- §7.3 首批挂载：卡头新鲜度取信号时间戳，无时点证据显「时点未知」 -->
            <NtFreshness label="信号" :at="timeOf(item.time)" />
          </template>
          <template #above>
            <div class="body">
              <!-- hero 档（方案 §5.2-4 第 3 处）：评分环中央大数字走 --nt-text-0，≥85 由 hot 发光预算接管 -->
              <NtProgressRing :value="item.score" :hot="item.highValue" :size="56" hero />
              <template v-if="!item.degraded">
                <NtRadar :dims="dimsOf(item)" :size="84" />
              </template>
              <p v-else class="degraded-note">模型降级：四维未复核，仅有规则分 {{ item.ruleScore ?? '—' }}</p>
            </div>
          </template>
          <template #foot>
            <p class="meta">
              {{ strategyLabel(item.strategy) }} · 规则 {{ item.ruleScore ?? '—' }} · AI {{ item.aiScore ?? '—' }}
              <template v-if="item.holdDaysMax != null"> · 最长持有 {{ item.holdDaysMax }} 日</template>
              <template v-if="item.traceId"> · {{ item.traceId }}</template>
            </p>
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
            <!-- FE-S2 生命周期时间轴（BE-02 事件流；空/失败回退创建/关闭近似节点） -->
            <NtSignalTimeline
              :signal-id="item.id"
              :time="item.time"
              :closed-at="item.closedAt"
              :status="item.status"
            />
          </template>
        </TicketDetail>
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

/* 批次 A3：搜索框与下拉同高对齐（NtInput 默认 32px 高，限宽不顶满）。 */
.filters__search {
  width: 180px;
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

.tm {
  color: var(--nt-text-3);
  font-size: 11px;
  font-family: var(--nt-font-mono);
}

/* 卡壳（above 槽）：评分环+雷达双列，降级说明整宽压底（四维分数行/评语已由票体四维投票收编）。 */
.body {
  display: flex;
  gap: 12px;
  align-items: center;
}

.degraded-note {
  margin: 0;
  font-size: 12px;
  color: var(--nt-warn);
}

.meta {
  margin: 0 auto 0 0;
  color: var(--nt-text-2);
  font-size: 11.5px;
  align-self: center;
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

/* W0-3b/FE-O6：action-plan 块（h3 标目档/弱边框/密字排）已随 TicketAction 收编迁出（组件 scoped 内唯一出处）。 */
</style>
