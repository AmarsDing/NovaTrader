<script setup lang="ts">
/**
 * 信号中枢：全部信号的卡片流。
 * 订阅 signal 主题 2 秒节流刷新；支持 方向/状态/模板 过滤与排序；
 * 有效期倒计时，过期置灰；85 分以上按发光预算高亮。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { listSignals, type SignalItem } from '@/api/signals'
import NtButton from '@/ui/NtButton.vue'
import NtProgressRing from '@/ui/NtProgressRing.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtRadar from '@/viz/NtRadar.vue'
import {
  clock,
  dimValue,
  isValidExpired,
  messageOf,
  price,
  ratioPct,
  sideLabel,
  signalStatusLabel,
  strategyLabel,
  validRemainText,
} from '@/utils/format'
import { onPush, type WsPushMessage } from '@/ws/hub'
import { getWorkbench } from '@/workspace/inject'

type SideFilter = '' | 'buy' | 'sell'
type StateFilter = '' | 'active' | 'expired' | 'closed'
type SortKey = 'score' | 'time'

const rows = ref<SignalItem[]>([])
const error = ref('')
const loading = ref(false)
const sideFilter = ref<SideFilter>('')
const stateFilter = ref<StateFilter>('')
const strategyFilter = ref('')
const sortKey = ref<SortKey>('score')
/** 倒计时用的当前时刻，30 秒一跳。 */
const nowTick = ref(Date.now())
const labels = ['技术面', '消息面', '资金面', '外围']

const sideOptions = [
  { value: '', label: '全部方向' },
  { value: 'buy', label: '买入' },
  { value: 'sell', label: '卖出' },
]
const stateOptions = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '有效' },
  { value: 'expired', label: '已过期' },
  { value: 'closed', label: '已平仓' },
]
const sortOptions = [
  { value: 'score', label: '按综合分' },
  { value: 'time', label: '按信号时间' },
]

const strategies = computed(() => [...new Set(rows.value.map((row) => row.strategy).filter(Boolean))])
const strategyOptions = computed(() => [
  { value: '', label: '全部模板' },
  ...strategies.value.map((code) => ({ value: code, label: strategyLabel(code) })),
])

function isClosed(item: SignalItem): boolean {
  const key = item.status.toLowerCase()
  return key === 'closed' || key === 'done' || item.hasPnl
}

function isExpired(item: SignalItem): boolean {
  return !isClosed(item) && isValidExpired(item.validUntil, nowTick.value)
}

const filtered = computed(() => {
  const list = rows.value.filter((item) => {
    if (sideFilter.value && item.side !== sideFilter.value) return false
    if (strategyFilter.value && item.strategy !== strategyFilter.value) return false
    if (stateFilter.value === 'active' && (isClosed(item) || isExpired(item))) return false
    if (stateFilter.value === 'expired' && !isExpired(item)) return false
    if (stateFilter.value === 'closed' && !isClosed(item)) return false
    return true
  })
  return [...list].sort((a, b) => {
    if (sortKey.value === 'time') return String(b.time).localeCompare(String(a.time))
    return (b.score ?? 0) - (a.score ?? 0)
  })
})

const activeCount = computed(() => rows.value.filter((item) => !isClosed(item) && !isExpired(item)).length)

async function load() {
  loading.value = true
  error.value = ''
  try {
    rows.value = await listSignals()
  } catch (err) {
    rows.value = []
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

function openMarket(symbol: string) {
  getWorkbench()?.openPanel('market', { symbol })
}

function openTrade(item: SignalItem) {
  getWorkbench()?.openPanel('trade', { symbol: item.symbol, side: item.side, price: item.entry || '' })
}

function dimsOf(item: SignalItem) {
  return labels.map((label) => ({ label, value: dimValue(item.dims, label) }))
}

function stateBadge(item: SignalItem): { text: string; cls: string } {
  if (isClosed(item)) return { text: signalStatusLabel(item.status), cls: 'closed' }
  if (isExpired(item)) return { text: '已过期', cls: 'expired' }
  return { text: validRemainText(item.validUntil, nowTick.value) || signalStatusLabel(item.status), cls: 'active' }
}

let offPush: (() => void) | undefined
let tickTimer: ReturnType<typeof setInterval> | null = null
let lastPush = 0

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'signal') return
  const now = Date.now()
  if (now - lastPush < 2000) return
  lastPush = now
  void load()
}

onMounted(() => {
  void load()
  // signal 属常驻主题（BASE_TOPICS），只需 onPush，不必再 subscribe。
  offPush = onPush(onPushMessage)
  tickTimer = setInterval(() => {
    nowTick.value = Date.now()
  }, 30_000)
})

onBeforeUnmount(() => {
  offPush?.()
  if (tickTimer) clearInterval(tickTimer)
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">信号中枢</h1>
        <p class="nt-note">
          综合分、四维、入场止损止盈、建议仓位和证据。85 分以上高亮；过期信号置灰，不再可执行。
          <span v-if="rows.length">有效 {{ activeCount }} / 共 {{ rows.length }}</span>
        </p>
      </div>
      <NtButton :loading="loading" @click="load">刷新</NtButton>
    </header>

    <div class="filters">
      <NtSelect v-model="sideFilter" :options="sideOptions" />
      <NtSelect v-model="stateFilter" :options="stateOptions" />
      <NtSelect v-model="strategyFilter" :options="strategyOptions" />
      <NtSelect v-model="sortKey" :options="sortOptions" />
    </div>

    <p v-if="loading" class="nt-empty">读取中…</p>
    <p v-else-if="error" class="nt-err">{{ error }}</p>
    <p v-else-if="!rows.length" class="nt-empty">今日没有信号。</p>
    <p v-else-if="!filtered.length" class="nt-empty">当前过滤条件下没有信号。</p>
    <div v-else class="grid">
      <article
        v-for="item in filtered"
        :key="item.id || item.symbol + item.time"
        class="card"
        :class="{ hot: item.highValue, stale: isClosed(item) || isExpired(item) }"
      >
        <header>
          <button type="button" class="sym" @click="openMarket(item.symbol)">{{ item.symbol }}</button>
          <span>{{ item.name }}</span>
          <span class="tm">{{ clock(item.time) }}</span>
          <!-- 红涨绿跌：买入红、卖出绿 -->
          <em :class="item.side === 'buy' ? 'up' : item.side === 'sell' ? 'down' : ''">{{ sideLabel(item.side) }}</em>
          <b class="badge" :class="stateBadge(item).cls">{{ stateBadge(item).text }}</b>
        </header>
        <div class="body">
          <NtProgressRing :value="item.score" :hot="item.highValue" :size="56" />
          <template v-if="!item.degraded">
            <NtRadar :dims="dimsOf(item)" :size="84" />
            <ul>
              <li v-for="label in labels" :key="label">
                <span>{{ label }}</span>
                <b>{{ dimValue(item.dims, label) ?? '—' }}</b>
              </li>
            </ul>
          </template>
          <p v-else class="degraded-note">模型降级：四维未复核，仅有规则分 {{ item.ruleScore ?? '—' }}</p>
        </div>
        <dl>
          <div>
            <dt>入场</dt>
            <dd>{{ price(item.entryLow) }} – {{ price(item.entryHigh) }}</dd>
          </div>
          <div>
            <dt>止损</dt>
            <dd class="down">{{ price(item.stop) }}</dd>
          </div>
          <div>
            <dt>止盈</dt>
            <dd class="up">{{ price(item.take) }}</dd>
          </div>
          <div>
            <dt>建议仓位</dt>
            <dd>{{ item.positionPct != null ? ratioPct(item.positionPct, 0) : '—' }}</dd>
          </div>
          <div>
            <dt>ATR</dt>
            <dd>{{ price(item.atr) }}</dd>
          </div>
          <div v-if="item.hasPnl">
            <dt>后验盈亏</dt>
            <dd :class="(item.pnlPct ?? 0) >= 0 ? 'up' : 'down'">
              {{ ratioPct(item.pnlPct) }}<template v-if="item.holdingDays != null"> · {{ item.holdingDays }} 日</template>
            </dd>
          </div>
        </dl>
        <p class="meta">
          {{ strategyLabel(item.strategy) }} · 规则 {{ item.ruleScore ?? '—' }} · AI {{ item.aiScore ?? '—' }}
          <template v-if="item.holdDaysMax != null"> · 最长持有 {{ item.holdDaysMax }} 日</template>
          <template v-if="item.traceId"> · {{ item.traceId }}</template>
        </p>
        <p v-if="item.reason" class="reason">{{ item.reason }}</p>
        <ul v-if="item.evidence.length" class="evidence">
          <li v-for="(line, index) in item.evidence" :key="index">{{ line }}</li>
        </ul>
        <button
          v-if="!isClosed(item) && !isExpired(item)"
          type="button"
          class="go"
          @click="openTrade(item)"
        >
          去下单
        </button>
      </article>
    </div>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.filters :deep(.nt-select) {
  min-width: 120px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 12px;
}

.card {
  padding: var(--nt-pad-panel);
  display: flex;
  flex-direction: column;
  gap: 10px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

/* AI 信号 ≥85：发光预算允许的高亮。 */
.card.hot {
  border-color: rgba(0, 229, 255, 0.45);
  box-shadow: 0 0 24px rgba(0, 229, 255, 0.14);
}

/* 已过期/已平仓：置灰，不再可执行。 */
.card.stale {
  opacity: 0.55;
}

header {
  display: flex;
  gap: 8px;
  align-items: baseline;
  flex-wrap: wrap;
}

.sym {
  background: none;
  border: none;
  color: var(--nt-accent);
  padding: 0;
  min-height: 0;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}

.sym:hover {
  text-decoration: underline;
}

.tm {
  color: var(--nt-text-3);
  font-size: 11px;
  font-family: var(--nt-font-mono);
}

em {
  margin-left: auto;
  font-style: normal;
  font-weight: 600;
}

.badge {
  font-size: 11px;
  font-weight: 600;
  padding: 1px 8px;
  border-radius: 999px;
  border: 1px solid var(--nt-border-2);
  color: var(--nt-text-2);
}

.badge.active {
  color: var(--nt-accent);
  border-color: rgba(0, 229, 255, 0.4);
}

.badge.expired {
  color: var(--nt-warn);
  border-color: rgba(255, 107, 0, 0.4);
}

.badge.closed {
  color: var(--nt-text-3);
}

.body {
  display: grid;
  grid-template-columns: 56px 84px 1fr;
  gap: 10px;
  align-items: center;
}

.degraded-note {
  grid-column: 2 / 4;
  margin: 0;
  font-size: 12px;
  color: var(--nt-warn);
}

ul {
  list-style: none;
  margin: 0;
  padding: 0;
}

.body li,
dl {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
}

dl {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px 12px;
  margin: 0;
  font-family: var(--nt-font-mono);
}

dt {
  color: var(--nt-text-3);
}

dd {
  margin: 0;
}

.meta,
.reason {
  margin: 0;
  color: var(--nt-text-2);
  font-size: 11.5px;
}

.evidence {
  color: var(--nt-text-2);
  font-size: 11.5px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.go {
  align-self: flex-start;
  padding: 5px 12px;
  font-size: 12px;
  font-weight: 600;
  font-family: inherit;
  color: var(--nt-accent);
  background: rgba(0, 229, 255, 0.06);
  border: 1px solid rgba(0, 229, 255, 0.32);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.go:hover {
  background: rgba(0, 229, 255, 0.12);
}
</style>
