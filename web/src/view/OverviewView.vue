<script setup lang="ts">
/**
 * 决策台：一屏看清今天能不能做、做什么、为什么没选上。
 * 账户 / 情绪 / 风控仍走 store；信号、候选、晨报、热词、数据源在本页取。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getBriefing, type Briefing } from '@/api/brain'
import { listSources, type DataSource } from '@/api/datahub'
import { hotWords, timeline, type HotWord, type IntelItem } from '@/api/intel'
import { listSignals, type SignalItem } from '@/api/signals'
import { listCandidates } from '@/api/strategy'
import type { Candidate } from '@/api/types/strategy'
import NtSectorBar from '@/viz/NtSectorBar.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtButton from '@/ui/NtButton.vue'
import { useAccountStore } from '@/stores/account'
import { useMarketStore } from '@/stores/market'
import { useRiskStore } from '@/stores/risk'
import { amount, clock, messageOf, phaseName, pnlClass, price, ratioPct, sideLabel, signed, validRemainText } from '@/utils/format'
import { isLive, onPush, type WsPushMessage } from '@/ws/hub'
import { getWorkbench } from '@/workspace/inject'
import type { Position } from '@/api/trade'
import { buildGaps, countBlockedSources, missLabel, strategyLabel, type DeskGap } from './decision-desk'

const REFRESH_MS = 30_000

const account = useAccountStore()
const market = useMarketStore()
const risk = useRiskStore()

const signals = ref<SignalItem[]>([])
const candidates = ref<Candidate[]>([])
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

let timer: ReturnType<typeof setInterval> | null = null
let offPush: (() => void) | undefined
let lastPush = 0

const noTape = computed(() => market.up === 0 && market.down === 0)
/** 顶栏之外，决策台卡片以当日盈亏领衔；缺 dayPnl 时用浮动盈亏兜底并在 sub 里写明。 */
const dayPnlValue = computed(() => (account.dayPnl != null ? account.dayPnl : account.unrealized))
const usedPct = computed(() => {
  if (!account.equity || account.cash == null) return null
  return 1 - account.cash / account.equity
})
const chartRows = computed(() =>
  market.sectors
    .filter((row) => row.avgPct != null)
    .map((row) => ({ name: row.name, avgPct: Number((row.avgPct as number).toFixed(2)) })),
)
const actionRows = computed(() =>
  [...signals.value].sort((a, b) => (b.score ?? 0) - (a.score ?? 0)).slice(0, 6),
)
const missRows = computed(() =>
  candidates.value
    .filter((row) => row.stage !== 'selected' && row.missReason)
    .sort((a, b) => (b.ruleScore ?? 0) - (a.ruleScore ?? 0))
    .slice(0, 8),
)
const selectedCount = computed(() => candidates.value.filter((row) => row.stage === 'selected').length)
const gaps = computed<DeskGap[]>(() =>
  buildGaps({
    sourcesFailed: sourcesFailed.value,
    sourcesEmpty: sourcesLoaded.value && !sourcesFailed.value && sources.value.length === 0,
    sourcesBlocked: countBlockedSources(sources.value),
    noTape: noTape.value,
    briefingMissing: briefingMissing.value,
    briefingStale: !!briefing.value?.stale,
    anyDegraded: signals.value.some((row) => row.degraded),
    paperDays: account.paperDays,
    killActive: risk.killActive,
    live: isLive(),
  }),
)

function weight(position: Position): string {
  if (!account.equity || !position.lastPrice || !position.quantity) return ''
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

async function loadFocus(symbol: string) {
  focus.value = symbol
  try {
    const data = await timeline(symbol)
    intelItems.value = data.items.slice(0, 6)
    intelSentiment.value = data.sentiment
  } catch (err) {
    intelItems.value = []
    intelSentiment.value = null
    error.value = messageOf(err)
  }
}

async function load(manual = false) {
  if (manual) loading.value = true
  error.value = ''
  const [accountRes, marketRes, riskRes, signalRes, candidateRes, wordRes, briefRes, sourceRes] =
    await Promise.allSettled([
      account.refresh(),
      market.refresh(),
      risk.refresh(),
      listSignals(),
      listCandidates({ limit: 40 }),
      hotWords(),
      getBriefing(''),
      listSources(),
    ])

  if (signalRes.status === 'fulfilled') signals.value = signalRes.value
  if (candidateRes.status === 'fulfilled') candidates.value = candidateRes.value
  if (wordRes.status === 'fulfilled') words.value = wordRes.value.slice(0, 8)
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

  const failed = [accountRes, marketRes, riskRes, signalRes, candidateRes].find((item) => item.status === 'rejected')
  if (failed && failed.status === 'rejected') error.value = messageOf(failed.reason)
  loading.value = false
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'signal' && msg.topic !== 'alert') return
  const data = msg.data && typeof msg.data === 'object' ? (msg.data as Record<string, unknown>) : {}
  const title = String(data.title || data.subject || data.symbol || data.kind || '')
  if (title) pulse.value = `${msg.topic === 'signal' ? '信号' : '告警'} ${title}`
  const now = Date.now()
  if (now - lastPush < 2000) return
  lastPush = now
  void load(false)
}

onMounted(() => {
  void load(true)
  timer = setInterval(() => void load(false), REFRESH_MS)
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  offPush?.()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">决策台</h1>
        <p class="nt-note">一屏看缺口、今日动作、漏选原因和持仓。没有读到的项保持为「—」，不把空数据当成平静。</p>
      </div>
      <NtButton :loading="loading" @click="load(true)">刷新</NtButton>
    </header>
    <p v-if="pulse" class="nt-note">
      刚到：<button type="button" class="link" @click="openPulse">{{ pulse }}</button>
    </p>
    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <h2 class="nt-panel__title">现在缺什么</h2>
      <p v-if="!gaps.length" class="nt-empty">链路齐：快照、晨报、模型和行情通道都有数。仍只做两个模板，不是全市场机会。</p>
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
        :sub="account.dayPnl != null ? `已实现 ${signed(account.realized)}` : '无当日盈亏口径，显示浮动盈亏'"
        :tone="pnlClass(dayPnlValue) || 'neutral'"
        hero
      />
      <NtStatCard
        label="权益"
        :value="amount(account.equity)"
        :sub="`可用 ${amount(account.cash)}${usedPct != null ? ` · 已用 ${(usedPct * 100).toFixed(0)}%` : ''}`"
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
        :sub="`模式 ${risk.mode || '—'} · 炸板率 ${ratioPct(market.brokenRate)}`"
      />
    </section>

    <div class="nt-split desk-split">
      <section class="nt-panel">
        <h2 class="nt-panel__title">今日动作</h2>
        <p v-if="!actionRows.length" class="nt-empty">今日没有信号。模板只有首板弱转强和强势回踩，而且要有日线和快照才会出。</p>
        <ul v-else class="rows">
          <li v-for="item in actionRows" :key="item.id || item.symbol + item.time">
            <span class="tm">{{ clock(item.time) }}</span>
            <button type="button" class="sym" @click="openPanel('market', { symbol: item.symbol })">
              {{ item.symbol }}
            </button>
            <span class="name">{{ item.name || strategyLabel(item.strategy) }}</span>
            <em :class="item.side === 'buy' ? 'up' : item.side === 'sell' ? 'down' : ''">{{ sideLabel(item.side) }}</em>
            <b :class="{ hot: item.highValue }">{{ item.score ?? '—' }}</b>
            <span class="px">{{ price(item.entryLow) }}–{{ price(item.entryHigh) }} / 止损 {{ price(item.stop) }}</span>
            <span class="valid" :class="{ warn: !item.validUntil }">
              {{ item.validUntil ? validRemainText(item.validUntil) || '已过期' : '无有效期' }}
            </span>
            <span v-if="item.positionPct != null" class="pos">仓位 {{ (item.positionPct * 100).toFixed(0) }}%</span>
            <button type="button" class="link" @click="loadFocus(item.symbol)">情报</button>
            <button type="button" class="link" @click="openPanel('trade', { symbol: item.symbol, side: item.side, price: item.entry || '' })">
              下单
            </button>
          </li>
        </ul>
        <p v-if="actionRows.length" class="nt-note more">
          <button type="button" class="link" @click="openPanel('signals')">打开信号中枢</button>
        </p>
      </section>

      <section class="nt-panel">
        <h2 class="nt-panel__title">晨报与热词</h2>
        <p v-if="briefing?.summary" class="brief">{{ briefing.summary }}</p>
        <p v-else class="nt-empty">晨报未返回。</p>
        <ul v-if="briefing?.items.length" class="brief-items">
          <li v-for="(item, index) in briefing.items.slice(0, 4)" :key="index">
            <button v-if="item.symbol" type="button" class="sym" @click="loadFocus(item.symbol)">{{ item.symbol }}</button>
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
        </p>
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

    <div class="nt-split desk-split">
      <section class="nt-panel">
        <h2 class="nt-panel__title">为什么没选上</h2>
        <p class="nt-note">候选 {{ candidates.length }} · 入选 {{ selectedCount }}。漏选原因来自漏斗，不是事后解释。</p>
        <p v-if="!missRows.length" class="nt-empty">没有漏选记录。全市场扫描要等日线和快照进来之后才会写候选。</p>
        <ul v-else class="rows">
          <li v-for="row in missRows" :key="row.id || row.symbol + row.strategy">
            <button type="button" class="sym" @click="openPanel('market', { symbol: row.symbol })">{{ row.symbol }}</button>
            <span class="name">{{ row.name || strategyLabel(row.strategy) }}</span>
            <span>{{ missLabel(row.missReason) }}</span>
            <b>规则 {{ row.ruleScore ?? '—' }}</b>
          </li>
        </ul>
        <p class="nt-note more">
          <button type="button" class="link" @click="openPanel('pool-manager')">打开股池</button>
        </p>
      </section>

      <section class="nt-panel">
        <h2 class="nt-panel__title">持仓</h2>
        <p v-if="account.openHalted" class="nt-err">开仓已停{{ account.haltReason ? `：${account.haltReason}` : '' }}</p>
        <p v-if="!account.positions.length" class="nt-empty">暂无持仓。</p>
        <div v-else class="pos-table">
          <div class="pos-row pos-row--head">
            <span>代码</span>
            <span>数量</span>
            <span>权重</span>
            <span>浮盈</span>
          </div>
          <div v-for="item in account.positions" :key="item.symbol" class="pos-row">
            <button type="button" class="sym" @click="openPanel('market', { symbol: item.symbol })">{{ item.symbol }}</button>
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
}

.sub {
  margin: 14px 0 0;
  font-size: 13px;
  font-weight: 600;
}
</style>
