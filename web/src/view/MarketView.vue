<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { timeline, type IntelItem } from '@/api/intel'
import { getBars, getIndicators, type Bar, type MarketIndicator } from '@/api/market'
import { listFills, type Fill } from '@/api/trade'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtIndicatorChart, { type IndicatorPoint } from '@/viz/NtIndicatorChart.vue'
import NtKline, { type KlineLevel, type KlineMarker } from '@/viz/NtKline.vue'
import { PALETTE } from '@/theme/tokens'
import { clock, compactAmount, messageOf, num, pick, pnlClass, price } from '@/utils/format'
import { router } from '@/router'
import { useSymbolStore } from '@/stores/symbol'
import { consumePanelParams, onPanelParams } from '@/workspace/panel-bus'
import { getWorkbench } from '@/workspace/inject'
import { isLive, onPush, onResync, subscribe, type MarketSnapshotMeta, type WsPushMessage } from '@/ws/hub'
import { dataFrame } from '@/ws/data-frame'
import { quoteFlash, type FlashSample } from '@/utils/quote-flash'
import { createPageRefresh } from './page-refresh'

/** 面板内打开时参数由 panel-bus 传入（深链/信号页跳转），路由 query 作兼容兜底。 */
const panelParams = consumePanelParams('market')
const symbol = ref(String(panelParams?.symbol ?? router.currentRoute.value.query.symbol ?? ''))

/** 全局当前证券存根（视频设计指引 §8「切 symbol 不换布局」单一事实源）：
 *  本页是 market 发起方之一（手工输入/面板参数/路由 query），写入 source='market'；
 *  他源（QB/QL/信号票等）切的券经下方 store watch 回流，布局恒不动、只换数据。 */
const symbolStore = useSymbolStore()
const freq = ref('1d')
const account = ref('SIM')
const bars = ref<Bar[]>([])
const indicators = ref<MarketIndicator[]>([])
const news = ref<IntelItem[]>([])
const fills = ref<Fill[]>([])
const newsReady = ref(false)
const fillsReady = ref(false)
const error = ref('')
const loading = ref(false)
const loaded = ref(false)

const freqOptions = [
  { value: '1d', label: '日线' },
  { value: '1m', label: '1 分钟' },
]
const accountOptions = [
  { value: 'SIM', label: 'SIM' },
  { value: 'LIVE', label: 'LIVE' },
]

/** 最近一根 K 线；前一根收盘只用于同周期比较，不冒充交易所昨收或实时快照。 */
const quote = computed(() => {
  if (!bars.value.length) return null
  const last = bars.value[bars.value.length - 1]
  const prev = bars.value.length > 1 ? bars.value[bars.value.length - 2] : null
  const lastClose = last.close
  const preClose = prev?.close ?? null
  const change = lastClose != null && preClose ? lastClose - preClose : null
  const changePct = change != null && preClose ? (change / preClose) * 100 : null
  const amplitude = last.high != null && last.low != null && preClose ? ((last.high - last.low) / preClose) * 100 : null
  return {
    time: last.time,
    last: lastClose,
    change,
    changePct,
    open: last.open,
    high: last.high,
    low: last.low,
    amplitude,
    volume: last.volume,
    amount: last.amount ?? null,
  }
})

/** 现价跳变闪烁样本：quote-flash 单例推送，闪动窗口结束自动回落（300ms 闪 + 300ms 冷静）。 */
const flashSample = ref<FlashSample | null>(null)
const offFlash = quoteFlash.onChange((map) => { flashSample.value = map.get(symbol.value.trim()) ?? null })
const qFlashClass = computed(() => {
  const sample = flashSample.value
  if (!sample || !sample.flashing) return ''
  if (sample.direction === 'up') return 'q-flash--up'
  if (sample.direction === 'down') return 'q-flash--down'
  return ''
})

const markers = computed<KlineMarker[]>(() => {
  const fromNews: KlineMarker[] = news.value.map((item) => ({
    time: item.time,
    position: 'aboveBar',
    // 红涨绿跌：利好红、利空绿；无评分的消息不涂色，避免误判成利好。
    color: item.sentiment == null ? PALETTE.text3 : item.sentiment >= 0 ? PALETTE.up : PALETTE.down,
    shape: 'circle',
    text: (item.title || '消息').slice(0, 8),
  }))
  const fromFills: KlineMarker[] = fills.value
    .filter((item) => item.symbol === symbol.value.trim())
    .map((item) => ({
      time: item.time,
      position: item.side === 'sell' ? 'aboveBar' : 'belowBar',
      color: item.side === 'sell' ? PALETTE.warn : PALETTE.accent,
      shape: item.side === 'sell' ? 'arrowDown' : 'arrowUp',
      text: item.side === 'sell' ? '卖' : '买',
    }))
  return [...fromNews, ...fromFills]
})

const indicatorPoints = computed<IndicatorPoint[]>(() =>
  indicators.value
    .filter((point) => point.macd != null || point.rsi6 != null)
    .map((point) => ({
      time: String(point.time).slice(5, 16) || String(point.time),
      macd: point.macd,
      rsi6: point.rsi6,
    })),
)

/** 摊薄成本参考线：（买成交额 − 卖成交额）/ 净持仓股数；含已实现盈亏，净额 ≤0 或数据不全则不画。 */
const klineLevels = computed<KlineLevel[]>(() => {
  const code = symbol.value.trim()
  if (!code) return []
  let qty = 0
  let cost = 0
  for (const fill of fills.value) {
    if (fill.symbol !== code || fill.qty == null || fill.qty <= 0) continue
    const amount = fill.amount ?? (fill.price != null ? fill.price * fill.qty : null)
    if (amount == null) continue
    if (fill.side === 'buy') { qty += fill.qty; cost += amount } else { qty -= fill.qty; cost -= amount }
  }
  if (qty <= 0 || cost <= 0) return []
  return [{ price: cost / qty, color: PALETTE.warn, title: '摊薄成本', lineStyle: 'dashed' }]
})

/** 请求代际：旧的 load 结果不得覆盖新的（freq 切换/推送/手动刷新可能并发）。 */
let loadSeq = 0
let disposed = false
let contextVersion = 0
const refreshKey = () => JSON.stringify([symbol.value.trim(), freq.value, account.value, contextVersion])
const refreshes = createPageRefresh({ key: refreshKey, read: readPage, pushIntervalMs: 5000 })

/** 数据帧代际：全量重载即失效窗口内待合并的旧帧（REST 快照权威性高于增量帧）。 */
let frameEpoch = 0
function load() {
  frameEpoch += 1
  return refreshes.refresh()
}

/** NtKline 的暴露口：增量走 updateLast、复位走 refit，不再靠重取整组 bars 驱动。 */
interface KlineExposed {
  updateLast: (bar: Bar) => boolean
  refit: () => void
}
const klineRef = ref<KlineExposed | null>(null)
/** 盘中只追加不裁剪；超阈值（约 3 个交易日的 1m 数据）触发一次全量重载对齐。 */
const LIVE_CAP = 800
/** 增量通道的保守兜底：消息/成交/指标与长尾数据按期对账一次。 */
const RECONCILE_MS = 60_000
let reconcileTimer: ReturnType<typeof setInterval> | undefined

type LiveBarRow = Bar & { symbol?: string; freq?: string }

function readPushBar(raw: unknown): LiveBarRow | null {
  if (!raw || typeof raw !== 'object') return null
  const record = raw as Record<string, unknown>
  const time = pick<string>(record, 'time', 'barTime', 'bar_time', 'timestamp', 'startTime', 'start_time')
  if (!time) return null
  return {
    time,
    open: num(pick(record, 'open')),
    high: num(pick(record, 'high')),
    low: num(pick(record, 'low')),
    close: num(pick(record, 'close')),
    volume: num(pick(record, 'volume')),
    amount: num(pick(record, 'amount')),
    symbol: pick<string>(record, 'symbol', 'stockCode', 'stock_code', 'code'),
    freq: pick<string>(record, 'freq'),
  }
}

/** market 推送帧形态：{bar} 单根 / {bars:[]} 批量 / 顶层即 bar 字段；三者都归一。 */
function pushBarRows(data: Record<string, unknown>): LiveBarRow[] {
  const out: LiveBarRow[] = []
  const single = readPushBar(data.bar)
  if (single) out.push(single)
  if (Array.isArray(data.bars)) data.bars.forEach((raw) => { const row = readPushBar(raw); if (row) out.push(row) })
  if (!out.length && (data.open !== undefined || data.close !== undefined)) {
    const flat = readPushBar(data)
    if (flat) out.push(flat)
  }
  return out
}

function applyLiveBar(bar: Bar) {
  const rows = bars.value
  if (!rows.length) {
    void load()
    return
  }
  const last = rows[rows.length - 1]
  const prevClose = last.close
  const cur = Date.parse(String(bar.time))
  const prev = Date.parse(String(last.time))
  if (Number.isFinite(cur) && Number.isFinite(prev)) {
    if (cur < prev) return
    if (cur === prev) rows[rows.length - 1] = bar
    else rows.push(bar)
  } else if (String(bar.time) === String(last.time)) {
    rows[rows.length - 1] = bar
  } else {
    rows.push(bar)
  }
  if (rows.length > LIVE_CAP) {
    void load()
    return
  }
  klineRef.value?.updateLast(bar)
  // 现价跳变闪烁：方向取相对最后展示价（同根=tick 方向，新根首 tick=相对前根收）。
  if (prevClose != null && bar.close != null && bar.close !== prevClose) {
    quoteFlash.mark(symbol.value.trim(), bar.close > prevClose ? 'up' : 'down')
  }
}

/** bar 帧经数据帧调度器合批再喂图：同 key 后到覆盖先到，rAF 对齐且隐藏页自动降频。 */
function queueLiveBar(row: LiveBarRow, rowSymbol: string | undefined, rowFreq: string | undefined) {
  const epoch = frameEpoch
  const key = `market:kline:${rowSymbol ?? symbol.value.trim()}:${rowFreq ?? freq.value}:${String(row.time)}`
  dataFrame.schedule(key, () => {
    if (disposed || epoch !== frameEpoch) return
    applyLiveBar(row)
  })
}

/** 手动读取/切 symbol 或 freq：全量重载后显式复位视口；推送增量永不抢缩放。 */
async function reloadWithRefit(): Promise<boolean> {
  const ok = await load()
  if (ok) {
    await nextTick()
    klineRef.value?.refit()
  }
  return ok
}

async function readPage(key: string): Promise<boolean> {
  const code = symbol.value.trim()
  if (!code) {
    error.value = '请输入证券代码，例如 600519.SH'
    return false
  }
  const seq = ++loadSeq
  const freqAt = freq.value
  const accountAt = account.value
  loading.value = true
  loaded.value = true
  error.value = ''
  const [barRes, indRes, newsRes, fillRes] = await Promise.allSettled([
    getBars(code, freqAt),
    freqAt === '1d' ? getIndicators(code) : Promise.resolve([]),
    timeline(code),
    listFills(accountAt),
  ])
  if (disposed || key !== refreshKey() || seq !== loadSeq || code !== symbol.value.trim() ||
    freqAt !== freq.value || accountAt !== account.value) return false
  bars.value = barRes.status === 'fulfilled' ? barRes.value : []
  indicators.value = indRes.status === 'fulfilled' ? indRes.value : []
  news.value = newsRes.status === 'fulfilled' ? newsRes.value.items : []
  fills.value = fillRes.status === 'fulfilled' ? fillRes.value : []
  newsReady.value = newsRes.status === 'fulfilled'
  fillsReady.value = fillRes.status === 'fulfilled'
  if (barRes.status === 'rejected') error.value = messageOf(barRes.reason)
  else if (!bars.value.length) error.value = ''
  loading.value = false
  return barRes.status === 'fulfilled' && indRes.status === 'fulfilled'
}

function openIntel(item: IntelItem) {
  getWorkbench()?.openPanel('intel', { symbol: symbol.value.trim() || undefined, highlight: item.id || undefined })
}

watch(
  () => router.currentRoute.value.query.symbol,
  (value) => {
    if (typeof value === 'string' && value && value !== symbol.value) {
      symbolStore.setSymbol(value, 'market')
      symbol.value = value
      void reloadWithRefit()
    }
  },
)

watch([symbol, freq, account], (next, before) => {
  contextVersion += 1
  bars.value = []
  indicators.value = []
  news.value = []
  fills.value = []
  newsReady.value = false
  fillsReady.value = false
  loading.value = false
  loaded.value = false
  flashSample.value = null
  if (next[0] !== before[0] || next[1] !== before[1]) resubMarket()
  if (next[0] === before[0] && symbol.value.trim()) void reloadWithRefit()
}, { flush: 'sync' })

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined
let offResync: (() => void) | undefined
/** P0-3：market 订阅随当前图表收窄到代码+周期；无代码时保持全量（快照行仍可用）。 */
function resubMarket() {
  unsubscribe?.()
  const code = symbol.value.trim()
  unsubscribe = code ? subscribe('market', { symbol: code, freq: freq.value }) : subscribe('market')
}
/** 已挂载时 openPanel('market', {symbol}) 直接推过来。 */
const offPanelParams = onPanelParams('market', (params) => {
  const code = typeof params.symbol === 'string' ? params.symbol.trim() : ''
  if (!code) return
  symbolStore.setSymbol(code, 'market')
  symbol.value = code
  void reloadWithRefit()
})

/** 手工换代码：先写全局存根再全量重载并复位视口；下方 store watch 对 source='market' 短路，
 *  这里必须显式重载而不是指望 watch 回路。同值重读也走此口（存根同值短路、重载恒执行）。 */
function applyManualSymbol(): void {
  const code = symbol.value.trim()
  if (!code) return
  symbolStore.setSymbol(code, 'market')
  void reloadWithRefit()
}

/** 他源切券回流（QB/QL/信号票等）：同值与本页自写（source='market'）不回灌，布局恒不动。 */
watch(() => symbolStore.code, (next) => {
  if (!next || symbolStore.source === 'market') return
  if (next !== symbol.value.trim()) {
    symbol.value = next
    void reloadWithRefit()
  }
}, { flush: 'sync' })

/** 最近一轮快照批次信息（market 主题推的 SnapshotMeta），null = 还没收到过推送。 */
const snapshot = ref<MarketSnapshotMeta | null>(null)

const snapshotLine = computed(() => {
  const meta = snapshot.value
  if (!meta) return ''
  const asOf = meta.asOf || meta.as_of
  const parts: string[] = []
  parts.push(`快照 ${asOf ? clock(asOf) : '时间未返回'}`)
  if (meta.stale) parts.push('已过期')
  if (meta.source) parts.push(`源 ${meta.source}`)
  const count = Number(meta.count)
  if (Number.isFinite(count) && count > 0) parts.push(`${count} 只`)
  const latency = Number(meta.latencyMs ?? meta.latency_ms)
  if (Number.isFinite(latency) && latency > 0) parts.push(`采集 ${Math.round(latency)}ms`)
  const seq = Number(meta.seq)
  if (Number.isFinite(seq) && seq > 0) parts.push(`#${seq}`)
  return parts.join(' · ')
})

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'market') return
  if (!msg.data || typeof msg.data !== 'object') return
  const data = msg.data as Record<string, unknown>
  const rows = pushBarRows(data)
  // 快照批次帧（无 bar 行）：只刷新快照行，REST 对账交给对账定时器，不再整段重取。
  if (!rows.length) {
    snapshot.value = data as unknown as MarketSnapshotMeta
    return
  }
  const code = symbol.value.trim()
  if (!code) return
  rows.forEach((row) => {
    // bar 帧的 symbol/freq 可能在行内也可能在帧顶层；对不上当前图表的帧直接忽略。
    const rowSymbol = row.symbol ?? pick<string>(data, 'symbol', 'stockCode', 'stock_code', 'code')
    const rowFreq = row.freq ?? pick<string>(data, 'freq')
    if (rowSymbol != null && rowSymbol !== code) return
    if (rowFreq != null && rowFreq !== freq.value) return
    queueLiveBar(row, rowSymbol, rowFreq)
  })
}

function onVisible() { if (!document.hidden && symbol.value.trim()) void load() }

onMounted(() => {
  // 存根对齐：本页带参数则以本页为准回写；无参数且别处已选好证券则沿用全局当前券。
  if (symbol.value) symbolStore.setSymbol(symbol.value, 'market')
  else if (symbolStore.code) symbol.value = symbolStore.code
  if (symbol.value) void load()
  offResync = onResync(['market'], async () => {
    if (!symbol.value.trim()) return
    const key = refreshKey()
    const ready = await load()
    if (!disposed && key === refreshKey() && !ready) throw new Error('行情图表尚未恢复')
  })
  resubMarket()
  offPush = onPush(onPushMessage)
  document.addEventListener('visibilitychange', onVisible)
  // 增量通道的兜底对账：页面可见且连接在线时，周期全量重取一次对齐指标/消息/成交与长尾数据。
  reconcileTimer = setInterval(() => {
    if (disposed || document.hidden || !isLive() || !symbol.value.trim()) return
    void load()
  }, RECONCILE_MS)
})

onBeforeUnmount(() => {
  disposed = true
  contextVersion += 1
  refreshes.dispose()
  if (reconcileTimer !== undefined) clearInterval(reconcileTimer)
  offPush?.()
  unsubscribe?.()
  offPanelParams()
  offResync?.()
  offFlash()
  document.removeEventListener('visibilitychange', onVisible)
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">行情</h1>
        <p class="nt-note">K 线、均线、MACD / RSI，以及消息和成交标记。</p>
      </div>
    </header>
    <form class="nt-form" @submit.prevent="applyManualSymbol">
      <label class="grow">
        <span>代码</span>
        <NtInput v-model="symbol" placeholder="600519.SH" />
      </label>
      <NtSelect v-model="freq" label="周期" :options="freqOptions" />
      <NtSelect v-model="account" label="成交账户" :options="accountOptions" />
      <NtButton variant="primary" :loading="loading" @click="applyManualSymbol">读取</NtButton>
    </form>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-else-if="snapshotLine" class="nt-note snap-line">行情推送：{{ snapshotLine }}</p>
    <p v-else-if="loaded && !loading && !bars.length" class="nt-empty">
      没有这只股票的 K 线。先到数据源采集证券列表和日线。
      <button type="button" class="link" @click="getWorkbench()?.openPanel('datasource-health')">打开数据源</button>
    </p>

    <div v-if="quote" class="quote-strip">
      <span class="q-item">最近一根{{ freq === '1d' ? '日 K' : '1 分钟 K' }} · 收</span>
      <span class="q-last" :class="[pnlClass(quote.change), qFlashClass]">{{ price(quote.last) }}</span>
      <span class="q-chg" :class="pnlClass(quote.change)">
        较前根
        {{ quote.change != null ? (quote.change >= 0 ? '+' : '') + quote.change.toFixed(2) : '—' }}
        {{ quote.changePct != null ? (quote.changePct >= 0 ? '+' : '') + quote.changePct.toFixed(2) + '%' : '' }}
      </span>
      <span class="q-item">开 {{ price(quote.open) }}</span>
      <span class="q-item">高 {{ price(quote.high) }}</span>
      <span class="q-item">低 {{ price(quote.low) }}</span>
      <span class="q-item">本根振幅 {{ quote.amplitude != null ? quote.amplitude.toFixed(2) + '%' : '—' }}</span>
      <span class="q-item">量 {{ compactAmount(quote.volume) }} 股</span>
      <span class="q-item">额 {{ compactAmount(quote.amount) }} 元</span>
      <span class="q-item q-time">K 线时间 {{ clock(quote.time) }}</span>
    </div>

    <div class="nt-split">
      <aside class="nt-panel news">
        <h2 class="nt-panel__title">消息</h2>
        <p v-if="!news.length" class="nt-empty">{{ newsReady ? '本次查询没有相关消息。' : loaded ? '消息未返回，不能判断有无相关消息。' : '读取证券后查看相关消息。' }}</p>
        <ul v-else>
          <li v-for="item in news" :key="item.id || item.time + item.title" @click="openIntel(item)">
            <div class="news__head">
              <b :class="pnlClass(item.sentiment)">{{ clock(item.time) }}</b>
              <NtBadge v-if="(item.importance ?? 0) >= 4" tone="warn">重要</NtBadge>
              <span v-if="item.kind" class="news__kind">{{ item.kind }}</span>
            </div>
            <span>{{ item.title }}</span>
          </li>
        </ul>
      </aside>
      <section class="nt-panel">
        <div class="kline-frame">
          <NtKline
            ref="klineRef"
            :bars="bars"
            :indicators="indicators"
            :markers="markers"
            :levels="klineLevels"
            :watermark="symbol.trim()"
            :freq="freq"
            height="100%"
          />
        </div>
        <p class="legend">冰青 MA5 · 电蓝 MA10 · 琥珀 MA20 · 柱为成交量<template v-if="klineLevels.length"> · 琥珀虚线为摊薄成本</template></p>
        <p v-if="loaded && !loading && !fillsReady" class="nt-note">成交明细未返回，图上的买卖标记尚不完整。</p>
        <NtIndicatorChart v-if="indicatorPoints.length" :points="indicatorPoints" height="220px" />
        <p v-else class="nt-empty">指标未返回。</p>
      </section>
    </div>
  </div>
</template>

<style scoped>
.snap-line {
  margin: 0 0 10px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11.5px;
}

.quote-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  align-items: baseline;
  padding: 8px 12px;
  margin-bottom: 10px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12.5px;
}

.q-last {
  padding: 0 5px;
  margin: 0 -5px;
  border-radius: var(--nt-radius-s);
  font-size: 20px;
  font-weight: 700;
}

/* 现价跳变短脉冲：真实 bar 帧驱动，300ms 内淡出；减少动态档由全局 animation:none 接管。 */
.q-flash--up {
  animation: q-flash-up 0.3s ease-out;
}

.q-flash--down {
  animation: q-flash-down 0.3s ease-out;
}

@keyframes q-flash-up {
  0% { background: color-mix(in srgb, var(--nt-up) 32%, transparent); }
  100% { background: transparent; }
}

@keyframes q-flash-down {
  0% { background: color-mix(in srgb, var(--nt-down) 32%, transparent); }
  100% { background: transparent; }
}

.q-chg {
  font-weight: 600;
}

.q-item {
  color: var(--nt-text-2);
}

.q-time {
  margin-left: auto;
  color: var(--nt-text-3);
  font-size: 11px;
}

.news ul {
  list-style: none;
  margin: 10px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 180px;
  overflow: auto;
}

@container (min-width: 760px) {
  .news ul {
    max-height: 640px;
  }
}

.news li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12.5px;
  cursor: pointer;
}

.news li:hover span:last-child {
  color: var(--nt-text-1);
}

.news__head {
  display: flex;
  gap: 8px;
  align-items: baseline;
}

.news__head b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.news__kind {
  font-size: 11px;
  color: var(--nt-text-3);
}

.link {
  margin-left: 6px;
  padding: 0;
  color: var(--nt-accent);
  background: none;
  border: none;
  cursor: pointer;
  font: inherit;
}

.kline-frame {
  height: 260px;
}

@container (min-width: 760px) {
  .kline-frame {
    height: 420px;
  }
}

.legend {
  margin: 8px 0 0;
  color: var(--nt-text-3);
  font-size: 11.5px;
  line-height: 1.4;
}
</style>
