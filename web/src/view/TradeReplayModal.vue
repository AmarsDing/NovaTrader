<script setup lang="ts">
/**
 * 交易回放弹窗（TSP TradeKlineModal / useListNav 移植，方案 B1 §4.1）：
 * - 头部：证券标识（不可点） + 方向徽章 + NavPager（◀ n/total ▶，边界给提示不静默）；
 * - 摘要行：时间 · 价格 · 数量 · 金额 · 盈亏（null → —） · 理由（全文入 title）；
 * - 图区：日 K 窗口（被点交易日 ±45/20），markers = 同窗同标的全部成交（同日同向聚合 ×N），
 *   levels = 被点成交价虚线；四态 loading/missing/empty/ready（缺态不编 0 K 线）；
 * - 键盘：Esc 由 NtModal 关（useOverlay 栈顶派发），←/→ 沿同序导航链切笔，切笔不关窗；
 * - 窗口缓存：新笔窗口被已加载窗口覆盖时复用，不重复拉取；在途请求按序列号作废旧结果。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getBars, type Bar } from '@/api/market'
import type { BacktestTrade } from '@/api/types/backtest'
import NtButton from '@/ui/NtButton.vue'
import NtEmpty from '@/ui/NtEmpty.vue'
import NtModal from '@/ui/NtModal.vue'
import SymbolName from '@/display/SymbolName.vue'
import NtKline, { type KlineLevel, type KlineMarker } from '@/viz/NtKline.vue'
import { PALETTE } from '@/theme/tokens'
import { amount, clock, messageOf, price, signed } from '@/utils/format'
import { formatDate } from '@/utils/time'
import { buildTradeNavItems, navStep, tradeCostLines, tradeMarkers, tradeWindow } from './backtest-replay'

const props = withDefaults(
  defineProps<{
    open: boolean
    /** 同序导航链（交易明细表默认序）。 */
    trades: BacktestTrade[]
    /** 打开时定位的链位。 */
    index?: number
  }>(),
  { index: 0 },
)

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'update:index', value: number): void
}>()

const navItems = computed(() => buildTradeNavItems(props.trades))
const cursor = ref(0)
const current = computed<BacktestTrade | null>(() => navItems.value[cursor.value] ?? null)

const bars = ref<Bar[]>([])
/** 四态 + badtime：成交时间不可解析是**成交数据**问题，不得误报成「窗口内无 K 线」（评审 P1-1）。 */
const state = ref<'idle' | 'loading' | 'ready' | 'missing' | 'empty' | 'badtime'>('idle')
const errorText = ref('')
const boundary = ref('')
let boundaryTimer: ReturnType<typeof setTimeout> | null = null

const colors = computed(() => ({ up: PALETTE.up, down: PALETTE.down, neutral: PALETTE.text3 }))
const win = computed(() => (current.value ? tradeWindow(formatDate(current.value.time)) : null))
const markers = computed<KlineMarker[]>(() => {
  const c = current.value
  const w = win.value
  if (!c || !w) return []
  return tradeMarkers(props.trades, { symbol: c.symbol, start: w.start, end: w.end, colors: colors.value })
})
const levels = computed<KlineLevel[]>(() => (current.value ? tradeCostLines(current.value, colors.value) : []))

const sideLabel = computed(() => {
  const side = current.value?.side
  return side === 'buy' ? '买' : side === 'sell' ? '卖' : '方向未知'
})
const sideClass = computed(() => {
  const side = current.value?.side
  return side === 'buy' ? 'up' : side === 'sell' ? 'down' : ''
})

/* ---------- 取数（窗口缓存 + 在途作废） ---------- */

let cache: { symbol: string; start: string; end: string; rows: Bar[] } | null = null
let inflightKey = ''
let loadSeq = 0

function show(rows: Bar[]) {
  bars.value = rows
  state.value = rows.length ? 'ready' : 'empty'
}

async function syncBars() {
  const c = current.value
  if (!c) {
    bars.value = []
    state.value = 'empty'
    return
  }
  const w = win.value
  if (!w) {
    // 时间不可解析：归因到成交数据本身，不冒充「行情未返回/窗口无 K 线」（评审 P1-1）。
    bars.value = []
    state.value = 'badtime'
    return
  }
  if (cache && cache.symbol === c.symbol && cache.start <= w.start && cache.end >= w.end) {
    show(cache.rows)
    return
  }
  const key = `${c.symbol}|${w.start}|${w.end}`
  if (inflightKey === key) return
  inflightKey = key
  const seq = ++loadSeq
  // 新窗口在途：清旧图进加载态——不拿上一笔（可能是别只票）的旧 K 线冒充本笔现场。
  bars.value = []
  state.value = 'loading'
  try {
    const rows = await getBars(c.symbol, '1d', { start: w.start, end: w.end, limit: 400 })
    if (seq !== loadSeq) return
    cache = { symbol: c.symbol, start: w.start, end: w.end, rows }
    show(rows)
  } catch (err) {
    if (seq !== loadSeq) return
    bars.value = []
    state.value = 'missing'
    errorText.value = messageOf(err)
  } finally {
    if (inflightKey === key) inflightKey = ''
  }
}

/* ---------- 导航 ---------- */

function flashBoundary(text: string) {
  boundary.value = text
  if (boundaryTimer) clearTimeout(boundaryTimer)
  boundaryTimer = setTimeout(() => {
    boundary.value = ''
  }, 1600)
}

function step(delta: number) {
  const next = navStep(cursor.value, delta, navItems.value.length)
  if (next == null) {
    flashBoundary(delta < 0 ? '已是第一笔' : '已是最后一笔')
    return
  }
  cursor.value = next
  emit('update:index', next)
}

function onWindowKey(event: KeyboardEvent) {
  if (!props.open) return
  if (event.key === 'ArrowLeft') {
    event.preventDefault()
    step(-1)
  } else if (event.key === 'ArrowRight') {
    event.preventDefault()
    step(1)
  }
}

onMounted(() => window.addEventListener('keydown', onWindowKey))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onWindowKey)
  if (boundaryTimer) clearTimeout(boundaryTimer)
})

watch(
  () => props.open,
  (open) => {
    if (!open) {
      // 作废在途：关窗后到达的结果不得回写。
      loadSeq += 1
      inflightKey = ''
      return
    }
    boundary.value = ''
    const max = Math.max(0, navItems.value.length - 1)
    cursor.value = Math.min(Math.max(0, props.index), max)
    void syncBars()
  },
  { immediate: true },
)

watch(cursor, () => {
  boundary.value = ''
  void syncBars()
})

defineExpose({ step })
</script>

<template>
  <NtModal :open="open" width="920px" title="交易回放" @update:open="emit('update:open', $event)">
    <div class="rp">
      <header class="rp__head">
        <div class="rp__id">
          <SymbolName v-if="current" :code="current.symbol" mode="inline" :clickable="false" />
          <span class="rp__side" :class="sideClass">{{ sideLabel }}</span>
        </div>
        <div class="rp__nav">
          <span v-if="boundary" class="rp__boundary">{{ boundary }}</span>
          <NtButton size="sm" variant="quiet" aria-label="上一笔" @click="step(-1)">◀ 上一笔</NtButton>
          <span class="rp__counter" aria-live="polite">{{ cursor + 1 }} / {{ navItems.length }}</span>
          <NtButton size="sm" variant="quiet" aria-label="下一笔" @click="step(1)">下一笔 ▶</NtButton>
        </div>
      </header>

      <p v-if="current" class="rp__summary">
        <span>时间 <em>{{ clock(current.time) }}</em></span>
        <span>价格 <em>{{ price(current.price) }}</em></span>
        <span>数量 <em>{{ amount(current.quantity) }}</em></span>
        <span>金额 <em>{{ amount(current.amount) }}</em></span>
        <span>盈亏 <em :class="sideClass">{{ signed(current.pnl) }}</em></span>
        <span v-if="current.reason" class="rp__reason" :title="current.reason">理由 <em>{{ current.reason }}</em></span>
      </p>

      <div class="rp__chart">
        <NtKline
          v-if="state === 'ready'"
          :bars="bars"
          :markers="markers"
          :levels="levels"
          :watermark="current ? current.symbol : ''"
          :symbol="current ? current.symbol : ''"
          freq="1d"
          height="360px"
        />
        <NtEmpty v-else-if="state === 'badtime'" state="missing" title="成交时间不可解析" hint="该笔成交的时间字段无法解析，无法定位 K 线窗口还原现场" />
        <NtEmpty v-else-if="state === 'missing'" state="missing" title="行情未返回" :hint="errorText || 'K 线读口未返回，无法还原交易现场'" />
        <NtEmpty v-else-if="state === 'empty'" state="empty" title="窗口内无 K 线" hint="该交易日前后没有日线数据，可能未采集或停牌" />
        <NtEmpty v-else state="loading" title="正在读取 K 线…" />
      </div>
    </div>
  </NtModal>
</template>

<style scoped>
.rp {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.rp__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.rp__id {
  display: inline-flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}

.rp__side {
  font-size: var(--nt-fs-caption);
  font-weight: 600;
  padding: 1px 6px;
  border-radius: var(--nt-radius-s);
  color: var(--nt-text-2);
  background: var(--nt-bg-3);
}

.rp__side.up {
  color: var(--nt-up);
}

.rp__side.down {
  color: var(--nt-down);
}

.rp__nav {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.rp__boundary {
  font-size: var(--nt-fs-caption);
  color: var(--nt-warn);
}

.rp__counter {
  min-width: 52px;
  text-align: center;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.rp__summary {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
  margin: 0;
  padding: 6px 10px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
  background: var(--nt-bg-2);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}

.rp__summary em {
  font-style: normal;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-1);
}

.rp__summary em.up {
  color: var(--nt-up);
}

.rp__summary em.down {
  color: var(--nt-down);
}

.rp__reason {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 图区走数据背板（数据密集区近不透明，玻璃模糊只留壳层）。 */
.rp__chart {
  padding: 4px;
  background: var(--nt-data-bg);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}
</style>
