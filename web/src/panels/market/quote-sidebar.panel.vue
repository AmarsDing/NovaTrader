<script setup lang="ts">
/**
 * 单券行情侧栏（视频设计指引 §8 右栏恒随 + 方案 §6.4 个股浓缩卡四区块）：
 * 身份（SymbolName stacked + 市场/行业/ST·停牌徽注，全走 security 字典）
 * + 价格（hero 现价/涨跌 + 昨收/今开/最高/最低/振幅/涨跌停）
 * + 量额（总量/总额/换手；量比 GetQuote 未携带显「—」，52 周高/低走 B-2 optional——缺窗显「—」）
 * + 估值（流通市值/PE(TTM)/PB 走字典；总市值字典未携带显「—」）+ 五档 + 相关情报。
 * 纪律：
 * - 恒随全局 symbol store——切代码只换数据，布局不动；
 * - 数据面：切代码 REST 首刷 → subscribe('market', {symbol}) 推送防抖触发再刷 → 手动刷新兜底；
 *   WS 断连 → NtFreshness cut 显「断 · 最后时刻」（warn，非实时语义唯一承载，2026-10-21 评审撤除重复徽标），快照过期显琥珀「已过期」；
 * - 算不出的字段显「未知」（后端零值不猜），无源字段显「—」（数据纪律「没取到≠0」，禁虚构禁派生），
 *   五档统一 viz/NtDepthBoard（B3 死件接入）：真档画量柱+卖5..买5 侧标，无档合成兜底（“合成·待数据” warn 口径，
 *   只画价不伪造量），合成输入也不可得→组件旁仍落纯文字空态，逐笔/异动入口显「P1 待接入」。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  getChipDistribution,
  getQuote,
  getSeatStats,
  type ChipDistribution,
  type QuoteView,
  type SeatStatsReply,
} from '@/api/market'
import { timeline, type IntelItem } from '@/api/intel'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTabs from '@/ui/NtTabs.vue'
import SymbolName from '@/display/SymbolName.vue'
import { statusOf, type StatusEntry } from '@/ui/status-map'
import { clock, compactAmount, messageOf, pnlClass, ppText, price, ratioPct, signed, toneOf } from '@/utils/format'
import { useSecurityStore } from '@/stores/security'
import { useSymbolStore } from '@/stores/symbol'
import { getWorkbench } from '@/workspace/inject'
import { consumePanelParams, onPanelParams } from '@/workspace/panel-bus'
import { isLive, onPush, subscribe, type WsPushMessage } from '@/ws/hub'
import { dataFrame } from '@/ws/data-frame'
import { quoteFlash, type FlashSample } from '@/utils/quote-flash'
import NtDepthBoard from '@/viz/NtDepthBoard.vue'

const symbolStore = useSymbolStore()
const security = useSecurityStore()

/** FE-M4/W1：筹码分布卡（随个股加载）与席位胜率卡（全市场口径，挂载一次）。
 *  三态纪律：ok / 不可用（status≠ok 显后端 reason 原样）/ 读取失败（不冒充「无数据」）。 */
const chip = ref<ChipDistribution | null>(null)
const chipFailed = ref(false)
const seats = ref<SeatStatsReply | null>(null)
const seatsFailed = ref(false)

/** 面板头的手输框：跟随全局代码；回车/失焦写回 store（source 记本面板）。 */
const draft = ref(symbolStore.code)
const quote = ref<QuoteView | null>(null)
const news = ref<IntelItem[]>([])
const newsReady = ref(false)
/** 情报三态缺的「未返回」位（B3 走查销号）：catch 吞错时 newsReady 不置位会永久冒「读取中」。 */
const newsFailed = ref(false)
const error = ref('')
const loading = ref(false)
const tab = ref<'intel' | 'flash'>('intel')
/** 面板生命周期内的连接快照（渲染期用 isLive() 直读，watcher 只负责触发刷新）。 */
const liveNow = computed(() => isLive())
/** §7.3 头区新鲜度时点：快照 asOf 解析为 epoch ms；空/非法 → null，NtFreshness 显「时点未知」（数据纪律，不冒充当前时刻）。 */
const freshAt = computed(() => {
  const raw = quote.value?.asOf
  if (!raw) return null
  const t = Date.parse(raw)
  return Number.isFinite(t) ? t : null
})

let loadSeq = 0
let disposed = false
let unsubscribe: (() => void) | undefined
let offPush: (() => void) | undefined
let offParams: (() => void) | undefined

const code = computed(() => symbolStore.code)

/** 现价跳变闪动（G1，与行情卡 MarketView 同节律）：REST 快照差分打标——本面板闪动节奏=快照重取节奏。 */
const flashSample = ref<FlashSample | null>(null)
const offFlash = quoteFlash.onChange((map) => { flashSample.value = map.get(code.value) ?? null })
const qFlashClass = computed(() => {
  const sample = flashSample.value
  if (!sample || !sample.flashing) return ''
  return sample.direction === 'up' ? 'q-flash--up' : sample.direction === 'down' ? 'q-flash--down' : ''
})

async function load(withNews = false) {
  const sym = code.value
  if (!sym) return
  const seq = ++loadSeq
  loading.value = true
  error.value = ''
  try {
    const [q, intelRes, chipRes] = await Promise.all([
      getQuote(sym),
      withNews ? timeline(sym).catch(() => null) : Promise.resolve(null),
      // FE-M4/W1：筹码随个股加载——独立失败态，不挡行情主链路。
      getChipDistribution(sym).catch(() => null),
    ])
    if (disposed || seq !== loadSeq || sym !== code.value) return
    // G1：快照差分打跳——同代码、前后两帧都有真值且现价变化才打标（切代码 prev=null 不打）。
    const prev = quote.value
    if (prev && q.found && prev.last > 0 && q.last > 0 && q.last !== prev.last) {
      quoteFlash.mark(sym, q.last > prev.last ? 'up' : 'down')
    }
    quote.value = q
    chip.value = chipRes
    chipFailed.value = !chipRes
    if (q.found && q.name) symbolStore.fillName(sym, q.name)
    // 情报三态（B3 走查销号）：失败也算读过了——「未返回≠没有消息」，显式区分，不永冒「读取中」。
    if (withNews) {
      news.value = intelRes ? intelRes.items.slice(0, 8) : []
      newsReady.value = true
      newsFailed.value = !intelRes
    }
    if (!q.found) error.value = '还没有这只证券的行情，先确认代码与数据采集。'
  } catch (err) {
    if (disposed || seq !== loadSeq) return
    error.value = messageOf(err)
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

/** FE-M4/W1：席位胜率=全市场龙虎榜口径（GetSeatStats 无 symbol 维度），挂载加载一次，不随个股刷新。 */
async function loadSeats() {
  try {
    seats.value = await getSeatStats('', 8)
    seatsFailed.value = false
  } catch {
    seatsFailed.value = true
  }
}

/** 推送只当刷新信号：同代码的 bar/快照帧到达 → 统一数据帧至多每 800ms 重取快照（G1 收编手写防抖：
 *  合帧/隐藏页降频/负载自适应由 dataFrame 兜底；与 limit-ladder 同纪律——固定 key 单节流器，
 *  任务闭包被 disposed 短路，不随面板实例增生。推送不背五档/涨跌停真值的口径不变。） */
const PUSH_REFRESH_MIN_INTERVAL_MS = 800
function scheduleRefresh() {
  dataFrame.scheduleThrottled('quote-sidebar', PUSH_REFRESH_MIN_INTERVAL_MS, () => {
    if (!disposed && isLive() && code.value) void load()
  })
}

function resub() {
  unsubscribe?.()
  unsubscribe = code.value ? subscribe('market', { symbol: code.value }) : undefined
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'market' || !msg.data || typeof msg.data !== 'object') return
  const sym = (msg.data as Record<string, unknown>).symbol
  // 帧不带 symbol（快照批次广播）或与当前代码一致都算刷新信号；
  // 带别的代码的帧说明推送收窄未生效，忽略。
  if (typeof sym === 'string' && sym && sym !== code.value) return
  scheduleRefresh()
}

function applyStoreCode(next: string) {
  if (!next) return
  if (draft.value !== next) draft.value = next
  quote.value = null
  news.value = []
  newsReady.value = false
  newsFailed.value = false
  resub()
  void load(true)
}

function submitDraft() {
  if (symbolStore.setSymbol(draft.value, 'QL')) return
  // 同值：等同手动刷新
  void load(true)
}

watch(code, (next, before) => {
  if (next && next !== before) applyStoreCode(next)
}, { flush: 'sync' })

/* ---------- 展示派生 ---------- */

const tone = computed(() => toneOf(quote.value?.pctChg ?? null))
const priceNow = computed(() => (quote.value && quote.value.last > 0 ? price(quote.value.last) : '未知'))
/** 价格区状态章为行情口径散值（SUSPEND/无涨跌幅/快照过期），文案/tone 走 status-map quote.* 册
 * （§7.1 第二批散值入册）；判定逻辑保持原样，只换文案与色源，禁自封。 */
const statusBadges = computed<StatusEntry[]>(() => {
  const q = quote.value
  const out: StatusEntry[] = []
  if (!q) return out
  if (q.tradeStatus === 'SUSPEND') out.push(statusOf('quote.suspended'))
  if (q.noLimit) out.push(statusOf('quote.nolimit'))
  if (q.stale) out.push(statusOf('quote.stale'))
  return out
})

interface FieldItem { label: string; value: string }

/** 缺失两态（数据纪律，泾渭分明）：
 * 「未知」= 行情有该字段但 0/空（GetQuote 契约 P3-10c，后端零值不猜）；
 * 「—」 = 字段无数据源/字典缺行（方案 §6.4 缺失表现，禁虚构禁派生）。 */
const MISS_QUOTE = '未知'
const MISS_FIELD = '—'
function isMiss(v: string) { return v === MISS_QUOTE || v === MISS_FIELD }

/** security 字典行（§6.4 身份/估值区块数据源，§6.2 字典）：无行=字典尚未回补，逐项显「—」不冒充。 */
const basic = computed(() => (code.value ? security.get(code.value) : undefined))
/** 行业行：dict industry，缺省「—」（text-3）。 */
const industryText = computed(() => basic.value?.industry || MISS_FIELD)

/** 价格区块（§6.4）：现价/涨跌在 hero（.last 走 --nt-fs-hero 令牌，§5.3 hero 档 24px / 紧凑密度 23px），
 * 「无涨跌幅」票涨跌停价显「无」。 */
const priceFields = computed<FieldItem[]>(() => {
  const q = quote.value
  if (!q || !q.found) {
    return ['昨收', '今开', '最高', '最低', '振幅', '涨停价', '跌停价']
      .map((label) => ({ label, value: MISS_QUOTE }))
  }
  const num = (v: number, fmt: (v: number) => string) => (v ? fmt(v) : MISS_QUOTE)
  return [
    { label: '昨收', value: num(q.prevClose, price) },
    { label: '今开', value: num(q.open, price) },
    { label: '最高', value: num(q.high, price) },
    { label: '最低', value: num(q.low, price) },
    { label: '振幅', value: q.amplitude > 0 ? ppText(q.amplitude) : MISS_QUOTE },
    { label: '涨停价', value: q.noLimit ? '无' : num(q.limitUp, price) },
    { label: '跌停价', value: q.noLimit ? '无' : num(q.limitDown, price) },
  ]
})

/** 量额区块（§6.4）：总量/总额/换手出自 GetQuote。
 * 「—」口径两则：量比 GetQuote proto 未携带盘中口径（仅竞价截面有 auction_vol_ratio，口径不同不挪用）；
 * 52 周高/低走附录 B-2（Quote.high_52w/low_52w optional，前端已接线）——服务端窗口不足不返回→「—」。 */
const volumeFields = computed<FieldItem[]>(() => {
  const q = quote.value
  if (!q || !q.found) {
    return [
      { label: '总量', value: MISS_QUOTE },
      { label: '总额', value: MISS_QUOTE },
      { label: '换手', value: MISS_QUOTE },
      { label: '量比', value: MISS_FIELD },
      { label: '52周高/低', value: MISS_FIELD },
    ]
  }
  return [
    { label: '总量', value: q.volume ? compactAmount(q.volume) : MISS_QUOTE },
    { label: '总额', value: q.amount ? compactAmount(q.amount) : MISS_QUOTE },
    { label: '换手', value: q.turnoverRate > 0 ? ppText(q.turnoverRate) : MISS_QUOTE },
    { label: '量比', value: MISS_FIELD },
    { label: '52周高/低', value: q.high52w && q.low52w ? `${price(q.low52w)} / ${price(q.high52w)}` : MISS_FIELD },
  ]
})

/** 估值区块（§6.4）：全部读 security 字典（stock_basic 口径），truthy 守卫让负 PE 照样渲染、0/null 显「—」。
 * 总市值走字典日级口径（stock_basic.total_mv，C1 采集链落读）；未采集显「—」——不拿 total_share×现价
 * 派生（禁计算派生，盘中即席口径归 Quote 链），两口径勿混。 */
const valuationFields = computed<FieldItem[]>(() => {
  const b = basic.value
  return [
    { label: '流通市值', value: b?.circMv ? compactAmount(b.circMv) : MISS_FIELD },
    { label: 'PE(TTM)', value: b?.peTtm ? b.peTtm.toFixed(1) : MISS_FIELD },
    { label: 'PB', value: b?.pb ? b.pb.toFixed(2) : MISS_FIELD },
    { label: '总市值', value: b?.totalMv ? compactAmount(b.totalMv) : MISS_FIELD },
  ]
})

/** B3 死件接入：五档渲染移交 viz/NtDepthBoard——真档直传 bid/ask；无档合成兜底（含封顶/徽注）组件内建，
 *  合成输入也不可得（last=0 是「没取到」非真价）→ 组件旁保留纯文字空态，不画假盘。 */
const depthSource = computed<{ lastPrice: number | null; preClose: number | null }>(() => {
  const q = quote.value
  return {
    lastPrice: q && q.last > 0 ? q.last : null,
    preClose: q && !q.noLimit && q.prevClose > 0 ? q.prevClose : null,
  }
})

/** FE-M4/W1 筹码卡字段（status==ok 才组列）：价格与衍生列 proto optional——
 *  null 显「—」（「没取到≠0」）；status≠ok 时卡体显后端 reason 原样，不在此拼数。 */
const chipFields = computed<FieldItem[]>(() => {
  const c = chip.value
  if (!c || c.status !== 'ok') return []
  const band = (lo: number | null, hi: number | null) =>
    lo != null && hi != null ? `${price(lo)} – ${price(hi)}` : MISS_FIELD
  return [
    { label: '获利比例', value: c.profit != null ? ratioPct(c.profit) : MISS_FIELD },
    { label: '加权平均成本', value: c.avgCost != null ? price(c.avgCost) : MISS_FIELD },
    { label: '90%成本区', value: band(c.cost90Lo, c.cost90Hi) },
    { label: '90%集中度', value: c.concentration90 != null ? ratioPct(c.concentration90) : MISS_FIELD },
    { label: '70%成本区', value: band(c.cost70Lo, c.cost70Hi) },
    { label: '筹码峰', value: c.peakPrice != null ? price(c.peakPrice) : MISS_FIELD },
  ]
})

/** FE-M4/W1 席位卡：带符号收益率（分数→百分点，红涨绿跌由 pnlClass 在模板承担，null 显「—」）。 */
function signedPct(v: number | null): string {
  return v == null ? '—' : `${v >= 0 ? '+' : ''}${ratioPct(v)}`
}

function openIntel(item?: IntelItem) {
  getWorkbench()?.openPanel('intel', {
    symbol: code.value || undefined,
    highlight: item?.id || undefined,
  })
}

onMounted(() => {
  // 命令面板 QL 600519 / 他面板直达：参数写回 store（store 不变则本面板也无需再动）。
  const params = consumePanelParams('quote-sidebar')
  const staged = typeof params?.symbol === 'string' ? params.symbol : ''
  if (staged) symbolStore.setSymbol(staged, 'QL')
  offParams = onPanelParams('quote-sidebar', (p) => {
    if (typeof p.symbol === 'string') symbolStore.setSymbol(p.symbol, 'QL')
  })
  if (code.value) applyStoreCode(code.value)
  void loadSeats()
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
  disposed = true
  unsubscribe?.()
  offPush?.()
  offParams?.()
  offFlash()
})
</script>

<template>
  <div class="nt-page qs">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">行情侧栏</h1>
        <p class="nt-note">恒随当前证券的行情、五档盘口与相关情报。</p>
      </div>
    </header>

    <form class="nt-form" @submit.prevent="submitDraft">
      <label class="grow">
        <span>代码</span>
        <NtInput v-model="draft" placeholder="600519.SH" @blur="submitDraft" />
      </label>
      <NtButton :loading="loading" @click="submitDraft">刷新</NtButton>
    </form>

    <p v-if="!code" class="nt-empty">还没有当前证券。用命令面板 GP 600519 或在行情面板选一只。</p>
    <NtErrorGuide :error="error" />

    <template v-if="quote">
      <header class="head">
        <div class="head__id">
          <!-- f10 试点（§6.5）：hover 出速览卡，点击进 F10 抽屉（覆盖缺省的设码行为）。 -->
          <SymbolName :code="code" mode="stacked" f10 />
          <div class="head__tags">
            <!-- 身份徽注（方案 §6.4 身份区块）：市场/行业/ST·停牌全出自 security 字典；
                 ST/停牌章缺省不显，文案/tone 收口走 §7.1 status-map 册（security.st=warn / security.suspended=neutral）。 -->
            <NtBadge v-if="basic?.market" tone="neutral">{{ basic.market }}</NtBadge>
            <NtBadge v-if="basic?.st" :tone="statusOf('security.st').tone">{{ statusOf('security.st').text }}</NtBadge>
            <NtBadge v-if="basic?.suspended" :tone="statusOf('security.suspended').tone">{{ statusOf('security.suspended').text }}</NtBadge>
            <span class="head__ind" :class="{ miss: isMiss(industryText) }">{{ industryText }}</span>
          </div>
        </div>
        <div class="head__main" :class="tone">
          <b class="last" :class="qFlashClass">{{ priceNow }}</b>
          <span class="chg">
            <!-- 「没取到≠0」（P3-10c）：prevClose=0 是没取到（新股首日无昨收），pctChg 无口径，显缺失而非假「+0.00%」 -->
            {{ quote.last > 0 ? (quote.prevClose > 0 ? `${signed(quote.chg)} · ${quote.pctChg >= 0 ? '+' : ''}${quote.pctChg.toFixed(2)}%` : '昨收缺失') : '' }}
          </span>
        </div>
        <div class="head__meta">
          <!-- §7.3 首批挂载：新鲜度取快照 asOf（缺/非法 → null 显「时点未知」，不冒充 0s），cut=链路断；
               阈值走组件默认 30s。下方 .asof 绝对时点保留为第二读数。 -->
          <NtFreshness label="行情" :at="freshAt" :cut="!liveNow" />
          <span v-for="b in statusBadges" :key="b.text"><NtBadge :tone="b.tone">{{ b.text }}</NtBadge></span>
          <span class="asof">{{ quote.asOf ? clock(quote.asOf) : '时间未知' }}<template v-if="quote.source"> · {{ quote.source === 'snap' ? '盘中快照' : '日线回退' }}</template></span>
        </div>
      </header>

      <section class="book nt-panel">
        <h2 class="nt-panel__title">五档盘口</h2>
        <NtDepthBoard
          v-if="quote.found && (quote.bid.length || quote.ask.length || depthSource.lastPrice != null)"
          :asks="quote.ask"
          :bids="quote.bid"
          :last-price="depthSource.lastPrice"
          :symbol="quote.symbol"
          :pre-close="depthSource.preClose"
          side-labels
        />
        <!-- 合成输入也不可得（last=0=「没取到」）：组件不画假盘，落纯文字空态。 -->
        <!-- found=false 时 source 空=无回退来源，先说「行情未取到」——冒充「按日线回退」是虚构（B3 走查销号）。 -->
        <p v-else class="nt-empty">当前数据源没有盘口（{{ !quote.found ? '行情未取到' : quote.source === 'snap' ? '盘口缺失' : '快照不可用，按日线回退' }}）。</p>
      </section>

      <section class="grid nt-panel">
        <h2 class="nt-panel__title">价格</h2>
        <dl class="pairs">
          <template v-for="f in priceFields" :key="f.label">
            <dt>{{ f.label }}</dt>
            <dd :class="{ miss: isMiss(f.value) }">{{ f.value }}</dd>
          </template>
        </dl>
      </section>

      <section class="grid nt-panel">
        <h2 class="nt-panel__title">量额</h2>
        <dl class="pairs">
          <template v-for="f in volumeFields" :key="f.label">
            <dt>{{ f.label }}</dt>
            <dd :class="{ miss: isMiss(f.value) }">{{ f.value }}</dd>
          </template>
        </dl>
      </section>

      <section class="grid nt-panel">
        <h2 class="nt-panel__title">估值</h2>
        <dl class="pairs">
          <template v-for="f in valuationFields" :key="f.label">
            <dt>{{ f.label }}</dt>
            <dd :class="{ miss: isMiss(f.value) }">{{ f.value }}</dd>
          </template>
        </dl>
      </section>

      <!-- FE-M4/W1：筹码分布卡（换手衰减模型纯计算，不落库）；status≠ok 显后端 reason 原样，不自封文案 -->
      <section class="grid nt-panel">
        <h2 class="nt-panel__title">筹码分布</h2>
        <p v-if="chipFailed" class="nt-empty">筹码分布读取失败，不能判断筹码结构——稍后刷新重试。</p>
        <p v-else-if="!chip" class="nt-empty">筹码分布读取中……</p>
        <p v-else-if="chip.status !== 'ok'" class="nt-empty">筹码分布不可用：{{ chip.reason || chip.status }}。</p>
        <template v-else>
          <dl class="pairs">
            <template v-for="f in chipFields" :key="f.label">
              <dt>{{ f.label }}</dt>
              <dd :class="{ miss: isMiss(f.value) }">{{ f.value }}</dd>
            </template>
          </dl>
          <p class="nt-note chip-note">
            窗口 {{ chip.windowStart }} – {{ chip.windowEnd }} · 累计换手 {{ ppText(chip.cumTurnoverPct) }} · 纯计算模型（前复权日K×换手率），不落库
          </p>
        </template>
      </section>

      <!-- FE-M4/W1：席位胜率卡（全市场龙虎榜口径，GetSeatStats 无个股维度——注脚明示不冒充个股关联） -->
      <section class="grid nt-panel">
        <h2 class="nt-panel__title">席位胜率</h2>
        <p v-if="seatsFailed" class="nt-empty">席位胜率读取失败，不能判断龙虎榜席位质量——稍后重试。</p>
        <p v-else-if="!seats" class="nt-empty">席位胜率读取中……</p>
        <p v-else-if="!seats.found" class="nt-empty">
          {{ seats.tradeDate || '当日' }} 龙虎榜无席位统计行（当日无上榜或任务未跑）。
        </p>
        <template v-else>
          <ul class="seats">
            <li v-for="row in seats.rows" :key="row.seat">
              <div class="seats__head">
                <b>{{ row.seat }}</b>
                <span>上榜 {{ row.sample }} 次</span>
              </div>
              <div class="seats__line">
                <span
                  >T+1 胜率 {{ row.winRateT1 != null ? ratioPct(row.winRateT1, 0) : '—' }} · 均收
                  <i :class="pnlClass(row.avgRetT1)">{{ signedPct(row.avgRetT1) }}</i></span
                >
                <span
                  >T+5 胜率 {{ row.winRateT5 != null ? ratioPct(row.winRateT5, 0) : '—' }} · 均收
                  <i :class="pnlClass(row.avgRetT5)">{{ signedPct(row.avgRetT5) }}</i></span
                >
              </div>
            </li>
          </ul>
          <p class="nt-note chip-note">
            全市场龙虎榜口径，与当前个股无关 · 基准日 {{ seats.tradeDate || seats.asOf }} · 窗口
            {{ seats.rows[0]?.lookback ?? '—' }} 交易日
          </p>
        </template>
      </section>

      <section class="nt-panel">
        <NtTabs
          v-model="tab"
          :items="[
            { value: 'intel', label: '相关情报' },
            { value: 'flash', label: '快讯' },
          ]"
        />
        <div v-if="tab === 'intel'">
          <p v-if="!news.length" class="nt-empty">{{ newsFailed ? '消息未返回，不能判断有无相关消息。' : newsReady ? '没有相关消息。' : '读取中……' }}</p>
          <ul v-else class="news">
            <!-- 可达性（B3 走查销号，双透镜互证）：可点 li 补 role/tabindex/键盘事件，与全站 button 焦点环同咒语。 -->
            <li
              v-for="item in news"
              :key="item.id || item.time + item.title"
              role="button"
              tabindex="0"
              title="打开情报面板查看"
              @click="openIntel(item)"
              @keydown.enter.prevent="openIntel(item)"
              @keydown.space.prevent="openIntel(item)"
            >
              <b>{{ clock(item.time) || '—' }}</b><!-- 时点缺失显占位，不留空白让人猜 -->
              <span>{{ item.title }}</span>
            </li>
          </ul>
          <NtButton size="sm" variant="ghost" @click="openIntel()">打开情报面板</NtButton>
        </div>
        <p v-else class="nt-empty">逐笔与异动流：P1 待接入通达信采集通道（无数据源，不造假）。</p>
      </section>
    </template>
  </div>
</template>

<style scoped>
.qs {
  /* 收编自 12.5px 裸值（B3 走查批六枪归阶梯，等值档映射，compact 密度随动） */
  font-size: var(--nt-fs-body);
}

.head {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  flex-wrap: wrap;
  gap: 10px;
  padding: 10px 12px;
  margin-bottom: 10px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.head__id {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.head__tags {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.head__ind {
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.head__ind.miss {
  color: var(--nt-text-3);
}

.head__main {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.last {
  font-size: var(--nt-fs-hero); /* 收编自 24px 裸值（§5.3 hero 档，等值档映射；紧凑密度 23px 随动） */
  font-weight: 700;
  /* hero 档（方案 §5.1）：单面板唯一 text-0 大数字；± 颜色由 .chg 继承 tone 承担。 */
  color: var(--nt-text-0);
  line-height: 1.05;
  padding: 0 4px;
  margin: 0 -4px;
  border-radius: var(--nt-radius-s);
}

/* 现价跳变短脉冲：快照差分打标，300ms 内淡出；减少动态档由全局 animation:none 接管（与 MarketView 同节律）。 */
.q-flash--up {
  animation: qs-flash-up 0.3s ease-out;
}

.q-flash--down {
  animation: qs-flash-down 0.3s ease-out;
}

@keyframes qs-flash-up {
  0% { background: color-mix(in srgb, var(--nt-up) 32%, transparent); }
  100% { background: transparent; }
}

@keyframes qs-flash-down {
  0% { background: color-mix(in srgb, var(--nt-down) 32%, transparent); }
  100% { background: transparent; }
}

.chg {
  font-size: var(--nt-fs-body);
  font-weight: 600;
}

.head__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  min-width: 0;
  gap: 6px;
}

.asof {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  /* 时标归 micro 档（阶梯税则：micro=时间戳/单位/脚注），收编自 11px 裸值 */
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}

.book,
.grid {
  margin-bottom: 10px;
}

.pairs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 18px;
  margin: 0;
  font-size: var(--nt-fs-body);
}

.pairs dt {
  color: var(--nt-text-3);
}

.pairs dd {
  margin: 0;
  text-align: right;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-1);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pairs dd.miss {
  color: var(--nt-text-3);
}

/* FE-M4/W1：筹码/席位卡注脚（口径与窗口一行说清，不塞进 dl 冒充字段）。 */
.chip-note {
  margin: 6px 0 0;
  font-size: var(--nt-fs-micro);
}

.seats {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.seats li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: var(--nt-fs-body);
}

.seats__head {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}

.seats__head b {
  font-weight: 500;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.seats__head span {
  flex: none;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-3);
  font-size: var(--nt-fs-micro);
}

.seats__line {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-2);
}

.seats__line i {
  font-style: normal;
}

.news {
  list-style: none;
  margin: 8px 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 220px;
  overflow: auto;
}

.news li {
  display: flex;
  gap: 8px;
  font-size: var(--nt-fs-body);
  cursor: pointer;
}

.news li:hover span {
  color: var(--nt-text-1);
}

/* 键盘焦点与 hover 同款提字 + 全站代表焦点环（controls.css 咒语原样抄写）。 */
.news li:focus-visible {
  outline: 2px solid var(--nt-accent);
  outline-offset: 2px;
  box-shadow: 0 0 0 2px var(--nt-focus-ring);
}

.news li:focus-visible span {
  color: var(--nt-text-1);
}

.news b {
  flex: none;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  color: var(--nt-text-3);
}
</style>
