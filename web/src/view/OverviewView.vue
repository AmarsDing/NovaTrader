<script setup lang="ts">
/**
 * 决策台：一屏看清今天能不能做、做什么、为什么没选上。
 * P3：左栏三列（待执行 / 已执行 / 漏选），右栏一张票的正文。
 * 账户 / 情绪 / 风控仍走 store；信号、候选、晨报、热词、数据源在本页取。
 * 票的拼接规则全在 decision-desk.ts（纯函数），本页不编理由、不新开模型调用。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getBriefing, type Briefing } from '@/api/brain'
import { getConfigValues } from '@/api/config'
import { listSources, type DataSource } from '@/api/datahub'
import { hotWords, timeline, type HotWord, type IntelItem } from '@/api/intel'
import { listCandidates } from '@/api/strategy'
import type { Candidate } from '@/api/types/strategy'
import NtSectorBar from '@/viz/NtSectorBar.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtButton from '@/ui/NtButton.vue'
import ConditionalPlans from './ConditionalPlans.vue'
import { useAccountStore } from '@/stores/account'
import { useMarketStore } from '@/stores/market'
import { useRiskStore } from '@/stores/risk'
import { useSignalsStore } from '@/stores/signals'
import {
  amount,
  clock,
  messageOf,
  phaseName,
  pnlClass,
  price,
  ratioPct,
  sideLabel,
  signed,
  validRemainText,
} from '@/utils/format'
import { isLive, onPush, onResync, type WsPushMessage } from '@/ws/hub'
import { createPageRefresh } from './page-refresh'
import { getWorkbench } from '@/workspace/inject'
import type { Position } from '@/api/trade'
import {
  buildGaps,
  buildMissed,
  buildTickets,
  buildDecisionAction,
  countBlockedSources,
  hasAvailableSource,
  missLabel,
  strategyLabel,
  type DeskGap,
  type DeskTicket,
  type DecisionContext,
} from './decision-desk'

const REFRESH_MS = 30_000

const account = useAccountStore()
const market = useMarketStore()
const risk = useRiskStore()
const signalStore = useSignalsStore()

const signals = computed(() => signalStore.ready ? signalStore.items : [])
const candidates = ref<Candidate[]>([])
const candidatesReady = ref(false)
const words = ref<HotWord[]>([])
const briefing = ref<Briefing | null>(null)
const briefingMissing = ref(false)
const sources = ref<DataSource[]>([])
const sourcesFailed = ref(false)
const sourcesLoaded = ref(false)
const focus = ref('')
const intelItems = ref<IntelItem[]>([])
const intelSentiment = ref<number | null>(null)
const error = ref('')
const loading = ref(false)
const pulse = ref('')
/** 风控参数（risk.max_single_pct）。读不到就传 null，票上写「以风控实检为准」 */
const maxSinglePct = ref<number | null>(null)
/** 当前展开的那张票 */
const selectedId = ref('')
/** 异议点到的维，四维行高亮 */
const focusDim = ref('')
const decisionContext = ref<DecisionContext | null>(null)
const nowTick = ref(Date.now())

let timer: ReturnType<typeof setInterval> | null = null
let actionTimer: ReturnType<typeof setInterval> | null = null
let offPush: (() => void) | undefined
let loadSequence = 0
let focusSequence = 0
let contextVersion = 0
let disposed = false
let offResync: (() => void) | undefined
const refreshKey = () => `${account.book}:${contextVersion}`
const refreshes = createPageRefresh({ key: refreshKey, read: readPage, pushIntervalMs: 2000 })

const noTape = computed(() => !market.ready || market.up == null || market.down == null || (market.up === 0 && market.down === 0))
/** 顶栏之外，决策台卡片以当日盈亏领衔；缺 dayPnl 时用浮动盈亏兜底并在 sub 里写明。 */
const dayPnlValue = computed(() => account.accountOk ? (account.dayPnl != null ? account.dayPnl : account.unrealized) : null)
const usedPct = computed(() => {
  if (!account.accountOk || !account.equity || account.cash == null) return null
  return 1 - account.cash / account.equity
})
const chartRows = computed(() =>
  (market.ready ? market.sectors : [])
    .filter((row) => row.avgPct != null)
    .map((row) => ({ name: row.name, avgPct: Number((row.avgPct as number).toFixed(2)) })),
)
const tickets = computed(() => buildTickets(signals.value, maxSinglePct.value))
const missed = computed(() => buildMissed(candidates.value))
const selectedCount = computed(() => candidates.value.filter((row) => row.stage === 'selected').length)
const selected = computed<DeskTicket | null>(
  () =>
    tickets.value.pending.find((item) => item.id === selectedId.value) ??
    tickets.value.executed.find((item) => item.id === selectedId.value) ??
    null,
)
const selectedAction = computed(() =>
  selected.value
    ? buildDecisionAction(
        selected.value,
        decisionContext.value && { ...decisionContext.value, live: isLive() },
        nowTick.value,
      )
    : null,
)
const gaps = computed<DeskGap[]>(() =>
  buildGaps({
    sourcesFailed: sourcesFailed.value,
    sourcesEmpty: sourcesLoaded.value && !sourcesFailed.value && sources.value.length === 0,
    sourcesBlocked: countBlockedSources(sources.value),
    noTape: noTape.value,
    briefingMissing: briefingMissing.value,
    briefingStale: !!briefing.value?.stale,
    anyDegraded: signals.value.some((row) => row.degraded),
    paperDays: account.accountOk ? account.paperDays : null,
    killActive: risk.ready && risk.killActive,
    live: isLive(),
  }),
)

/* 选中的票没了（被执行/被撤）就回落到待执行第一张；没有选中项时默认展开第一张待执行 */
watch(tickets, (next) => {
  if (selected.value) return
  selectedId.value = next.pending[0]?.id ?? next.executed[0]?.id ?? ''
})

function pick(id: string) {
  focusDim.value = ''
  selectedId.value = selectedId.value === id ? '' : id
}

/** 异议点到反对的那一维 */
function pointDim(key: string) {
  focusDim.value = focusDim.value === key ? '' : key
}

function weight(position: Position): string {
  if (!account.accountOk || !account.equity || !position.lastPrice || !position.quantity) return ''
  return `${(((position.lastPrice * position.quantity) / account.equity) * 100).toFixed(1)}%`
}

/** 浮动盈亏比率：(现价-成本)/成本；缺数据返回 null 显示「—」。 */
function pnlRatio(position: Position): number | null {
  if (!position.avgCost || !position.lastPrice) return null
  return (position.lastPrice - position.avgCost) / position.avgCost
}

function wordClickable(word: HotWord): boolean {
  return word.type === 'stock'
}

function openPulse() {
  if (pulse.value.startsWith('信号')) openPanel('signals')
  else openPanel('notify-center')
}

function openPanel(id: string, params?: Record<string, unknown>) {
  getWorkbench()?.openPanel(id, params)
}

function openMarket(symbol: string) {
  openPanel('market', { symbol })
}

/** 下单仍进交易面板，带上这张票的代码、方向和入场价。交易面板在 P4 才拦截 */
function openTrade(ticket: DeskTicket) {
  if (
    !buildDecisionAction(ticket, decisionContext.value && { ...decisionContext.value, live: isLive() }, Date.now())
      .canOpenTrade
  )
    return
  openPanel('trade', {
    symbol: ticket.symbol,
    side: ticket.side,
    price: ticket.entry != null ? String(ticket.entry) : '',
    signalId: ticket.id,
    strategyVersionId: ticket.strategyVersionId,
    book: ticket.book,
    validUntil: ticket.validUntil,
    traceId: ticket.traceId,
    requestedPositionPct: ticket.risk.requestedPct,
  })
}

async function loadFocus(symbol: string) {
  const sequence = ++focusSequence
  focus.value = symbol
  try {
    const data = await timeline(symbol)
    if (disposed || sequence !== focusSequence || focus.value !== symbol) return
    intelItems.value = data.items.slice(0, 6)
    intelSentiment.value = data.sentiment
  } catch (err) {
    if (disposed || sequence !== focusSequence || focus.value !== symbol) return
    intelItems.value = []
    intelSentiment.value = null
    error.value = messageOf(err)
  }
}

function load(manual = false) {
  if (manual && !disposed) loading.value = true
  return refreshes.refresh()
}

async function readPage(key: string): Promise<boolean> {
  const sequence = ++loadSequence
  loading.value = true
  error.value = ''
  const checkedBook = account.book
  const [
    accountRes,
    marketRes,
    riskRes,
    signalRes,
    candidateRes,
    wordRes,
    briefRes,
    sourceRes,
  ] = await Promise.allSettled([
    account.refresh(),
    market.refresh(),
    risk.refresh(),
    signalStore.refresh(),
    listCandidates({ limit: 40 }),
    hotWords(),
    getBriefing(''),
    listSources(),
  ])

  if (disposed || key !== refreshKey() || sequence !== loadSequence || account.book !== checkedBook) return false
  nowTick.value = Date.now()
  candidatesReady.value = candidateRes.status === 'fulfilled'
  candidates.value = candidateRes.status === 'fulfilled' ? candidateRes.value : []
  words.value = wordRes.status === 'fulfilled' ? wordRes.value.slice(0, 8) : []
  if (briefRes.status === 'fulfilled') {
    briefing.value = briefRes.value
    briefingMissing.value = !briefRes.value.summary && briefRes.value.items.length === 0
  } else {
    briefing.value = null
    briefingMissing.value = true
  }
  if (sourceRes.status === 'fulfilled') {
    sources.value = sourceRes.value
    sourcesFailed.value = false
    sourcesLoaded.value = true
  } else {
    sources.value = []
    sourcesFailed.value = true
    sourcesLoaded.value = false
  }

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

  const failed = [accountRes, marketRes, riskRes, signalRes, candidateRes].find(
    (item) => item.status === 'rejected',
  )
  if (failed && failed.status === 'rejected') error.value = messageOf(failed.reason)
  loading.value = false
  return [accountRes, marketRes, riskRes, signalRes].every((item) => item.status === 'fulfilled' && item.value === true)
}

async function loadRiskParams() {
  try {
    const values = await getConfigValues()
    if (disposed) return
    const parsed = Number(values['risk.max_single_pct'])
    maxSinglePct.value = Number.isFinite(parsed) && parsed > 0 ? parsed : null
  } catch {
    if (disposed) return
    maxSinglePct.value = null
  }
}

async function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'signal' && msg.topic !== 'alert' && msg.topic !== 'position') return
  const data = msg.data && typeof msg.data === 'object' ? (msg.data as Record<string, unknown>) : {}
  const title = String(data.title || data.subject || data.symbol || data.kind || '')
  if (title) pulse.value = `${msg.topic === 'signal' ? '信号' : '告警'} ${title}`
  const key = refreshKey()
  const ready = await refreshes.fromPush()
  if (!disposed && key === refreshKey() && !ready) throw new Error('决策台核心状态尚未恢复')
}

function onVisible() {
  if (!document.hidden) { nowTick.value = Date.now(); void load(false) }
}

onMounted(() => {
  void load(true)
  void loadRiskParams()
  timer = setInterval(() => { if (!document.hidden) void load(false) }, REFRESH_MS)
  actionTimer = setInterval(() => {
    if (!document.hidden) nowTick.value = Date.now()
  }, 1000)
  offPush = onPush(onPushMessage)
  offResync = onResync(['signal', 'position', 'alert'], async () => {
    const key = refreshKey()
    const ready = await load(false)
    if (!disposed && key === refreshKey() && !ready) throw new Error('决策台核心状态尚未恢复')
  })
  document.addEventListener('visibilitychange', onVisible)
})

onBeforeUnmount(() => {
  disposed = true
  contextVersion += 1
  focusSequence += 1
  refreshes.dispose()
  if (timer) clearInterval(timer)
  if (actionTimer) clearInterval(actionTimer)
  offPush?.()
  offResync?.()
  document.removeEventListener('visibilitychange', onVisible)
})
watch(
  () => account.book,
  () => {
    contextVersion += 1
    decisionContext.value = null
    void load(false)
  },
  { flush: 'sync' },
)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">决策台</h1>
        <p class="nt-note">左栏三列收票，右栏读一张票的正文。没读到的项保持为「—」，不把空数据当成平静。</p>
      </div>
      <NtButton :loading="loading" @click="load(true)">刷新</NtButton>
    </header>
    <p v-if="pulse" class="nt-note">
      刚到：<button type="button" class="link" @click="openPulse">{{ pulse }}</button>
    </p>
    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <h2 class="nt-panel__title">现在缺什么</h2>
      <p v-if="!gaps.length" class="nt-empty">
        链路齐：快照、晨报、模型和行情通道都有数。仍只做两个模板，不是全市场机会。
      </p>
      <ul v-else class="gaps">
        <li v-for="gap in gaps" :key="gap.id">
          <button type="button" :class="gap.level" @click="openPanel(gap.panel)">{{ gap.text }}</button>
        </li>
      </ul>
    </section>

    <section class="nt-cards">
      <NtStatCard
        label="当日盈亏"
        :value="signed(dayPnlValue)"
        :sub="!account.accountOk ? '账户读取未核对' : account.dayPnl != null ? `已实现 ${signed(account.realized)}` : '无当日盈亏口径，显示浮动盈亏'"
        :tone="pnlClass(dayPnlValue) || 'neutral'"
        hero
      />
      <NtStatCard
        label="权益"
        :value="amount(account.accountOk ? account.equity : null)"
        :sub="account.accountOk ? `可用 ${amount(account.cash)}${usedPct != null ? ` · 已用 ${(usedPct * 100).toFixed(0)}%` : ''}` : '账户读取未核对'"
      />
      <NtStatCard
        label="情绪"
        :value="noTape ? '无快照' : phaseName(market.phase)"
        :sub="
          noTape
            ? '全市场快照未入库'
            : `仓位系数 ${market.positionScale ?? '—'} · 上涨 ${market.up ?? '—'} / 下跌 ${market.down ?? '—'}`
        "
      />
      <NtStatCard
        label="系统"
        :value="isLive() ? '实时' : '不是实时'"
        :sub="`模式 ${risk.ready ? risk.mode : '—'} · 炸板率 ${ratioPct(market.ready ? market.brokenRate : null)}`"
      />
    </section>

    <!-- P3 一张票：左栏三列，右栏正文 -->
    <section class="nt-panel desk">
      <div class="desk__lanes">
        <div class="lane">
          <h3>
            待执行 <b>{{ signalStore.ready ? tickets.pending.length : '—' }}</b>
          </h3>
          <p v-if="!signalStore.ready" class="nt-empty">信号未返回，待执行数量未知。</p>
          <p v-else-if="!tickets.pending.length" class="nt-empty">没有待执行信号。</p>
          <ul v-else>
            <li
              v-for="ticket in tickets.pending"
              :key="ticket.id"
              :class="{ on: selectedId === ticket.id }"
              @click="pick(ticket.id)"
            >
              <span class="tm">{{ clock(ticket.time) }}</span>
              <span class="sym mono">{{ ticket.symbol }}</span>
              <span class="name">{{ ticket.name || strategyLabel(ticket.strategy) }}</span>
              <em v-if="ticket.degraded" class="tag">规则分</em>
              <b :class="{ hot: ticket.highValue }">{{
                ticket.degraded ? (ticket.ruleScore ?? '—') : (ticket.score ?? '—')
              }}</b>
            </li>
          </ul>
        </div>
        <div class="lane">
          <h3>
            已执行 <b>{{ signalStore.ready ? tickets.executed.length : '—' }}</b>
          </h3>
          <p v-if="!signalStore.ready" class="nt-empty">信号未返回，已执行数量未知。</p>
          <p v-else-if="!tickets.executed.length" class="nt-empty">今天还没有执行的票。</p>
          <ul v-else>
            <li
              v-for="ticket in tickets.executed"
              :key="ticket.id"
              :class="{ on: selectedId === ticket.id }"
              @click="pick(ticket.id)"
            >
              <span class="tm">{{ clock(ticket.time) }}</span>
              <span class="sym mono">{{ ticket.symbol }}</span>
              <span class="name">{{ ticket.name || strategyLabel(ticket.strategy) }}</span>
              <span class="done">{{ ticket.closedNote || '已执行' }}</span>
            </li>
          </ul>
        </div>
        <div class="lane">
          <h3>
            漏选 <b>{{ candidatesReady ? missed.length : '—' }}</b>
          </h3>
          <p class="nt-note">候选 {{ candidatesReady ? candidates.length : '—' }} · 入选 {{ candidatesReady ? selectedCount : '—' }}。</p>
          <p v-if="!candidatesReady" class="nt-empty">候选未返回，漏选数量未知。</p>
          <p v-else-if="!missed.length" class="nt-empty">没有漏选记录。</p>
          <ul v-else>
            <li v-for="row in missed" :key="row.symbol + row.strategy">
              <button type="button" class="sym mono link" @click="openMarket(row.symbol)">{{ row.symbol }}</button>
              <span class="name">{{ row.name || strategyLabel(row.strategy) }}</span>
              <span class="miss">{{ missLabel(row.reason) }}</span>
              <b>规则 {{ row.ruleScore ?? '—' }}</b>
            </li>
          </ul>
        </div>
      </div>

      <div class="desk__body">
        <template v-if="selected">
          <header class="ticket__head">
            <button type="button" class="sym mono link big" @click="openMarket(selected.symbol)">
              {{ selected.symbol }}
            </button>
            <strong>{{ selected.name || strategyLabel(selected.strategy) }}</strong>
            <em :class="selected.side === 'buy' ? 'up' : selected.side === 'sell' ? 'down' : ''">{{
              sideLabel(selected.side)
            }}</em>
            <span class="date">形成日 {{ selected.tradeDate || '—' }}</span>
            <span class="date"
              >账本 {{ selected.book || '未返回' }} · 版本 {{ selected.strategyVersionId || '未返回' }}</span
            >
            <span v-if="selected.degraded" class="tag">规则分 {{ selected.ruleScore ?? '—' }}</span>
            <b v-else class="score" :class="{ hot: selected.highValue }">{{ selected.score ?? '—' }}</b>
            <span v-if="selected.lane === 'executed'" class="done">{{ selected.closedNote || '已执行' }}</span>
          </header>

          <p v-if="selected.lane === 'executed'" class="nt-note">账本已核成交 {{ selected.executedVolume ?? '—' }} 股 · 成交均价 {{ price(selected.executedPrice ?? null) }}。部分成交可能仍有余量，请核对原订单。</p>
          <section v-if="selectedAction" class="action-plan" :class="selectedAction.state" aria-label="当前行动计划">
            <h4>当前动作：{{ selectedAction.title }}</h4>
            <p><strong>什么时候行动</strong>{{ selectedAction.timing }}</p>
            <p><strong>失效条件</strong>{{ selectedAction.invalidation }}</p>
            <p v-if="selectedAction.blocker" class="action-plan__blocker">
              <strong>主要阻断</strong>{{ selectedAction.blocker }}
            </p>
            <p class="action-plan__caution"><strong>先看反证</strong>{{ selectedAction.caution }}</p>
            <p v-if="selectedAction.canOpenTrade" class="nt-note">
              当前仅可准备模拟盘确认，尚未校验最新个股价格、数量与下单风控。
            </p>
          </section>

          <ConditionalPlans :signal-id="selected.id" />
          <div class="ticket__grid">
            <div class="ticket__block">
              <h4>价格</h4>
              <p class="px">
                入场 {{ price(selected.entryLow) }}–{{ price(selected.entryHigh) }} · 止损 {{ price(selected.stop) }} ·
                止盈 {{ price(selected.take) }}
              </p>
              <p class="valid" :class="{ warn: !selected.validUntil }">
                {{ selected.validUntil ? validRemainText(selected.validUntil) || '已过期' : '无有效期' }}
                <template v-if="selected.risk.requestedPct != null">
                  · 请求仓位 {{ (selected.risk.requestedPct * 100).toFixed(0) }}%</template
                >
              </p>
            </div>

            <div class="ticket__block">
              <h4>四维投票</h4>
              <ul class="votes">
                <li
                  v-for="vote in selected.votes"
                  :key="vote.key"
                  :class="{ against: vote.against, focus: focusDim === vote.key }"
                  @click="vote.against && pointDim(vote.key)"
                >
                  <span>{{ vote.label }}</span>
                  <b>{{ vote.score ?? '—' }}</b>
                </li>
              </ul>
            </div>
          </div>

          <div v-if="selected.objections.length" class="ticket__block objections">
            <h4>异议</h4>
            <ul>
              <li v-for="(item, index) in selected.objections" :key="index">{{ item }}</li>
            </ul>
          </div>

          <div class="ticket__block">
            <h4>理由</h4>
            <p v-if="!selected.reasons.length" class="nt-empty">
              {{ selected.degraded ? '规则分：模型降级，没有研判正文' : '未调用' }}
            </p>
            <ol v-else class="reasons">
              <li v-for="(item, index) in selected.reasons" :key="index">{{ item }}</li>
            </ol>
            <p v-if="selected.evidence.length" class="evidence">
              <span v-for="(item, index) in selected.evidence" :key="index" class="chip">{{ item }}</span>
            </p>
          </div>

          <div class="ticket__block">
            <h4>风控</h4>
            <p class="px">
              请求仓位
              {{ selected.risk.requestedPct != null ? `${(selected.risk.requestedPct * 100).toFixed(1)}%` : '—' }} ·
              单股上限估算
              {{ selected.risk.allowedPct != null ? `${(selected.risk.allowedPct * 100).toFixed(1)}%` : '—' }}
              <template v-if="selected.risk.rule"> · {{ selected.risk.rule }}</template>
            </p>
            <p class="nt-note">{{ selected.risk.note }}</p>
          </div>

          <footer class="ticket__actions">
            <NtButton size="sm" variant="quiet" @click="openMarket(selected.symbol)">看行情</NtButton>
            <NtButton
              v-if="selected.lane === 'pending'"
              size="sm"
              :disabled="!selectedAction?.canOpenTrade"
              @click="openTrade(selected)"
              >准备模拟盘确认</NtButton
            >
          </footer>
        </template>
        <p v-else-if="!signalStore.ready" class="nt-empty">信号未返回，暂时无法展开决策票。</p>
        <p v-else class="nt-empty">点左边一票读正文。没有研判正文时这里写「未调用」或「规则分」，不编理由。</p>
      </div>
    </section>

    <div class="nt-split desk-split">
      <section class="nt-panel">
        <h2 class="nt-panel__title">晨报与热词</h2>
        <p v-if="briefing?.summary" class="brief">{{ briefing.summary }}</p>
        <p v-else class="nt-empty">晨报未返回。</p>
        <ul v-if="briefing?.items.length" class="brief-items">
          <li v-for="(item, index) in briefing.items.slice(0, 4)" :key="index">
            <button v-if="item.symbol" type="button" class="sym" @click="loadFocus(item.symbol)">
              {{ item.symbol }}
            </button>
            <span>{{ item.title || item.body }}</span>
          </li>
        </ul>
        <p v-if="!words.length" class="nt-empty">热词未返回。</p>
        <ul v-else class="words">
          <li v-for="word in words" :key="word.type + word.target">
            <button v-if="wordClickable(word)" type="button" class="sym" @click="loadFocus(word.target)">
              {{ word.target }}
            </button>
            <span v-else>{{ word.target }}</span>
            <b>{{ word.score ?? word.count ?? '—' }}</b>
          </li>
        </ul>
        <p class="nt-note more">
          <button type="button" class="link" @click="openPanel('morning-briefing')">打开晨报</button>
          <button type="button" class="link" @click="openPanel('intel')">打开情报</button>
          <button type="button" class="link" @click="openPanel('signals')">打开信号中枢</button>
          <button type="button" class="link" @click="openPanel('pool-manager')">打开股池</button>
        </p>
      </section>

      <section class="nt-panel">
        <h2 class="nt-panel__title">持仓</h2>
        <p v-if="account.accountOk && account.openHalted" class="nt-err">
          开仓已停{{ account.haltReason ? `：${account.haltReason}` : '' }}
        </p>
        <!-- 持仓接口失败和真的空仓分开写：没读到时账面不显示成空仓 -->
        <p v-if="!account.positionsOk" class="nt-err">持仓接口没有应答，账面不当作空仓。</p>
        <p v-else-if="!account.positions.length" class="nt-empty">暂无持仓。</p>
        <div v-else class="pos-table">
          <div class="pos-row pos-row--head">
            <span>代码</span>
            <span>数量</span>
            <span>权重</span>
            <span>浮盈</span>
          </div>
          <div v-for="item in account.positions" :key="item.symbol" class="pos-row">
            <button type="button" class="sym" @click="openMarket(item.symbol)">{{ item.symbol }}</button>
            <b>{{ item.quantity ?? '—' }}</b>
            <em>{{ weight(item) || '—' }}</em>
            <i :class="pnlClass(pnlRatio(item))">{{ pnlRatio(item) != null ? ratioPct(pnlRatio(item), 1) : '—' }}</i>
          </div>
        </div>
        <h3 class="sub">板块涨跌（全市场）</h3>
        <p v-if="!chartRows.length" class="nt-empty">板块快照未返回。</p>
        <NtSectorBar v-else :rows="chartRows" height="220px" />
      </section>
    </div>

    <section v-if="focus" class="nt-panel">
      <h2 class="nt-panel__title">{{ focus }} 的情报</h2>
      <p v-if="intelSentiment != null" class="nt-note">近端综合情感 {{ intelSentiment }}</p>
      <p v-if="!intelItems.length" class="nt-empty">这条时间线是空的。新闻源没入库时，这里不会凭空出现消息。</p>
      <ul v-else class="rows">
        <li v-for="item in intelItems" :key="item.id || item.title">
          <b :class="pnlClass(item.sentiment)">{{ clock(item.time) }}</b>
          <span>{{ item.title }}</span>
          <em>{{ item.source }}</em>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.desk-split {
  align-items: start;
}

.gaps,
.rows,
.words,
.brief-items {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.gaps button {
  width: 100%;
  text-align: left;
  padding: 6px 8px;
  border-radius: var(--nt-radius-s);
  border: 1px solid var(--nt-border-1);
  background: transparent;
  color: var(--nt-text-1);
  font: inherit;
  font-size: 12.5px;
  cursor: pointer;
}

.gaps button.block {
  border-color: var(--nt-danger);
  color: var(--nt-danger);
}

.gaps button.warn {
  border-color: var(--nt-warn, var(--nt-border-2, var(--nt-border-1)));
}

.rows li,
.brief-items li,
.words li {
  display: flex;
  gap: 8px;
  align-items: baseline;
  font-size: 12.5px;
}

.rows li span,
.brief-items li span {
  min-width: 0;
}

.name,
.px {
  color: var(--nt-text-2);
}

.px {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}

.tm {
  color: var(--nt-text-3);
  font-size: 11px;
  font-family: var(--nt-font-mono);
  min-width: 42px;
}

.valid,
.pos {
  font-size: 11px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

.valid.warn {
  color: var(--nt-warn);
}

.sym,
.link {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-accent);
  cursor: pointer;
  font: inherit;
}

.sym {
  font-family: var(--nt-font-mono);
}

.rows b,
.words b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.rows b.hot {
  color: var(--nt-warn, var(--nt-accent));
}

.words b {
  margin-left: auto;
}

.pos-table {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
}

.pos-row {
  display: grid;
  grid-template-columns: minmax(64px, 1fr) 64px 56px 64px;
  gap: 8px;
  align-items: baseline;
}

.pos-row--head {
  color: var(--nt-text-3);
  font-size: 11px;
}

.pos-row b,
.pos-row em,
.pos-row i {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  font-style: normal;
  text-align: right;
}

.pos-row i.up {
  color: var(--nt-up);
}

.pos-row i.down {
  color: var(--nt-down);
}

.brief {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--nt-text-1);
}

.more {
  margin-top: 8px;
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}

.sub {
  margin: 14px 0 0;
  font-size: 13px;
  font-weight: 600;
}

/* ---- 一张票：左栏三列 + 右栏正文 ---- */

.desk {
  display: grid;
  grid-template-columns: minmax(0, 340px) minmax(0, 1fr);
  gap: 14px;
  align-items: start;
}

.desk__lanes {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}

.lane h3 {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-text-2);
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}

.lane h3 b {
  font-family: var(--nt-font-mono);
  color: var(--nt-text-3);
  font-weight: 500;
}

.lane ul {
  list-style: none;
  margin: 6px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.lane li {
  display: flex;
  gap: 6px;
  align-items: baseline;
  font-size: 12px;
  padding: 5px 6px;
  border-radius: var(--nt-radius-s);
  border: 1px solid transparent;
  cursor: pointer;
}

.lane li.on {
  border-color: var(--nt-accent);
  background: color-mix(in srgb, var(--nt-accent) 8%, transparent);
}

.lane li .name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}

.lane li b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.lane li b.hot {
  color: var(--nt-warn, var(--nt-accent));
}

.mono {
  font-family: var(--nt-font-mono);
}

.tag {
  font-style: normal;
  font-size: 10px;
  color: var(--nt-warn);
  border: 1px solid var(--nt-warn);
  border-radius: 3px;
  padding: 0 3px;
  white-space: nowrap;
}

.done,
.miss {
  font-size: 11px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

.desk__body {
  min-width: 0;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ticket__head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
}

.ticket__head .big {
  font-size: 15px;
}

.ticket__head strong {
  font-size: 14px;
}

.ticket__head .up {
  color: var(--nt-up);
  font-style: normal;
}

.ticket__head .down {
  color: var(--nt-down);
  font-style: normal;
}

.ticket__head .date {
  color: var(--nt-text-3);
  font-size: 11px;
}

.ticket__head .score {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-size: 18px;
  font-weight: 600;
}

.ticket__head .score.hot {
  color: var(--nt-warn, var(--nt-accent));
}

.ticket__grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
}

.ticket__block h4 {
  margin: 0 0 6px;
  font-size: 11px;
  font-weight: 600;
  color: var(--nt-text-3);
  letter-spacing: 0.04em;
}

.ticket__block p {
  margin: 0;
}

.votes {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.votes li {
  display: flex;
  justify-content: space-between;
  font-size: 12.5px;
  padding: 3px 6px;
  border-radius: var(--nt-radius-s);
  border: 1px solid transparent;
}

.votes li.against {
  color: var(--nt-warn);
  cursor: pointer;
}

.votes li.focus {
  border-color: var(--nt-warn);
}

.votes li b {
  font-family: var(--nt-font-mono);
  font-weight: 500;
}

.objections ul {
  margin: 0;
  padding-left: 18px;
  color: var(--nt-warn);
  font-size: 12.5px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.reasons {
  margin: 0;
  padding-left: 18px;
  font-size: 12.5px;
  color: var(--nt-text-1);
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.5;
}

.evidence {
  margin-top: 6px;
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.chip {
  font-size: 10.5px;
  font-family: var(--nt-font-mono);
  color: var(--nt-text-2);
  border: 1px solid var(--nt-border-1);
  border-radius: 3px;
  padding: 1px 5px;
}

.ticket__actions {
  display: flex;
  gap: 10px;
  justify-content: flex-end;
}

.action-plan {
  padding: 10px 12px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-1);
}

.action-plan h4 {
  margin: 0 0 8px;
  color: var(--nt-text-1);
  font-size: 13px;
}

.action-plan p {
  margin: 5px 0;
  color: var(--nt-text-2);
  font-size: 12px;
  line-height: 1.6;
}

.action-plan strong {
  margin-right: 8px;
  font-weight: 500;
  color: var(--nt-text-3);
}

.action-plan .action-plan__blocker,
.action-plan .action-plan__caution {
  color: var(--nt-warn);
}

@media (max-width: 900px) {
  .desk {
    grid-template-columns: 1fr;
  }
}
</style>
