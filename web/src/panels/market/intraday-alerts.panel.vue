<script setup lang="ts">
/**
 * 盘中异动流（M02 P0-4）：急拉（3min ≥1.5%）/ 封板 / 开板，同类 5min 去重，竞价落定清零。
 * REST 回填当日流水，WS alert/move 帧实时前置；「只看候选」走服务端 watch 租约（web 自选 + strategy 池），
 * 该模式下实时帧改为节流重拉（候选池不完整在前端，直接前置会漏掉新入池标的）。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getIntradayAlerts, type IntradayAlert, type IntradayAlertKind } from '@/api/market'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import SymbolName from '@/display/SymbolName.vue'
import { statusOf, type StatusTone } from '@/ui/status-map'
import NtFreshness from '@/ui/NtFreshness.vue'
import { clock, messageOf } from '@/utils/format'
import { dataFrame } from '@/ws/data-frame'
import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'

const PCT_CHOICES = [
  { v: 0, t: '全部' },
  { v: 1.5, t: '≥1.5%' },
  { v: 3, t: '≥3%' },
  { v: 5, t: '≥5%' },
]
/** R6 类别徽标已入册（status-map 第七批 move.* 键）：文案/tone 由册锁定，封板换轨 up 红不再借 danger。 */
const KINDS: { k: IntradayAlertKind; t: string; tone: StatusTone }[] = [
  { k: 'surge', t: statusOf('move.surge').text, tone: statusOf('move.surge').tone },
  { k: 'limit_up', t: statusOf('move.limit_up').text, tone: statusOf('move.limit_up').tone },
  { k: 'break', t: statusOf('move.break').text, tone: statusOf('move.break').tone },
]
const PUSH_RELOAD_MIN_INTERVAL_MS = 15000
const MAX_ROWS = 300

const rows = ref<IntradayAlert[]>([])
const seen = new Set<string>()
const loading = ref(false)
const liveCount = ref(0)
const error = ref('')
const minPct = ref(0)
const watchOnly = ref(false)
const kindOn = ref<Record<IntradayAlertKind, boolean>>({ surge: true, limit_up: true, break: true })

const shown = computed(() => rows.value.filter((r) => kindOn.value[r.kind]))

/** §7.3 首批挂载：行自带异动时点，取全行最大者为新鲜度证据；无行/无时点显「时点未知」。 */
const latestAt = computed<number | null>(() => {
  let best = NaN
  for (const r of rows.value) {
    const t = Date.parse(r.time || '')
    if (Number.isFinite(t)) best = Number.isNaN(best) ? t : Math.max(best, t)
  }
  return Number.isNaN(best) ? null : best
})

function key(r: { symbol: string; kind: string; time: string }): string {
  return `${r.symbol}|${r.kind}|${r.time}`
}

function today(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

let loadSequence = 0
let disposed = false

async function load() {
  const sequence = ++loadSequence
  loading.value = true
  error.value = ''
  try {
    const list = await getIntradayAlerts(today(), minPct.value, watchOnly.value)
    if (disposed || sequence !== loadSequence) return
    seen.clear()
    const merged: IntradayAlert[] = []
    for (const r of list) {
      const k = key(r)
      if (seen.has(k)) continue
      seen.add(k)
      merged.push(r)
    }
    rows.value = merged.slice(0, MAX_ROWS)
  } catch (err) {
    if (disposed || sequence !== loadSequence) return
    error.value = messageOf(err)
  } finally {
    if (!disposed && sequence === loadSequence) loading.value = false
  }
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'alert' || msg.event !== 'move') return
  liveCount.value++
  if (watchOnly.value) {
    // 候选池是服务端状态：该模式下收到异动帧只触发节流重拉，不做本地前置。
    dataFrame.scheduleThrottled('intraday-alerts', PUSH_RELOAD_MIN_INTERVAL_MS, () => {
      if (!disposed) void load()
    })
    return
  }
  const d = (msg.data ?? {}) as Record<string, unknown>
  const kind = String(d.kind ?? '')
  if (kind !== 'surge' && kind !== 'limit_up' && kind !== 'break') return
  const pct = typeof d.pct === 'number' ? d.pct : null
  if (minPct.value > 0 && (pct == null || pct < minPct.value)) return
  const row: IntradayAlert = {
    symbol: String(d.symbol ?? ''),
    name: String(d.name ?? ''),
    kind,
    time: String(d.time ?? ''),
    pct,
    price: typeof d.price === 'number' ? d.price : null,
  }
  const k = key(row)
  if (seen.has(k)) return
  seen.add(k)
  rows.value = [row, ...rows.value].slice(0, MAX_ROWS)
}

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined

// alert 在 BASE_TOPICS 里默认已订阅；这里再 subscribe 走引用计数，面板独立成弹窗时同样能收到帧。
onMounted(() => {
  void load()
  unsubscribe = subscribe('alert')
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
  disposed = true
  loadSequence++
  offPush?.()
  unsubscribe?.()
})

watch([minPct, watchOnly], () => {
  void load()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">
          异动流
          <NtBadge v-if="liveCount" tone="warn" dot>{{ liveCount }} 条实时</NtBadge>
        </h1>
        <p class="nt-sub">急拉 ≥1.5%/3min · 封板 / 开板 · 同类 5min 去重 · 竞价落定清零</p>
      </div>
      <div class="nt-actions">
        <div class="chips" role="group" aria-label="幅度筛选">
          <button
            v-for="c in PCT_CHOICES"
            :key="c.v"
            type="button"
            :class="{ on: minPct === c.v }"
            @click="minPct = c.v"
          >
            {{ c.t }}
          </button>
        </div>
        <div class="chips" role="group" aria-label="类别筛选">
          <button
            v-for="c in KINDS"
            :key="c.k"
            type="button"
            :class="{ on: kindOn[c.k] }"
            @click="kindOn[c.k] = !kindOn[c.k]"
          >
            {{ c.t }}
          </button>
        </div>
        <button type="button" class="watch" :class="{ on: watchOnly }" @click="watchOnly = !watchOnly">只看候选</button>
        <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
        <NtFreshness label="异动" :at="latestAt" />
      </div>
    </header>

    <NtErrorGuide :error="error" />

    <div v-if="shown.length" class="nt-table-wrap"><table>
      <thead>
        <tr><th>时间</th><th>证券</th><th>类别</th><th class="r">幅度</th><th class="r">现价</th></tr>
      </thead>
      <tbody>
        <tr v-for="r in shown.slice(0, 200)" :key="`${r.symbol}-${r.kind}-${r.time}`">
          <!-- R6：时间 mono text-3 | SymbolName | 类别徽标 | 幅度 up/down | 现价；缺失显「—」不冒充 -->
          <td class="mono t3">{{ clock(r.time) || '—' }}</td>
          <td><SymbolName v-if="r.symbol" :code="r.symbol" /><template v-else>—</template></td>
          <td><NtBadge :tone="KINDS.find((c) => c.k === r.kind)?.tone ?? 'neutral'" dot>{{ KINDS.find((c) => c.k === r.kind)?.t ?? r.kind }}</NtBadge></td>
          <td class="r" :class="r.pct != null && r.pct > 0 ? 'up' : r.pct != null && r.pct < 0 ? 'down' : ''">
            {{ r.pct == null ? '—' : `${r.pct >= 0 ? '+' : ''}${r.pct.toFixed(2)}%` }}
          </td>
          <td class="r mono">{{ r.price == null ? '—' : r.price.toFixed(2) }}</td>
        </tr>
      </tbody>
    </table></div>
    <p v-else-if="!loading" class="nt-empty">
      {{ rows.length ? '当前类别筛选下无异动' : '当日无异动（或非交易日 / 引擎未产出）' }}
    </p>
  </div>
</template>

<style scoped>
.nt-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.chips { display: inline-flex; gap: 4px; padding: 3px; border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-m); background: var(--nt-data-raised); }
.chips button { padding: 4px 10px; border: 0; border-radius: var(--nt-radius-s); background: transparent; color: var(--nt-text-2); font-size: 12px; cursor: pointer; }
.chips button.on { background: var(--nt-accent-soft); color: var(--nt-accent); }
.watch { padding: 5px 12px; border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-m); background: transparent; color: var(--nt-text-2); font-size: 12px; cursor: pointer; }
.watch.on { border-color: var(--nt-accent); color: var(--nt-accent); }
.up { color: var(--nt-up); }
.down { color: var(--nt-down); }
.mono { font-family: var(--nt-font-mono); }

/* R6 时间位降档 text-3（方案 §6.3）。 */
.t3 { color: var(--nt-text-3); }
</style>
