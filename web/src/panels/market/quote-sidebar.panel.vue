<script setup lang="ts">
/**
 * 单券行情侧栏（视频设计指引 §8 右栏恒随 + 方案 §6.4 个股浓缩卡四区块）：
 * 身份（SymbolName stacked + 市场/行业/ST·停牌徽注，全走 security 字典）
 * + 价格（hero 现价/涨跌 + 昨收/今开/最高/最低/振幅/涨跌停）
 * + 量额（总量/总额/换手；量比、52 周高/低无数据源占位「—」）
 * + 估值（流通市值/PE(TTM)/PB 走字典；总市值字典未携带显「—」）+ 五档 + 相关情报。
 * 纪律：
 * - 恒随全局 symbol store——切代码只换数据，布局不动；
 * - 数据面：切代码 REST 首刷 → subscribe('market', {symbol}) 推送防抖触发再刷 → 手动刷新兜底；
 *   WS 断连 → NtFreshness cut 显「断 · 最后时刻」（warn，非实时语义唯一承载，2026-10-21 评审撤除重复徽标），快照过期显琥珀「已过期」；
 * - 算不出的字段显「未知」（后端零值不猜），无源字段显「—」（数据纪律「没取到≠0」，禁虚构禁派生），
 *   无盘口时五档区显占位，逐笔/异动入口显「P1 待接入」。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getQuote, type QuoteView } from '@/api/market'
import { timeline, type IntelItem } from '@/api/intel'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTabs from '@/ui/NtTabs.vue'
import SymbolName from '@/display/SymbolName.vue'
import { statusOf, type StatusEntry } from '@/ui/status-map'
import { clock, compactAmount, messageOf, pctText, price, signed, toneOf } from '@/utils/format'
import { useSecurityStore } from '@/stores/security'
import { useSymbolStore } from '@/stores/symbol'
import { getWorkbench } from '@/workspace/inject'
import { consumePanelParams, onPanelParams } from '@/workspace/panel-bus'
import { isLive, onPush, subscribe, type WsPushMessage } from '@/ws/hub'
import { dataFrame } from '@/ws/data-frame'
import { quoteFlash, type FlashSample } from '@/utils/quote-flash'

const symbolStore = useSymbolStore()
const security = useSecurityStore()

/** 面板头的手输框：跟随全局代码；回车/失焦写回 store（source 记本面板）。 */
const draft = ref(symbolStore.code)
const quote = ref<QuoteView | null>(null)
const news = ref<IntelItem[]>([])
const newsReady = ref(false)
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
    const [q, intelRes] = await Promise.all([
      getQuote(sym),
      withNews ? timeline(sym).catch(() => null) : Promise.resolve(null),
    ])
    if (disposed || seq !== loadSeq || sym !== code.value) return
    // G1：快照差分打跳——同代码、前后两帧都有真值且现价变化才打标（切代码 prev=null 不打）。
    const prev = quote.value
    if (prev && q.found && prev.last > 0 && q.last > 0 && q.last !== prev.last) {
      quoteFlash.mark(sym, q.last > prev.last ? 'up' : 'down')
    }
    quote.value = q
    if (q.found && q.name) symbolStore.fillName(sym, q.name)
    if (intelRes) {
      news.value = intelRes.items.slice(0, 8)
      newsReady.value = true
    }
    if (!q.found) error.value = '还没有这只证券的行情，先确认代码与数据采集。'
  } catch (err) {
    if (disposed || seq !== loadSeq) return
    error.value = messageOf(err)
  } finally {
    if (seq === loadSeq) loading.value = false
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
    { label: '振幅', value: q.amplitude > 0 ? pctText(q.amplitude) : MISS_QUOTE },
    { label: '涨停价', value: q.noLimit ? '无' : num(q.limitUp, price) },
    { label: '跌停价', value: q.noLimit ? '无' : num(q.limitDown, price) },
  ]
})

/** 量额区块（§6.4）：总量/总额/换手出自 GetQuote。
 * 「—」两项：量比 GetQuote proto 未携带盘中口径（仅竞价截面有 auction_vol_ratio，口径不同不挪用）；
 * 52 周高/低需附录 B-2 端点补字段（未上）——显式缺失，禁虚构。 */
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
    { label: '换手', value: q.turnoverRate > 0 ? pctText(q.turnoverRate) : MISS_QUOTE },
    { label: '量比', value: MISS_FIELD },
    { label: '52周高/低', value: MISS_FIELD },
  ]
})

/** 估值区块（§6.4）：全部读 security 字典（stock_basic 口径），truthy 守卫让负 PE 照样渲染、0/null 显「—」。
 * 总市值字典未携带（stock_basic 无该列；不拿 total_share×现价派生——禁计算派生）→ 恒显「—」。 */
const valuationFields = computed<FieldItem[]>(() => {
  const b = basic.value
  return [
    { label: '流通市值', value: b?.circMv ? compactAmount(b.circMv) : MISS_FIELD },
    { label: 'PE(TTM)', value: b?.peTtm ? b.peTtm.toFixed(1) : MISS_FIELD },
    { label: 'PB', value: b?.pb ? b.pb.toFixed(2) : MISS_FIELD },
    { label: '总市值', value: MISS_FIELD },
  ]
})

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
        <p class="nt-note">恒随当前证券，身份块走 SymbolName · 图纸见 M12 附议。</p>
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
    <p v-if="error" class="nt-err">{{ error }}</p>

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
        <div v-if="quote.bid.length || quote.ask.length" class="ladder">
          <div class="ladder__col">
            <p v-for="(l, i) in [...quote.ask].reverse()" :key="`a${i}`" class="level down">
              <span class="side">卖{{ quote.ask.length - i }}</span>
              <b>{{ price(l.price) }}</b>
              <span class="vol">{{ compactAmount(l.volume) }}</span>
            </p>
          </div>
          <div class="ladder__sep" />
          <div class="ladder__col">
            <p v-for="(l, i) in quote.bid" :key="`b${i}`" class="level up">
              <span class="side">买{{ i + 1 }}</span>
              <b>{{ price(l.price) }}</b>
              <span class="vol">{{ compactAmount(l.volume) }}</span>
            </p>
          </div>
        </div>
        <p v-else class="nt-empty">当前数据源没有盘口（{{ quote.source === 'snap' ? '盘口缺失' : '快照不可用，按日线回退' }}）。</p>
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

      <section class="nt-panel">
        <NtTabs
          v-model="tab"
          :items="[
            { value: 'intel', label: '相关情报' },
            { value: 'flash', label: '快讯' },
          ]"
        />
        <div v-if="tab === 'intel'">
          <p v-if="!news.length" class="nt-empty">{{ newsReady ? '没有相关消息。' : '读取中……' }}</p>
          <ul v-else class="news">
            <li v-for="item in news" :key="item.id || item.time + item.title" @click="openIntel(item)">
              <b>{{ clock(item.time) }}</b>
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
  font-size: 12.5px;
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
  font-size: 11px;
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
  font-size: 12.5px;
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
  font-size: 11px;
  color: var(--nt-text-3);
}

.book,
.grid {
  margin-bottom: 10px;
}

.ladder__col {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.ladder__sep {
  border-top: 1px dashed var(--nt-border-1);
  margin: 6px 0;
}

.level {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin: 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.level .side {
  width: 3em;
  flex: none;
  font-size: 11px;
  color: var(--nt-text-3);
}

.level b {
  font-weight: 600;
}

.level .vol {
  margin-left: auto;
  color: var(--nt-text-2);
  font-size: 11.5px;
}

.pairs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 18px;
  margin: 0;
  font-size: 12px;
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
  font-size: 12px;
  cursor: pointer;
}

.news li:hover span {
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
