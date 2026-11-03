<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { timeline, type IntelItem } from '@/api/intel'
import { getBars, getIndicators, type Bar, type MarketIndicator } from '@/api/market'
import { listFills, type Fill } from '@/api/trade'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtKline, { type KlineLevel, type KlineMarker } from '@/viz/NtKline.vue'
import NtIndexStrip from '@/display/NtIndexStrip.vue'
import IntelRow from '@/display/IntelRow.vue'
import SymbolName from '@/display/SymbolName.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import { PALETTE } from '@/theme/tokens'
import { clock, compactAmount, messageOf, num, pick, pnlClass, price } from '@/utils/format'
import { normalizeCode } from '@/utils/symbol'
import { chartTime } from '@/utils/time'
import { router } from '@/router'
import { useSymbolStore } from '@/stores/symbol'
import { consumePanelParams, onPanelParams } from '@/workspace/panel-bus'
import { getWorkbench } from '@/workspace/inject'
import { isLive, onPush, onResync, subscribe, type MarketSnapshotMeta, type WsPushMessage } from '@/ws/hub'
import { dataFrame } from '@/ws/data-frame'
import { quoteFlash, type FlashSample } from '@/utils/quote-flash'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'
import { createPageRefresh } from './page-refresh'
import { mergeGapRows } from './market-backfill'
import { fillSideTone, aggregateKlineMarkers, withFillsCostLine } from './market-markers'

// W3-2 面板偏好持久化：键版勿改，旧档自然失效。
const PREFS_KEY = 'nt.prefs.view.market.v1'
const PREFS_ALLOW = { freq: ['1d', '1m'], account: ['SIM', 'LIVE'] } as const
// 读档须在首次订阅/拉数前完成（freq 直接进 resubMarket 与 getBars）。
const savedPrefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)

/** 面板内打开时参数由 panel-bus 传入（深链/信号页跳转，进站已归一），路由 query 作兼容兜底（需归一）。 */
const panelParams = consumePanelParams('market')
const symbol = ref(normalizeCode(String(panelParams?.symbol ?? router.currentRoute.value.query.symbol ?? '')))
/** 全局当前证券（§8 恒随）：本图写入时记 source=market；外部切换时换图，布局不动。 */
const symbolStore = useSymbolStore()
const freq = ref(savedPrefs.freq ?? '1d')
const account = ref(savedPrefs.account ?? 'SIM')
const bars = ref<Bar[]>([])
const indicators = ref<MarketIndicator[]>([])
const news = ref<IntelItem[]>([])
const fills = ref<Fill[]>([])
const newsReady = ref(false)
const fillsReady = ref(false)
const indicatorsReady = ref(false)
const error = ref('')
const loading = ref(false)
const loaded = ref(false)
/** 框选测量开关：换 symbol/周期/账户一律复位，避免框住旧数据。 */
const measuring = ref(false)

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

/** 当前票成交：legend 门槛与 markers 同口径——账户他票成交不得点亮本图的买卖图例。 */
const symbolFills = computed(() => fills.value.filter((item) => item.symbol === symbol.value.trim()))

const markers = computed<KlineMarker[]>(() => {
  const fromNews: KlineMarker[] = news.value.map((item) => ({
    time: item.time,
    position: 'aboveBar',
    // 红涨绿跌：利好红、利空绿；无评分的消息不涂色，避免误判成利好。
    color: item.sentiment == null ? PALETTE.text3 : item.sentiment >= 0 ? PALETTE.up : PALETTE.down,
    shape: 'circle',
    text: (item.title || '消息').slice(0, 8),
  }))
  const fromFills: KlineMarker[] = symbolFills.value.map((item) => ({
    time: item.time,
    position: item.side === 'sell' ? 'aboveBar' : 'belowBar',
    // 红买绿卖（双色制定案，单源见 view/market-markers.ts）：up/down 同全站红涨绿跌，
    // 色觉硬门禁管辖内；方向未知走中性 text3 且不借买/卖箭头——bar 下圆点与消息圈（bar 上）错位自明。
    color: PALETTE[fillSideTone(item.side)],
    shape: item.side === 'sell' ? 'arrowDown' : item.side === 'buy' ? 'arrowUp' : 'circle',
    text: item.side === 'sell' ? '卖' : item.side === 'buy' ? '买' : '·',
  }))
  /* W6b 接线：成交密集时按时间桶采样抽稀（每桶每 side 最早一条）+ 消息不动（不参与方向采样）。 */
  return aggregateKlineMarkers([...fromNews, ...fromFills])
})

/** 副图已迁入 NtKline 的 v5 pane；这里只保留"有没有有效指标"的判定（全空行不算有）。 */
const hasSubIndicators = computed(() =>
  indicators.value.some((point) => point.macd != null || point.dif != null || point.dea != null || point.rsi6 != null),
)

/** 摊薄成本线（W6b 收编 cost 槽）：买卖成交额差 ÷ 净持仓股数；净额≤0 或数据不全不交成本线。
 *  levels 维度照走 cost 槽语义（成本入 withCostLine 首），同价关键价被成本接管。 */
const fillsCostOf = computed(() => withFillsCostLine([], fills.value, symbol.value.trim()))
const klineLevels = computed<KlineLevel[]>(() => fillsCostOf.value.levels)
const klineCost = computed(() => fillsCostOf.value.cost)

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

/**
 * P2-9 重连缺口回填：断线恢复后先只补缺口窗口 [末根时刻, 最新]，而非整段重载（K线级回填）。
 * 返回 false 交给整载兜底的情形：无基准行 / 回填够不到末根（缺口宽于 LIVE_CAP）/ 合并后超 LIVE_CAP / 并发换图。
 * 并发整载已接管（frameEpoch 变）时跟随 load() 的真实结果，不提前向 hub ack。
 * 合并期间被实时帧（待用 bar 管线，今日已产未路由见 M01 §7.4）抢先越过的行被 updateLast 忽略，60s 对账自愈。
 * 指标/消息/成交不随回填刷新，交给对账定时器（本就负责长尾对齐）。
 */
async function backfillGap(): Promise<boolean> {
  const code = symbol.value.trim()
  const tail = bars.value.length ? bars.value[bars.value.length - 1] : null
  if (!code || !tail?.time) return false
  const epoch = frameEpoch
  const freqAt = freq.value
  // 1d 末根可能被（待用 bar 管线）分钟帧写成 RFC3339；截到上海日历日，避免 start 把当日日线行排除出窗口。
  const tailTime = String(tail.time)
  const tailDay = chartTime(tailTime, '1d')
  const startAt = freqAt === '1d' && typeof tailDay === 'string' && tailDay ? tailDay : tailTime
  let fetched: Bar[]
  try {
    fetched = await getBars(code, freqAt, { start: startAt, limit: LIVE_CAP })
  } catch (error) {
    console.warn('[market] 缺口回填窗口请求失败，转整载兜底', error)
    return false
  }
  if (disposed) return true
  if (epoch !== frameEpoch) return load()
  if (code !== symbol.value.trim() || freqAt !== freq.value) return false
  const merged = mergeGapRows(bars.value, fetched, LIVE_CAP, freqAt === '1d' ? 'day' : 'ms')
  if (!merged.covered || merged.overflow) return false
  for (const bar of merged.applied) klineRef.value?.updateLast(bar)
  return true
}

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
  indicatorsReady.value = indRes.status === 'fulfilled'
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
    if (typeof value !== 'string' || !value) return
    // 与 setup 初始化/onPanelParams/手工提交同归一：裸码或小写后缀不得裸赋——
    // WS 订阅与 onPushMessage 精确匹配都按归一码，此路裸赋会让实时 bar 帧静默漏流（REST 因后端归一无感）。
    const code = normalizeCode(value)
    if (!code || code === symbol.value) return
    symbolStore.setSymbol(code, 'market')
    symbol.value = code
    void reloadWithRefit()
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
  indicatorsReady.value = false
  loading.value = false
  loaded.value = false
  flashSample.value = null
  measuring.value = false
  if (next[0] !== before[0] || next[1] !== before[1]) resubMarket()
  if (next[0] === before[0] && symbol.value.trim()) void reloadWithRefit()
}, { flush: 'sync' })

// 偏好整档持久化：周期与成交标记读取范围为浏览偏好（非执行账本，安全闸不涉及）。
watch([freq, account], () => {
  savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { freq: freq.value, account: account.value })
})

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
  // 归一兜底：总线已归一，直连赋值保证 REST/WS 订阅都用通达信形态（裸码 WS 精确匹配会漏流）。
  const code = typeof params.symbol === 'string' ? normalizeCode(params.symbol) : ''
  if (!code) return
  symbolStore.setSymbol(code, 'market')
  symbol.value = code
  void reloadWithRefit()
})

/** 手工表单提交（含「读取」钮）：换图的同时把当前证券写进全局 store；手输裸码也归一后上链。 */
function applyManualSymbol() {
  const next = normalizeCode(symbol.value)
  if (next && next !== symbol.value.trim()) symbol.value = next
  symbolStore.setSymbol(next, 'market')
  void reloadWithRefit()
}

/** 外部面板（行情侧栏/下单条/情报等）切了当前证券 → 本图跟随换券，布局不动。 */
watch(
  () => symbolStore.code,
  (next) => {
    if (!next || symbolStore.source === 'market') return
    if (next !== symbol.value.trim()) symbol.value = next
    void reloadWithRefit()
  },
  { flush: 'sync' },
)

/** 最近一轮快照批次信息（market 主题推的 SnapshotMeta），null = 还没收到过推送。 */
const snapshot = ref<MarketSnapshotMeta | null>(null)

/** 面板头徽注（§7.2-2/§7.3）：时间证据优先取快照帧 asOf，退到末根 bar 时刻；
 *  均无则传 null 由组件显「时点未知」（数据纪律：不拿 0s/当前时刻冒充）。 */
const freshAt = computed<number | null>(() => {
  const asOf = snapshot.value ? snapshot.value.asOf || snapshot.value.as_of : undefined
  if (asOf) {
    const t = Date.parse(String(asOf))
    if (Number.isFinite(t)) return t
  }
  const tail = bars.value[bars.value.length - 1]
  if (tail?.time) {
    const t = Date.parse(String(tail.time))
    if (Number.isFinite(t)) return t
  }
  return null
})
/** 链路断（健康/推送/同步任一未就绪）显「断 · 最后 HH:mm:ss」，优先于年龄分支。 */
const freshCut = computed(() => !isLive())
/** 分钟线推送节拍约一根一分钟（宽限 2 分钟）；日线一根一天（宽限 30h）。 */
const freshStaleMs = computed(() => (freq.value === '1d' ? 30 * 3600 * 1000 : 120 * 1000))

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
  // 初始与全局 store 对账：本图带参进场则写入；空手进场且 store 有票则跟随。
  if (symbol.value) symbolStore.setSymbol(symbol.value, 'market')
  else if (symbolStore.code) symbol.value = symbolStore.code
  if (symbol.value) void load()
  offResync = onResync(['market'], async () => {
    if (!symbol.value.trim()) return
    const key = refreshKey()
    // P2-9：先只补缺口窗口（补不动——过宽/无基准/换图/请求失败——再整载），失败仍抛出让 hub 保持 required。
    const ready = (await backfillGap()) || (await load())
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
      <NtFreshness label="行情" :at="freshAt" :cut="freshCut" :stale-after-ms="freshStaleMs" />
    </header>
    <NtIndexStrip />
    <form class="nt-form" @submit.prevent="applyManualSymbol">
      <label class="grow">
        <span>代码</span>
        <NtInput v-model="symbol" placeholder="600519.SH" />
      </label>
      <NtSelect v-model="freq" label="周期" :options="freqOptions" />
      <NtSelect v-model="account" label="成交账户" :options="accountOptions" />
      <NtButton variant="primary" :loading="loading" @click="applyManualSymbol">读取</NtButton>
      <!-- QD 借鉴（G8）：行情页留下单直达入口，代码随行预填 trade-bar（panel-bus 同款机制）。 -->
      <NtButton :disabled="!symbol.trim()" @click="getWorkbench()?.openPanel('trade-bar', { symbol: symbol.trim() })">去下单</NtButton>
      <NtButton
        :variant="measuring ? 'primary' : 'ghost'"
        :disabled="!bars.length"
        title="在图上按住拖动框选区间，统计涨跌与振幅；Esc 退出"
        @click="measuring = !measuring"
      >
        框选测量
      </NtButton>
    </form>
    <NtErrorGuide v-if="error" :error="error" />
    <p v-else-if="snapshotLine" class="nt-note snap-line">行情推送：{{ snapshotLine }}</p>
    <p v-else-if="loaded && !loading && !bars.length" class="nt-empty">
      没有这只股票的 K 线。先到数据源采集证券列表和日线。
      <button type="button" class="link" @click="getWorkbench()?.openPanel('datasource-health')">打开数据源</button>
    </p>

    <div v-if="quote" class="quote-strip">
      <SymbolName :code="symbol.trim()" mode="stacked" />
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
          <!-- W0-3b/FE-I4：消息行=IntelRow 单一事实组件（时点/来源/重要度/kind/原文外链唯一出处），行点击跳情报面板 -->
          <IntelRow v-for="item in news" :key="item.id || item.time + item.title" :item="item" clickable @open="openIntel" />
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
            :cost="klineCost"
            :watermark="symbol.trim()"
            :symbol="symbol.trim()"
            :freq="freq"
            :measure="measuring"
            height="100%"
          />
        </div>
        <p v-if="measuring && bars.length" class="nt-note">测量模式：在图上按住拖动画框，统计区间涨跌/振幅；Esc 或再次点按钮退出。</p>
        <p class="legend">冰青 MA5 · 电蓝 MA10 · 琥珀 MA20 · 柱为成交量<template v-if="symbolFills.length"> · 红↑=买 · 绿↓=卖</template><template v-if="freq === '1m'"> · 底色=集合竞价时段</template><template v-if="symbol.trim() && bars.length"> · 琥珀描边=涨/跌停封板（板块推断）</template><template v-if="klineCost"> · 冰青实线为摊薄成本（成本与 MA5 同冰青色族，实线恒横，MA5 为折线残影）</template><template v-if="hasSubIndicators"> · 副图 MACD 柱/电蓝 DIF/琥珀 DEA、冰青 RSI6（虚线 80/20）</template></p>
        <p v-if="loaded && !loading && !fillsReady" class="nt-note">成交明细未返回，图上的买卖标记尚不完整。</p>
        <p v-if="freq === '1d' && loaded && !loading && !indicatorsReady" class="nt-empty">指标未返回。</p>
        <p v-else-if="freq === '1d' && loaded && !loading && !hasSubIndicators" class="nt-empty">副图指标（MACD/RSI）未返回。</p>
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

/* W0-3b/FE-I4：行级样式（cursor/head/kind）已随 IntelRow 组件收口；容器级 .news ul（高度/滚动/间距）保留，
   hover 提亮标题沿用原交互（组件根 li 带父级 scope 属性，.news li 仍命中）。 */
.news li:hover :deep(.title) {
  color: var(--nt-text-1);
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
