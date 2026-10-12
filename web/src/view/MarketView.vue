<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { timeline, type IntelItem } from '@/api/intel'
import { getBars, getIndicators, type Bar, type MarketIndicator } from '@/api/market'
import { listFills, type Fill } from '@/api/trade'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtIndicatorChart, { type IndicatorPoint } from '@/viz/NtIndicatorChart.vue'
import NtKline, { type KlineMarker } from '@/viz/NtKline.vue'
import { PALETTE } from '@/theme/tokens'
import { clock, compactAmount, messageOf, pnlClass, price } from '@/utils/format'
import { router } from '@/router'
import { consumePanelParams } from '@/workspace/panel-bus'
import { getWorkbench } from '@/workspace/inject'
import { onPush, subscribe, type MarketSnapshotMeta, type WsPushMessage } from '@/ws/hub'

/** 面板内打开时参数由 panel-bus 传入（深链/信号页跳转），路由 query 作兼容兜底。 */
const panelParams = consumePanelParams('market')
const symbol = ref(String(panelParams?.symbol ?? router.currentRoute.value.query.symbol ?? ''))
const freq = ref('1d')
const account = ref('SIM')
const bars = ref<Bar[]>([])
const indicators = ref<MarketIndicator[]>([])
const news = ref<IntelItem[]>([])
const fills = ref<Fill[]>([])
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

/** 报价条：最新一根 bar + 前一根作参照；日线时展示开高低/振幅/量/额。 */
const quote = computed(() => {
  if (!bars.value.length) return null
  const last = bars.value[bars.value.length - 1]
  const prev = bars.value.length > 1 ? bars.value[bars.value.length - 2] : null
  const lastClose = last.close
  const preClose = prev?.close ?? last.open
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

const markers = computed<KlineMarker[]>(() => {
  const fromNews: KlineMarker[] = news.value.map((item) => ({
    time: item.time,
    position: 'aboveBar',
    // 红涨绿跌：利好红、利空绿
    color: (item.sentiment ?? 0) >= 0 ? PALETTE.up : PALETTE.down,
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

async function load() {
  const code = symbol.value.trim()
  if (!code) {
    error.value = '请输入证券代码，例如 600519.SH'
    return
  }
  loading.value = true
  loaded.value = true
  error.value = ''
  const [barRes, indRes, newsRes, fillRes] = await Promise.allSettled([
    getBars(code, freq.value),
    freq.value === '1d' ? getIndicators(code) : Promise.resolve([]),
    timeline(code),
    listFills(account.value),
  ])
  bars.value = barRes.status === 'fulfilled' ? barRes.value : []
  indicators.value = indRes.status === 'fulfilled' ? indRes.value : []
  news.value = newsRes.status === 'fulfilled' ? newsRes.value.items : []
  fills.value = fillRes.status === 'fulfilled' ? fillRes.value : []
  if (barRes.status === 'rejected') error.value = messageOf(barRes.reason)
  else if (!bars.value.length) error.value = ''
  loading.value = false
}

function openIntel(item: IntelItem) {
  getWorkbench()?.openPanel('intel', { symbol: symbol.value.trim() || undefined, highlight: item.id || undefined })
}

watch(
  () => router.currentRoute.value.query.symbol,
  (value) => {
    if (typeof value === 'string' && value && value !== symbol.value) {
      symbol.value = value
      void load()
    }
  },
)

watch(freq, () => {
  if (symbol.value.trim()) void load()
})

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined
let lastPush = 0

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
  if (msg.data && typeof msg.data === 'object') snapshot.value = msg.data as MarketSnapshotMeta
  if (!symbol.value.trim()) return
  const now = Date.now()
  if (now - lastPush < 5000) return
  lastPush = now
  void load()
}

onMounted(() => {
  if (symbol.value) load()
  unsubscribe = subscribe('market')
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
        <h1 class="nt-title">行情</h1>
        <p class="nt-note">K 线、均线、MACD / RSI，以及消息和成交标记。</p>
      </div>
    </header>
    <form class="nt-form" @submit.prevent="load">
      <label class="grow">
        <span>代码</span>
        <NtInput v-model="symbol" placeholder="600519.SH" />
      </label>
      <NtSelect v-model="freq" label="周期" :options="freqOptions" />
      <NtSelect v-model="account" label="成交账户" :options="accountOptions" />
      <NtButton variant="primary" :loading="loading" @click="load">读取</NtButton>
    </form>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-else-if="snapshotLine" class="nt-note snap-line">行情推送：{{ snapshotLine }}</p>
    <p v-else-if="loaded && !loading && !bars.length" class="nt-empty">
      没有这只股票的 K 线。先到数据源采集证券列表和日线。
      <button type="button" class="link" @click="getWorkbench()?.openPanel('datasource-health')">打开数据源</button>
    </p>

    <div v-if="quote" class="quote-strip">
      <span class="q-last" :class="pnlClass(quote.change)">{{ price(quote.last) }}</span>
      <span class="q-chg" :class="pnlClass(quote.change)">
        {{ quote.change != null ? (quote.change >= 0 ? '+' : '') + quote.change.toFixed(2) : '—' }}
        {{ quote.changePct != null ? (quote.changePct >= 0 ? '+' : '') + quote.changePct.toFixed(2) + '%' : '' }}
      </span>
      <span class="q-item">开 {{ price(quote.open) }}</span>
      <span class="q-item">高 {{ price(quote.high) }}</span>
      <span class="q-item">低 {{ price(quote.low) }}</span>
      <span class="q-item">振幅 {{ quote.amplitude != null ? quote.amplitude.toFixed(2) + '%' : '—' }}</span>
      <span class="q-item">量 {{ compactAmount(quote.volume) }}</span>
      <span class="q-item">额 {{ compactAmount(quote.amount) }}</span>
      <span class="q-item q-time">{{ clock(quote.time) }}</span>
    </div>

    <div class="nt-split">
      <aside class="nt-panel news">
        <h2 class="nt-panel__title">消息</h2>
        <p v-if="!news.length" class="nt-empty">这条时间线上没有消息。</p>
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
          <NtKline :bars="bars" :indicators="indicators" :markers="markers" :freq="freq" height="100%" />
        </div>
        <p class="legend">青 MA5 · 紫 MA10 · 橙 MA20 · 柱为成交量</p>
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
  font-size: 20px;
  font-weight: 700;
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
