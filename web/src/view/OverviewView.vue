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
import { getAuction, type AuctionSummary } from '@/api/market'
import { getRiskReport, type RiskReport } from '@/api/risk'
import { listCandidates } from '@/api/strategy'
import { sectorMembership, type SectorMembershipItem } from '@/api/membership'
import type { Candidate } from '@/api/types/strategy'
import NtSectorDist from '@/viz/NtSectorDist.vue'
import type { SectorDistRow } from '@/viz/NtSectorDist.vue'
import IntelRow from '@/display/IntelRow.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import SymbolName from '@/display/SymbolName.vue'
import TicketDetail from '@/display/TicketDetail.vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import ConditionalPlans from './ConditionalPlans.vue'
import { useAccountStore } from '@/stores/account'
import { useMarketStore } from '@/stores/market'
import { useRiskStore } from '@/stores/risk'
import { useSignalsStore } from '@/stores/signals'
import {
  amount,
  clock,
  compactAmount,
  messageOf,
  phaseName,
  pnlClass,
  ppText,
  price,
  ratioPct,
  sideLabel,
  signed,
} from '@/utils/format'
import { serverNow } from '@/utils/server-clock'
import { isLive, onPush, onResync, type WsPushMessage } from '@/ws/hub'
import { createPageRefresh } from './page-refresh'
import { getWorkbench } from '@/workspace/inject'
import type { Position } from '@/api/trade'
import {
  buildAuctionStrip,
  buildGaps,
  buildMissed,
  buildTickets,
  buildDecisionAction,
  buildTradeState,
  countBlockedSources,
  groupMissed,
  hasAvailableSource,
  laneBadge,
  laneRemainText,
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
/** FE-O5 竞价截面（GetAuction 09:25 落库）；辅助条独立成败——接口未应答 auctionReady=false，
 *  found=false 显「竞价截面未落库」，不拖核心 ready（没取到≠0）。 */
const auction = ref<AuctionSummary | null>(null)
const auctionReady = ref(false)
/** FE-O1 今日风控卡的「拦截 n」读风控日报；独立成败——未应答 sub 写「未返回」，不拖核心 ready。 */
const riskReport = ref<RiskReport | null>(null)
const riskReportReady = ref(false)
/** FE-O4 板块分布（BE-05）：持仓+候选 symbols 反查板块归属；辅助图独立成败，
 *  接口未应答 membershipReady=false 显占位，不拖核心 ready；membershipNote 写明覆盖口径。 */
const membershipItems = ref<SectorMembershipItem[]>([])
const membershipReady = ref(false)
const membershipNote = ref('')
/** 当前展开的那张票 */
const selectedId = ref('')
/** 异议点到的维，四维行高亮 */
const focusDim = ref('')
const decisionContext = ref<DecisionContext | null>(null)
/** 有效期/动作判定同一时钟（P2-10 服务端钟，未校准回退本地）。 */
const nowTick = ref(serverNow())

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
/** FE-O1 今日买入上限读参数册 risk.max_daily_buys；键缺失显「—」不猜数（与 risk-chain 面板同口径）。 */
const buyLimit = computed(() => {
  const v = risk.params['max_daily_buys']
  return v == null ? null : String(v)
})
/** 买入满额时卡面转 warn——「今天还能不能买」是这张卡要回答的问题。 */
const buyCapHit = computed(() => {
  const max = Number(buyLimit.value)
  return risk.buysToday != null && Number.isFinite(max) && max > 0 && risk.buysToday >= max
})
const distTop = 12
/** FE-O4 板块分布：持仓+候选各券按归属板块计数（一券多板块各记一次），
 *  无有效归属（接口显式回空 sectors，非错误）归入「未归属」桶；
 *  计数降序、同名按板块名兜底，超出 distTop 合成「其他」桶保可读性。 */
const distRows = computed<SectorDistRow[]>(() => {
  if (!membershipItems.value.length) return []
  const counts = new Map<string, number>()
  let unassigned = 0
  for (const item of membershipItems.value) {
    if (!item.sectors.length) {
      unassigned += 1
      continue
    }
    for (const sec of item.sectors) counts.set(sec.name, (counts.get(sec.name) ?? 0) + 1)
  }
  const rows = [...counts.entries()]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name, 'zh-Hans-CN'))
  const top = rows.slice(0, distTop)
  const rest = rows.slice(distTop)
  if (rest.length) top.push({ name: `其他（${rest.length} 个板块）`, count: rest.reduce((sum, row) => sum + row.count, 0) })
  if (unassigned) top.push({ name: '未归属', count: unassigned })
  return top
})
const tickets = computed(() => buildTickets(signals.value, maxSinglePct.value))
const missed = computed(() => buildMissed(candidates.value))
/** FE-O3 漏选分组计数头：完整列表按 missReason 聚合，组按条数降序。 */
const missedGroups = computed(() => groupMissed(missed.value))
const selectedCount = computed(() => candidates.value.filter((row) => row.stage === 'selected').length)

/* FE-O2 票卡标准化：徽标（signal 册）与有效期倒计时随票随钟出，模板不重复算。 */
const pendingCards = computed(() =>
  tickets.value.pending.map((ticket) => ({
    ticket,
    badge: laneBadge(ticket, nowTick.value),
    remain: laneRemainText(ticket.validUntil, nowTick.value),
  })),
)
const executedCards = computed(() =>
  tickets.value.executed.map((ticket) => ({ ticket, badge: laneBadge(ticket, nowTick.value) })),
)
/* FE-O5 竞价强弱条：时段判定走 serverNow（buildAuctionStrip 内部 sessionOf），过时段整条 null 不渲。 */
const auctionStrip = computed(() => buildAuctionStrip(auction.value, nowTick.value))

/** 方向文字色：买入红/卖出绿（A 股红涨绿跌，与票头方向同口径）。 */
function sideClass(side: string): string {
  return side === 'buy' ? 'up' : side === 'sell' ? 'down' : ''
}
/** 方向色带：票卡左缘 inset 2px（红买绿卖——09:45 的买单和 14:45 的卖单不再长一个样）。 */
function bandClass(side: string): string {
  return side === 'buy' ? 'side-buy' : side === 'sell' ? 'side-sell' : ''
}
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
/** FE-O1 四卡之「可交易状态」：gaps 的抬头读数（阻断 n / 链路齐），明细仍由下方「现在缺什么」清单承接。 */
const tradeState = computed(() => buildTradeState(gaps.value))

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

/** R3 浮盈额：(现价-成本)×数量；任一字段缺/0 隐显「—」（没取到≠0）。 */
function pnlAmount(position: { quantity: number | null; avgCost: number | null; lastPrice: number | null }): number | null {
  if (!position.quantity || !position.avgCost || !position.lastPrice) return null
  return (position.lastPrice - position.avgCost) * position.quantity
}

/** B3 攻心版：持仓区两条告警统一走 NtErrorGuide——「开仓已停」是风控态、「接口没应答」是取数失败，都不冒充空仓（数据纪律）。 */
const haltError = computed(() =>
  account.accountOk && account.openHalted ? `开仓已停${account.haltReason ? `：${account.haltReason}` : ''}` : '',
)
const positionsError = computed(() => (!account.positionsOk ? '持仓接口没有应答，账面不当作空仓' : ''))

/** R3 今日可卖：后端 available 即 T+1 可卖口径（接口无独立 sellableToday 字段，注明）；0 显「T+1 不可卖」。 */
function sellable(position: { available: number | null }): { text: string; hold: boolean } {
  if (position.available == null) return { text: '—', hold: false }
  if (position.available === 0) return { text: 'T+1 不可卖', hold: true }
  return { text: String(position.available), hold: false }
}

/** 价格显位：0/缺都显「—」，不把没取到画成 0。 */
function priceText(value: number | null): string {
  return value != null && value > 0 ? price(value) : '—'
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
    !buildDecisionAction(ticket, decisionContext.value && { ...decisionContext.value, live: isLive() }, serverNow())
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
    auctionRes,
    riskReportRes,
  ] = await Promise.allSettled([
    account.refresh(),
    market.refresh(),
    risk.refresh(),
    signalStore.refresh(),
    // FE-O3 完整列表：上限对齐后端默认 200（clamp 上限 1000），不再靠 8 条截断收口。
    listCandidates({ limit: 200 }),
    hotWords(),
    getBriefing(''),
    listSources(),
    // FE-O5：竞价截面是辅助条，独立成败不拖核心 ready。
    getAuction(10),
    // FE-O1：风控日报给「今日风控」卡的拦截数，独立成败不拖核心 ready。
    getRiskReport(),
  ])

  if (disposed || key !== refreshKey() || sequence !== loadSequence || account.book !== checkedBook) return false
  nowTick.value = serverNow()
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
  // FE-O5 竞价截面独立成败：失败只让竞价条显「竞价接口未应答」（时段内），不拖主表错误语义。
  auctionReady.value = auctionRes.status === 'fulfilled'
  auction.value = auctionRes.status === 'fulfilled' ? auctionRes.value : null
  // FE-O1 风控日报独立成败：失败只让「今日风控」卡的拦截数显「未返回」，不进主表错误语义。
  riskReportReady.value = riskReportRes.status === 'fulfilled'
  riskReport.value = riskReportRes.status === 'fulfilled' ? riskReportRes.value : null

  // FE-O4 板块分布独立成败：依赖本轮持仓+候选结果，单独拉取不拖核心 ready。
  const posSymbols = account.positionsOk ? account.positions.map((row) => row.symbol) : []
  const candSymbols = candidateRes.status === 'fulfilled' ? candidateRes.value.map((row) => row.symbol) : []
  // 持仓在前（权重更高）；去重保序；上限对齐后端 maxMembershipSymbols=200，截断部分在注释行写明覆盖。
  const distSymbols = [...new Set([...posSymbols, ...candSymbols].filter(Boolean))]
  if (!distSymbols.length) {
    membershipItems.value = []
    membershipReady.value = true
    membershipNote.value = '持仓与候选均为空'
  } else {
    try {
      const res = await sectorMembership(distSymbols.slice(0, 200))
      if (disposed || sequence !== loadSequence) return false
      membershipItems.value = res.items
      membershipReady.value = true
      const cover = res.items.length < distSymbols.length ? `，归属覆盖 ${res.items.length}/${distSymbols.length} 券` : ''
      const date = res.tradeDate ? ` · 归属窗口 ${res.tradeDate}` : ''
      membershipNote.value = `持仓 ${posSymbols.length} + 候选 ${candSymbols.length} 券${cover}${date}`
    } catch {
      if (disposed || sequence !== loadSequence) return false
      membershipItems.value = []
      membershipReady.value = false
      membershipNote.value = ''
    }
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
  if (!document.hidden) { nowTick.value = serverNow(); void load(false) }
}

onMounted(() => {
  void load(true)
  void loadRiskParams()
  timer = setInterval(() => { if (!document.hidden) void load(false) }, REFRESH_MS)
  actionTimer = setInterval(() => {
    if (!document.hidden) nowTick.value = serverNow()
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
    <NtErrorGuide :error="error" />

    <section class="nt-cards">
      <!-- FE-O1 四卡重排（§2.1 目标形态）：当日盈亏 hero＋可交易状态＋情绪＋今日风控；
           权益卡撤除——权益/可用与顶栏 CommandBar 同口径重复，一屏不留两个大数（B1 双英雄例外判例收口）。 -->
      <NtStatCard
        label="当日盈亏"
        :value="signed(dayPnlValue)"
        :sub="!account.accountOk ? '账户读取未核对' : account.dayPnl != null ? `已实现 ${signed(account.realized)}` : '无当日盈亏口径，显示浮动盈亏'"
        :tone="pnlClass(dayPnlValue) || 'neutral'"
        hero
      />
      <!-- 可交易状态=gaps 的抬头读数：值只给阻断数（链路齐是常态不着色），明细仍由下方「现在缺什么」清单承接。 -->
      <NtStatCard label="可交易状态" :value="tradeState.value" :sub="tradeState.sub" :tone="tradeState.tone" />
      <!-- 情绪卡收编炸板率（原系统卡口径归情绪域），涨/跌家数进子句。 -->
      <NtStatCard
        label="情绪"
        :value="noTape ? '无快照' : phaseName(market.phase)"
        :sub="
          noTape
            ? '全市场快照未入库'
            : `仓位系数 ${market.positionScale ?? '—'} · 涨 ${market.up ?? '—'} / 跌 ${market.down ?? '—'} · 炸板率 ${ratioPct(market.ready ? market.brokenRate : null)}`
        "
      />
      <!-- 今日风控：买入 x/上限（键缺失显「—」不猜数，与 risk-chain 同口径）＋日报拦截数；
           日报独立成败——未应答 sub 写「未返回」，不拖核心 ready；买入满额转 warn。 -->
      <NtStatCard
        label="今日风控"
        :value="`买入 ${risk.buysToday ?? '—'}/${buyLimit ?? '—'}`"
        :sub="riskReportReady ? `拦截 ${riskReport?.rejected ?? '—'}` : '风控日报未返回'"
        :tone="buyCapHit ? 'warn' : 'neutral'"
      />
    </section>
    <!-- FE-O1 链路徽注行：原「系统」卡拆为弱档徽注——实时性与模式是链路属性，不占一张卡的读数位。 -->
    <p class="link-strip">
      <NtBadge :tone="isLive() ? 'success' : 'warn'" dot>{{ isLive() ? '行情实时' : '行情非实时' }}</NtBadge>
      <NtBadge tone="neutral">模式 {{ risk.ready ? risk.mode : '—' }}</NtBadge>
    </p>

    <!-- FE-O5 竞价强弱条：仅集合竞价时段（09:15–09:30）显，过时段自动隐藏；
         截面未落库/接口未应答显缺口文案（没取到≠0，均涨幅/未匹配缺样本显「—」）。 -->
    <p v-if="auctionStrip" class="auction-strip">
      <span class="auction-strip__label">竞价</span>
      <template v-if="auctionStrip.found">
        <span>涨停 <b>{{ auctionStrip.limitUpCount }}</b> 家</span>
        <span>竞价额 <b>{{ compactAmount(auctionStrip.totalAmount) }}</b></span>
        <span>样本 <b>{{ auctionStrip.sampleCount }}</b> 只</span>
        <span>均涨幅 <b :class="pnlClass(auctionStrip.avgPct)">{{ auctionStrip.avgPct == null ? '—' : ppText(auctionStrip.avgPct, 2) }}</b></span>
        <span>未匹配净买 <b>{{ auctionStrip.unmatchedShares == null ? '—' : `${(auctionStrip.unmatchedShares / 1e4).toFixed(2)} 万股` }}</b></span>
        <span v-if="auctionStrip.asOf" class="auction-strip__meta">截面 {{ auctionStrip.asOf }}</span>
      </template>
      <span v-else class="auction-strip__meta">{{ auctionReady ? '竞价截面未落库（09:25 截面未入库）' : '竞价接口未应答' }}</span>
    </p>

    <!-- B3 攻心版换位（直觉4 任务即视图）：答案卡先扫，阻断清单紧随——缺什么总在票区前读，不会到下单才发现链路断。 -->
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
            <!-- FE-O2 票卡标准化：方向色带（红买绿卖）+ 时间/代码/状态徽标一行，方向/分数/入场区间/有效期倒计时一行 -->
            <li
              v-for="card in pendingCards"
              :key="card.ticket.id"
              :class="[{ on: selectedId === card.ticket.id }, bandClass(card.ticket.side)]"
              @click="pick(card.ticket.id)"
            >
              <span class="row">
                <span class="tm">{{ clock(card.ticket.time) }}</span>
                <!-- §P0 走查：代码+名称自制双件统一 SymbolName(inline)，名称走字典，缺名显「名称未返回」 -->
                <SymbolName :code="card.ticket.symbol" mode="inline" />
                <NtBadge v-if="card.badge" :tone="card.badge.tone">{{ card.badge.text }}</NtBadge>
              </span>
              <span class="meta">
                <em :class="sideClass(card.ticket.side)">{{ sideLabel(card.ticket.side) }}</em>
                <b :class="{ hot: card.ticket.highValue }">{{
                  card.ticket.degraded ? `规则 ${card.ticket.ruleScore ?? '—'}` : (card.ticket.score ?? '—')
                }}</b>
                <span>入场 {{ price(card.ticket.entryLow) }}–{{ price(card.ticket.entryHigh) }}</span>
                <span :class="{ warn: !card.ticket.validUntil }">{{ card.remain }}</span>
              </span>
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
            <!-- FE-O2 票卡标准化：已执行票与待执行同款方向色带，副行给成交均价/数量（目标形态：成交价/量/后验 pnl） -->
            <li
              v-for="card in executedCards"
              :key="card.ticket.id"
              :class="[{ on: selectedId === card.ticket.id }, bandClass(card.ticket.side)]"
              @click="pick(card.ticket.id)"
            >
              <span class="row">
                <span class="tm">{{ clock(card.ticket.time) }}</span>
                <!-- §P0 走查：代码+名称自制双件统一 SymbolName(inline)，名称走字典，缺名显「名称未返回」 -->
                <SymbolName :code="card.ticket.symbol" mode="inline" />
                <span class="row-note">{{ card.ticket.closedNote || '已执行' }}</span>
              </span>
              <span class="meta">
                <em :class="sideClass(card.ticket.side)">{{ sideLabel(card.ticket.side) }}</em>
                <span>成交均价 {{ price(card.ticket.executedPrice ?? null) }}</span>
                <span>{{ card.ticket.executedVolume ?? '—' }} 股</span>
              </span>
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
          <!-- FE-O3 完整列表：不再截断 8 条；按 missReason 分组出计数头（组按条数降序），
               组内保持规则分倒序；长列表在 miss-groups 滚动区收口，不撑高整列。 -->
          <div v-else class="miss-groups">
            <section v-for="group in missedGroups" :key="group.reason" class="miss-group">
              <h4>{{ group.label }} <b>{{ group.count }}</b></h4>
              <ul>
                <!-- FE-O2 漏选卡可钻取：点击跳信号中枢并按代码定位（验收：漏选每行 2 次点击内到达解释） -->
                <li v-for="row in group.rows" :key="row.symbol + row.strategy">
                  <button type="button" class="miss-row" title="到信号中枢定位这只票" @click="openPanel('signals', { symbol: row.symbol })">
                    <SymbolName :code="row.symbol" mode="inline" />
                    <b>规则 {{ row.ruleScore ?? '—' }}</b>
                  </button>
                </li>
              </ul>
            </section>
          </div>
        </div>
      </div>

      <div class="desk__body">
        <template v-if="selected">
          <!-- W0-3b/FE-O6：票体=TicketDetail 单一事实组件（头/行动/价格/投票/异议/理由/风控唯一出处），
               上面槽放条件计划、票脚槽放页侧按钮；焦点维焦点态仍由本页持有。 -->
          <TicketDetail
            :ticket="selected"
            :action="selectedAction"
            :now-tick="nowTick"
            :focus-dim="focusDim"
            @point-dim="pointDim"
          >
            <template #above>
              <ConditionalPlans :signal-id="selected.id" />
            </template>
            <template #foot>
              <NtButton size="sm" variant="quiet" @click="openMarket(selected.symbol)">看行情</NtButton>
              <NtButton
                v-if="selected.lane === 'pending'"
                size="sm"
                :disabled="!selectedAction?.canOpenTrade"
                @click="openTrade(selected)"
                >准备模拟盘确认</NtButton
              >
            </template>
          </TicketDetail>
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
            <!-- §6.6 证券提及统一 SymbolName；附带的 @click 保留本页「focus 的情报」联动，打开侧栏走组件自身合同 -->
            <SymbolName v-if="item.symbol" :code="item.symbol" @click="loadFocus(item.symbol)" />
            <span>{{ item.title || item.body }}</span>
          </li>
        </ul>
        <p v-if="!words.length" class="nt-empty">热词未返回。</p>
        <ul v-else class="words">
          <li v-for="word in words" :key="word.type + word.target">
            <SymbolName v-if="wordClickable(word)" :code="word.target" @click="loadFocus(word.target)" />
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
        <NtErrorGuide :error="haltError" />
        <!-- 持仓接口失败和真的空仓分开写：没读到时账面不显示成空仓（B3 收编进 NtErrorGuide，v-if 保住后面 else 链） -->
        <NtErrorGuide v-if="positionsError" :error="positionsError" />
        <p v-else-if="!account.positions.length" class="nt-empty">暂无持仓。</p>
        <div v-else class="pos-table">
          <!-- §6.3 R3 持仓行列序：证券|数量|可用|成本|现价|浮盈额|浮盈%|今日可卖|权重 -->
          <div class="pos-row pos-row--head">
            <span>证券</span>
            <span>数量</span>
            <span>可用</span>
            <span>成本</span>
            <span>现价</span>
            <span>浮盈额</span>
            <span>浮盈%</span>
            <span>今日可卖</span>
            <span>权重</span>
          </div>
          <div v-for="item in account.positions" :key="item.symbol" class="pos-row">
            <!-- §6.3 R3 持仓行：SymbolName(stacked) 名称上行代码下行，缺名走「名称未返回」占位 -->
            <SymbolName :code="item.symbol" mode="stacked" />
            <b>{{ item.quantity ?? '—' }}</b>
            <b>{{ item.available ?? '—' }}</b>
            <b>{{ priceText(item.avgCost) }}</b>
            <b>{{ priceText(item.lastPrice) }}</b>
            <i :class="pnlClass(pnlAmount(item))">{{ pnlAmount(item) != null ? amount(pnlAmount(item)) : '—' }}</i>
            <i :class="pnlClass(pnlRatio(item))">{{ pnlRatio(item) != null ? ratioPct(pnlRatio(item), 1) : '—' }}</i>
            <!-- 今日可卖：available 即 T+1 可卖口径；0 显「T+1 不可卖」text-3，缺显「—」 -->
            <em class="sellable" :class="{ 'sellable--hold': sellable(item).hold }">{{ sellable(item).text }}</em>
            <em>{{ weight(item) || '—' }}</em>
          </div>
        </div>
        <h3 class="sub">板块分布（持仓+候选）</h3>
        <p v-if="membershipNote" class="nt-note">{{ membershipNote }}</p>
        <p v-if="!membershipReady" class="nt-empty">板块归属接口未应答。</p>
        <NtSectorDist v-else :rows="distRows" height="220px" />
      </section>
    </div>

    <section v-if="focus" class="nt-panel">
      <h2 class="nt-panel__title">{{ focus }} 的情报</h2>
      <p v-if="intelSentiment != null" class="nt-note">近端综合情感 {{ intelSentiment }}</p>
      <p v-if="!intelItems.length" class="nt-empty">这条时间线是空的。新闻源没入库时，这里不会凭空出现消息。</p>
      <ul v-else class="rows">
        <!-- W0-3b/FE-I4：消息行=IntelRow 单一事实组件（此前此处只剩 时间/标题/来源 三裁口径，
             现与情报中枢/行情侧栏同源：重要度强章+弱标、kind、原文外链一并到位） -->
        <IntelRow v-for="item in intelItems" :key="item.id || item.title" :item="item" />
      </ul>
    </section>
  </div>
</template>

<style scoped>
.desk-split {
  align-items: start;
}

/* FE-O5 竞价强弱条：一行收编（与梯队情绪细条同族），读数等宽、红涨绿跌走 pnlClass。 */
.auction-strip {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 14px;
  margin: 0;
  padding: 7px 10px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-1);
  font-size: 12px;
  color: var(--nt-text-2);
}

.auction-strip__label {
  color: var(--nt-text-3);
  font-size: 11.5px;
}

.auction-strip b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-1);
}

.auction-strip b.up {
  color: var(--nt-up);
}

.auction-strip b.down {
  color: var(--nt-down);
}

.auction-strip__meta {
  color: var(--nt-text-3);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

/* FE-O1 链路徽注行：原「系统」卡拆出的弱档徽注，贴在四卡下方不抢读数。 */
.link-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin: 0;
}

/* FE-O3 漏选分组滚动区：完整列表不再截断，长列表栏内滚动，不撑高整列与整页。 */
.miss-groups {
  margin-top: 6px;
  max-height: 320px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* FE-O3 漏选分组计数头：原因中文口径左置、聚合计数右置（与 lane h3 计数同档）。 */
.miss-group h4 {
  margin: 0;
  font-size: 11px;
  font-weight: 600;
  color: var(--nt-text-3);
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}

.miss-group h4 b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.miss-group ul {
  margin-top: 4px;
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

.brief-items li,
.words li {
  display: flex;
  gap: 8px;
  align-items: baseline;
  font-size: 12.5px;
}

.brief-items li span {
  min-width: 0;
}

.tm {
  color: var(--nt-text-3);
  font-size: 11px;
  font-family: var(--nt-font-mono);
  min-width: 42px;
}

.link {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-accent);
  cursor: pointer;
  font: inherit;
}

/* W0-3b：.rows 情报行样式已随 IntelRow 组件收口（行体 flex/mono 时间/来源位）。 */
.words b {
  margin-left: auto;
}

.pos-table {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
  overflow-x: auto;
}

/* R3 持仓表：证券 1.2fr + 8 个数值列；窄屏整体横向滚动，不挤扁证券列。 */
.pos-row {
  display: grid;
  grid-template-columns:
    minmax(96px, 1.2fr) minmax(48px, 0.6fr) minmax(48px, 0.6fr) minmax(56px, 0.7fr) minmax(56px, 0.7fr)
    minmax(72px, 0.8fr) minmax(56px, 0.6fr) minmax(88px, 0.8fr) minmax(48px, 0.5fr);
  gap: 8px;
  align-items: baseline;
  min-width: 560px;
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

/* R3 今日可卖：数值常态 text-2；0 →「T+1 不可卖」text-3（方案 §6.3 R3）。 */
.pos-row .sellable {
  color: var(--nt-text-2);
  white-space: nowrap;
}

.pos-row .sellable--hold {
  color: var(--nt-text-3);
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

/* FE-O2 票卡标准化：票卡两行——头行 时间/代码/状态徽标，副行 方向/分数/入场区间/有效期倒计时；
   方向色带=左缘 inset 2px（梯队卡 inset 轨道先例收窄档），选中描边不受影响。 */
.lane li {
  display: flex;
  flex-direction: column;
  gap: 3px;
  align-items: stretch;
  font-size: 12px;
  padding: 5px 6px 5px 8px;
  border-radius: var(--nt-radius-s);
  border: 1px solid transparent;
  cursor: pointer;
}

.lane li.side-buy {
  box-shadow: inset 2px 0 var(--nt-up);
}

.lane li.side-sell {
  box-shadow: inset 2px 0 var(--nt-down);
}

.lane li.on {
  border-color: var(--nt-accent);
  background: color-mix(in srgb, var(--nt-accent) 8%, transparent);
}

.lane li .row {
  display: flex;
  gap: 6px;
  align-items: baseline;
}

.lane li .meta {
  display: flex;
  gap: 8px;
  align-items: baseline;
  flex-wrap: wrap;
  font-size: 11px;
  color: var(--nt-text-3);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.lane li .meta em {
  font-style: normal;
}

.lane li .meta em.up {
  color: var(--nt-up);
}

.lane li .meta em.down {
  color: var(--nt-down);
}

.lane li .meta .warn {
  color: var(--nt-warn);
}

/* 已执行票头行的了结注（closedNote 单源）：弱档右置。 */
.lane li .row-note {
  font-size: 11px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

/* FE-O2 漏选卡可钻取：整行按钮跳信号中枢按代码定位。 */
.lane li .miss-row {
  width: 100%;
  display: flex;
  gap: 6px;
  align-items: baseline;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
  text-align: left;
}

/* SymbolName 在 lane 里占弹性位，名称过长走组件内省略。 */
.lane li :deep(.sn) {
  min-width: 0;
  flex: 1;
}

.lane li b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.lane li b.hot {
  color: var(--nt-warn); /* 删 fallback 遮羞布 */
}

.mono {
  font-family: var(--nt-font-mono);
}

/* W0-3b/FE-O6：票头 .tag/.done、票体 .ticket__*、.votes/.objections/.reasons/.evidence/.chip、
   .action-plan 已随 TicketDetail/TicketAction 收编迁出（组件 scoped 内唯一出处）。
   FE-O3：漏选原因徽注 .miss 随分组计数头收口删除（组头唯一出处）。 */
.desk__body {
  min-width: 0;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

@media (max-width: 900px) {
  .desk {
    grid-template-columns: 1fr;
  }
}
</style>
