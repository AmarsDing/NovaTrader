<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { timeline, type IntelItem } from '@/api/intel'
import { getBars, getIndicators, type Bar, type MarketIndicator } from '@/api/market'
import { listFills, type Fill } from '@/api/trade'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtIndicatorChart, { type IndicatorPoint } from '@/viz/NtIndicatorChart.vue'
import NtKline, { type KlineMarker } from '@/viz/NtKline.vue'
import { PALETTE } from '@/theme/tokens'
import { clock, messageOf, pnlClass } from '@/utils/format'
import { consumePanelParams } from '@/workspace/panel-bus'

const route = useRoute()
/** 面板内打开时参数由 panel-bus 传入（深链/信号页跳转），路由 query 作兼容兜底。 */
const panelParams = consumePanelParams('market')
const symbol = ref(String(panelParams?.symbol ?? route.query.symbol ?? ''))
const freq = ref('1d')
const account = ref('SIM')
const bars = ref<Bar[]>([])
const indicators = ref<MarketIndicator[]>([])
const news = ref<IntelItem[]>([])
const fills = ref<Fill[]>([])
const error = ref('')
const loading = ref(false)

const freqOptions = [
  { value: '1d', label: '日线' },
  { value: '1m', label: '1 分钟' },
]
const accountOptions = [
  { value: 'SIM', label: 'SIM' },
  { value: 'LIVE', label: 'LIVE' },
]

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
  else if (!bars.value.length) error.value = '没有 K 线'
  loading.value = false
}

watch(
  () => route.query.symbol,
  (value) => {
    if (typeof value === 'string' && value && value !== symbol.value) {
      symbol.value = value
      load()
    }
  },
)

onMounted(() => {
  if (symbol.value) load()
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
    <div class="nt-split">
      <aside class="nt-panel news">
        <h2 class="nt-panel__title">消息</h2>
        <p v-if="!news.length" class="nt-empty">这条时间线上没有消息。</p>
        <ul v-else>
          <li v-for="item in news" :key="item.id || item.time + item.title">
            <b :class="pnlClass(item.sentiment)">{{ clock(item.time) }}</b>
            <span>{{ item.title }}</span>
          </li>
        </ul>
      </aside>
      <section class="nt-panel">
        <NtKline :bars="bars" :indicators="indicators" :markers="markers" :freq="freq" />
        <p class="legend">青 MA5 · 紫 MA10 · 橙 MA20 · 柱为成交量</p>
        <NtIndicatorChart v-if="indicatorPoints.length" :points="indicatorPoints" height="220px" />
        <p v-else class="nt-empty">指标未返回。</p>
      </section>
    </div>
  </div>
</template>

<style scoped>
.news ul {
  list-style: none;
  margin: 10px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 640px;
  overflow: auto;
}

.news li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12.5px;
}

.news li b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.legend {
  margin: 8px 0;
  color: var(--nt-text-3);
  font-size: 11.5px;
}
</style>
