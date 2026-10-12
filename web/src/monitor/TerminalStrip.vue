<script setup lang="ts">
/**
 * 底部终端带（监控业务监控方案 §4.6）：事件流 TAPE / 运行日志 / 任务与准入 / 模型·GPU / 账本 五格。
 * 头部一行是运行状态光点（服务、中间件、磁盘、显存），down 才显示名字。
 * 第一格随时段换语义（§4.3）：盘前昨日复盘、竞价异动流、连续竞价事件流、午休半场小结、
 * 尾盘持仓明细、盘后复盘、非交易日任务调度扩为两格（此时事件流不上台）。
 * 数据全部来自 loadMonitorInput 已聚合的接口 + 页面级 TapeSession（tape.ts），本组件不另发请求。
 * 只读：整条终端带没有写操作，点行只是选中画布节点。缺口写「未接入：需要 GET …」，不编数。
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { amount, intText, missLabel, ratioPct, signed } from '@/utils/format'
import { dutyOf, isLoud, statusDot } from './duty'
import { gpuMemory } from './hud'
import { TAPE_DOMAIN_LABEL, TAPE_MAX_ROWS } from './tape'
import type { TapeRow } from './tape'
import type { BandKey, FirstPane } from './timeband'
import type { MonitorInput } from './types'

const props = defineProps<{
  input: MonitorInput
  /** 事件流行（倒序，已切到 40 行）。undefined = 页面没喂进流数据，格内如实写未接入 */
  rows?: TapeRow[]
  /** 时段给的第一格语义；缺省按 tape 渲染 */
  firstPane?: FirstPane
  /** 时段键：只为区分盘前「昨日复盘」与盘后「复盘」的标题 */
  bandKey?: BandKey
}>()
const emit = defineEmits<{ select: [id: string]; flash: [id: string] }>()

interface DutyDot {
  id: string
  name: string
  status: string
  loud: boolean
}

const duties = computed<DutyDot[]>(() => {
  const ops = props.input.ops
  if (!ops?.ok) return []
  const rows: DutyDot[] = []
  for (const item of ops.services) {
    rows.push(toDot('service', item.name, item.status))
  }
  for (const item of ops.resources ?? []) {
    rows.push(toDot('resource', item.name, item.status))
  }
  return rows
})

function toDot(kind: string, title: string, status: string): DutyDot {
  return {
    id: `${kind}:${title.trim().toLowerCase()}`,
    name: dutyOf(title).name,
    status,
    loud: isLoud(status),
  }
}

const dotClass = statusDot

/* ---- 第一格：时段语义（§4.3） ---- */

const firstPane = computed<FirstPane>(() => props.firstPane ?? 'tape')
const offday = computed(() => firstPane.value === 'dispatch')
const firstTitle = computed(() => {
  switch (firstPane.value) {
    case 'auction':
      return '竞价异动'
    case 'midday':
      return '半场小结'
    case 'positions':
      return '持仓明细'
    case 'review':
      return props.bandKey === 'preopen' ? '昨日复盘' : '复盘'
    default:
      return '事件流'
  }
})
const firstCode = computed(() => {
  switch (firstPane.value) {
    case 'auction':
      return 'AUCTION'
    case 'midday':
      return 'MIDDAY'
    case 'positions':
      return 'POSITIONS'
    case 'review':
      return 'REVIEW'
    default:
      return 'TAPE'
  }
})
/* 竞价异动流没有只读出口（S7 的 GET /v1/market/auction），先用事件流顶上，不空着一格 */
const tapeVisible = computed(() => firstPane.value === 'tape' || firstPane.value === 'auction')

/* ---- 事件流：暂停滚动 + 新行入场淡出（§4.6 交互列） ---- */

const paused = ref(false)
const shown = ref<TapeRow[]>([])
const pendingCount = ref(0)
/** 已经上过屏的行 id。用来判「新行」，只在本组件内存里 */
const seen = new Set<string>()
const entering = ref<Set<string>>(new Set())
let seeded = false
let enterTimer = 0

watch(
  () => props.rows,
  (rows) => {
    if (paused.value) {
      const ids = new Set(shown.value.map((row) => row.id))
      pendingCount.value = (rows ?? []).filter((row) => !ids.has(row.id)).length
      return
    }
    publish(rows ?? [])
  },
  { immediate: true },
)

function publish(rows: TapeRow[], mark = true) {
  shown.value = rows
  pendingCount.value = 0
  if (mark) {
    markEntering(rows)
    return
  }
  for (const row of rows) seen.add(row.id)
}

function togglePause() {
  if (paused.value) {
    paused.value = false
    /* 暂停期间攒下的一批不逐行闪：恢复是重新对齐，不是新事件入场 */
    publish(props.rows ?? [], false)
    return
  }
  paused.value = true
}

/** 首次填充不算「新」——打开页面不该满屏闪一下 */
function markEntering(rows: TapeRow[]) {
  if (!seeded) {
    if (rows.length === 0) return
    seeded = true
    for (const row of rows) seen.add(row.id)
    return
  }
  const fresh = rows.filter((row) => !seen.has(row.id))
  for (const row of rows) seen.add(row.id)
  if (fresh.length === 0) return
  const next = new Set(entering.value)
  for (const row of fresh) next.add(row.id)
  entering.value = next
  if (enterTimer) window.clearTimeout(enterTimer)
  enterTimer = window.setTimeout(() => {
    entering.value = new Set()
    enterTimer = 0
  }, 320)
}

onBeforeUnmount(() => {
  if (enterTimer) window.clearTimeout(enterTimer)
})

/** 点某行 → 选中对应画布节点并闪一下。没有对应节点的行不可点（不假装能定位） */
function pickRow(row: TapeRow) {
  if (!row.selectId) return
  emit('select', row.selectId)
  emit('flash', row.selectId)
}

const tapeEmpty = computed(() => {
  if (props.rows == null) return '事件流未接入：页面没有喂进流数据'
  return '还没有事件。推送、通知、审计、成交任一到账，这里就会按时间倒序出现'
})

/* ---- 第一格的其它时段语义（有数据才上台，没有就点名缺的接口） ----
 * 这几格是摘要，不带点选（§4.6 只给事件流「点某行选中节点」的交互）；
 * 要看细节走画布牌或 HUD 格。 */

interface PaneLine {
  key: string
  label: string
  value: string
  tone: 'up' | 'down' | 'warn' | 'flat'
}

const account = computed(() => props.input.account)
const risk = computed(() => props.input.risk)
const pnlTone = computed<'up' | 'down' | 'flat'>(() => {
  const pnl = account.value?.dayPnl
  if (pnl == null || pnl === 0) return 'flat'
  return pnl > 0 ? 'up' : 'down'
})

/** 午休半场小结：三个数都读现有接口，口径写在 note 里，不当作半场增量 */
const middayLines = computed<PaneLine[]>(() => {
  const lines: PaneLine[] = []
  const book = account.value
  lines.push(
    book?.ok
      ? { key: 'pnl', label: '半场盈亏', value: signed(book.dayPnl), tone: pnlTone.value }
      : { key: 'pnl', label: '半场盈亏', value: '账户未返回', tone: 'flat' },
  )
  const sentiment = props.input.sentiment
  lines.push(
    sentiment?.ok
      ? { key: 'limit', label: '涨停家数', value: `${intText(sentiment.up)} 家 · 炸板 ${ratioPct(sentiment.brokenRate)}`, tone: 'flat' }
      : { key: 'limit', label: '涨停家数', value: '情绪接口无返回', tone: 'flat' },
  )
  const briefing = props.input.briefing
  const head = briefing?.ok ? briefing.items[0]?.title ?? '' : ''
  lines.push({
    key: 'focus',
    label: '下午关注',
    value: head || (briefing?.ok ? '晨报没有条目' : '晨报未返回'),
    tone: 'flat',
  })
  return lines
})

/** 尾盘持仓明细：距止损要有对齐的信号止损价才写，没有就不写，不拿别的价冒充。
 * 浮盈走红涨绿跌，距止损的橙/红只作整行左边框（§5.1），两件事不混色。 */
interface PositionLine {
  key: string
  symbol: string
  money: string
  tone: 'up' | 'down' | 'flat'
  tail: string
  flag: '' | 'warn' | 'danger'
}

const positionLines = computed<PositionLine[]>(() => {
  const book = account.value
  if (!book?.ok) return []
  const stops = new Map<string, number>()
  for (const signal of props.input.signals?.ok ? props.input.signals.items : []) {
    if (signal.stop == null || stops.has(signal.symbol)) continue
    stops.set(signal.symbol, signal.stop)
  }
  return book.positions.slice(0, 6).map((item) => {
    const last = item.lastPrice
    const float = last != null && item.avgCost != null && item.quantity != null ? (last - item.avgCost) * item.quantity : null
    const stop = stops.get(item.symbol)
    let tail = '止损价未对齐'
    let flag: PositionLine['flag'] = ''
    if (last != null && stop != null && last > 0) {
      const distance = (last - stop) / last
      if (distance <= 0) {
        tail = '已触发止损'
        flag = 'danger'
      } else {
        tail = `距止损 ${ratioPct(distance, 1)}`
        flag = distance < 0.02 ? 'warn' : ''
      }
    }
    return {
      key: item.symbol,
      symbol: item.symbol,
      money: float == null ? '浮盈未返回' : signed(float),
      tone: float == null || float === 0 ? 'flat' : float > 0 ? 'up' : 'down',
      tail,
      flag,
    }
  })
})

/** 复盘：漏选条数与最大原因是现有数据；胜率与评分-收益散点没有只读出口，点名接口 */
const reviewLines = computed<PaneLine[]>(() => {
  const candidates = props.input.candidates
  if (!candidates?.ok) return [{ key: 'miss', label: '漏选', value: '候选接口无返回', tone: 'flat' }]
  const missed = candidates.items.filter((item) => item.stage !== 'selected')
  const lines: PaneLine[] = [{ key: 'miss', label: '漏选', value: `${missed.length} 条`, tone: 'flat' }]
  const limits = props.input.limits
  if (limits?.ok) {
    const missedSymbols = new Set(missed.map((item) => item.symbol))
    const hit = limits.rows.filter((row) => missedSymbols.has(row.symbol)).length
    lines.push({ key: 'limit-miss', label: '涨停里漏掉', value: `${hit} / ${limits.rows.length} 只`, tone: 'flat' })
  }
  for (const item of missed.slice(0, 3)) {
    lines.push({
      key: `row:${item.symbol}`,
      label: item.symbol,
      value: `${item.name || '名称未返回'} · ${item.missReason ? missLabel(item.missReason) : '原因未返回'}`,
      tone: 'flat',
    })
  }
  return lines
})

const REVIEW_GAP = '未接入：需要 GET /v1/strategy/performance、/v1/strategy/score-vs-pnl（胜率与评分-收益散点）'
const AUCTION_GAP = '未接入：需要 GET /v1/market/auction（竞价逐笔异动，每 5 秒一行）。下面先用事件流顶上。'

/* ---- 运行日志：通知 + 审计按时间合并 ---- */

interface LogRow {
  id: string
  time: string
  tag: string
  text: string
  tone: 'up' | 'down' | 'warn' | 'flat'
}

const logs = computed<LogRow[]>(() => {
  const rows: LogRow[] = []
  if (props.input.notices?.ok) {
    for (const item of props.input.notices.items) {
      rows.push({
        id: `n:${item.id}`,
        time: shortTime(item.time),
        tag: item.level || '通知',
        text: item.title,
        tone: item.level === 'critical' || item.level === 'danger' ? 'down' : item.level === 'warn' ? 'warn' : 'flat',
      })
    }
  }
  if (props.input.audit?.ok) {
    for (const item of props.input.audit.items) {
      rows.push({ id: `a:${item.id}`, time: shortTime(item.time), tag: item.action || '审计', text: item.target, tone: 'flat' })
    }
  }
  return rows.slice(0, 14)
})

function shortTime(value: string): string {
  const text = value.trim()
  if (text.length >= 19) return text.slice(11, 19)
  return text || '—'
}

/* ---- 任务与准入：回测任务 + 准入 + 影子（非交易日扩为两格，多显示几条） ---- */

const tasks = computed(() => (props.input.tasks?.ok ? props.input.tasks.items.slice(0, offday.value ? 8 : 4) : []))
const admission = computed(() => props.input.admission ?? null)
const admitPct = computed(() => {
  const admit = admission.value
  if (!admit?.ok || admit.tradingDays == null || !admit.requiredDays) return null
  return Math.min(100, Math.round((admit.tradingDays / admit.requiredDays) * 100))
})
const shadows = computed(() => (props.input.shadow?.ok ? props.input.shadow.items.slice(0, 3) : []))

/* ---- 模型 / GPU ---- */

const tiers = computed(() => (props.input.brainStats?.ok ? props.input.brainStats.tiers : []))
const brainMissing = computed(() => !props.input.brainStats?.ok)
const memory = computed(() => gpuMemory(props.input))
const queued = computed(() => tiers.value.reduce((sum, tier) => sum + (tier.queued ?? 0), 0))
const inFlight = computed(() => tiers.value.reduce((sum, tier) => sum + (tier.inFlight ?? 0), 0))

/* ---- 账本 ---- */

const openOrders = computed(() => {
  if (!props.input.orders?.ok) return null
  return props.input.orders.items.filter((item) => !['filled', 'cancelled', 'canceled', 'rejected', 'expired'].includes(item.status.toLowerCase())).length
})
/** 文件单状态（§4.6）：只有实盘才谈得上文件单，读口是 S7 的 GET /v1/ops/broker-files */
const brokerLine = computed(() => {
  const mode = risk.value?.ok ? risk.value.mode : ''
  if (!mode) return '模式未返回，文件单状态无从判定'
  if (mode.toUpperCase() !== 'LIVE') return '模拟盘，不下文件单'
  return '未接入：需要 GET /v1/ops/broker-files'
})
</script>

<template>
  <footer class="nt-terms">
    <div class="nt-terms__rail">
      <span class="nt-terms__label">运行状态</span>
      <button
        v-for="dot in duties"
        :key="dot.id"
        type="button"
        class="nt-duty"
        :class="{ 'is-loud': dot.loud }"
        :title="`${dot.name} ${dot.status || '无状态'}`"
        @click="emit('select', dot.id)"
      >
        <i class="dot" :class="dotClass(dot.status)"></i>
        <b v-if="dot.loud">{{ dot.name }}</b>
      </button>
      <span v-if="duties.length === 0" class="nt-terms__gap">运维状态无返回</span>
    </div>
    <div class="nt-terms__grid" :class="{ 'is-offday': offday }">
      <section v-if="!offday" class="nt-term nt-term--first" :data-pane="firstPane">
        <header>
          <b>{{ firstTitle }}</b>
          <span>{{ firstCode }}</span>
          <button v-if="tapeVisible" type="button" class="nt-term__btn" :aria-pressed="paused" @click="togglePause">
            {{ paused ? (pendingCount > 0 ? `继续 · ${pendingCount} 条新` : '继续') : '暂停' }}
          </button>
          <em v-if="tapeVisible" class="nt-term__count">{{ shown.length }}/{{ TAPE_MAX_ROWS }}</em>
        </header>

        <template v-if="tapeVisible">
          <p v-if="firstPane === 'auction'" class="nt-term__gapline">{{ AUCTION_GAP }}</p>
          <ul v-if="shown.length" class="nt-tape">
            <li v-for="row in shown" :key="row.id" :class="{ 'is-enter': entering.has(row.id) }">
              <button
                v-if="row.selectId"
                type="button"
                class="nt-tape__row"
                :class="[`is-${row.domain}`, `tone-${row.tone}`]"
                :title="`选中 ${row.selectId}`"
                @click="pickRow(row)"
              >
                <em>{{ row.time }}</em>
                <i>{{ TAPE_DOMAIN_LABEL[row.domain] }}</i>
                <span>{{ row.text }}</span>
                <b v-if="row.ident">{{ row.ident }}</b>
              </button>
              <div v-else class="nt-tape__row" :class="[`is-${row.domain}`, `tone-${row.tone}`]">
                <em>{{ row.time }}</em>
                <i>{{ TAPE_DOMAIN_LABEL[row.domain] }}</i>
                <span>{{ row.text }}</span>
                <b v-if="row.ident">{{ row.ident }}</b>
              </div>
            </li>
          </ul>
          <p v-else class="nt-term__empty">{{ tapeEmpty }}</p>
        </template>

        <template v-else-if="firstPane === 'midday'">
          <ul>
            <li v-for="line in middayLines" :key="line.key">
              <i :class="line.tone === 'flat' ? '' : `is-${line.tone}`">{{ line.label }}</i>
              <span :class="`is-${line.tone}`">{{ line.value }}</span>
            </li>
          </ul>
          <p class="nt-term__note">盈亏是当日累计，半场增量接口未接入；涨停家数是当前情绪快照。</p>
        </template>

        <template v-else-if="firstPane === 'positions'">
          <ul v-if="positionLines.length">
            <li v-for="line in positionLines" :key="line.key" :data-flag="line.flag || undefined">
              <i>{{ line.symbol }}</i>
              <span :class="`is-${line.tone}`">{{ line.money }}</span>
              <em>{{ line.tail }}</em>
            </li>
          </ul>
          <p v-else class="nt-term__empty">{{ account?.ok ? (account.positionsOk === false ? '持仓接口没有应答，不当作空仓' : '空仓') : '账户未返回' }}</p>
          <p class="nt-term__note">距止损只在对齐到信号止损价时才写；不足 2% 整行转橙，已触发转红。</p>
        </template>

        <template v-else>
          <ul>
            <li v-for="line in reviewLines" :key="line.key">
              <i>{{ line.label }}</i>
              <span>{{ line.value }}</span>
            </li>
          </ul>
          <p class="nt-term__note">{{ REVIEW_GAP }}</p>
        </template>
      </section>

      <section class="nt-term nt-term--log">
        <header>
          <b>运行日志</b>
          <span>RUN LOG</span>
        </header>
        <ul v-if="logs.length">
          <li v-for="row in logs" :key="row.id">
            <em>{{ row.time }}</em>
            <i :class="`is-${row.tone}`">{{ row.tag }}</i>
            <span>{{ row.text }}</span>
          </li>
        </ul>
        <p v-else class="nt-term__empty">通知与审计都没有记录</p>
      </section>

      <section class="nt-term nt-term--dispatch">
        <header>
          <b>任务与准入</b>
          <span>DISPATCH</span>
        </header>
        <ul v-if="tasks.length">
          <li v-for="task in tasks" :key="task.id">
            <i class="is-flat">{{ task.status || '—' }}</i>
            <span>{{ task.name || task.id }}</span>
            <em>{{ task.message }}</em>
          </li>
        </ul>
        <p v-else class="nt-term__empty">{{ input.tasks?.ok ? '没有回测任务' : '任务未返回' }}</p>
        <div v-if="admission?.ok" class="nt-term__meter">
          <span>模拟准入</span>
          <div class="nt-meter"><i :style="{ width: `${admitPct ?? 0}%` }"></i></div>
          <b>{{ admission.ready ? '已通过' : `${intText(admission.tradingDays)}/${intText(admission.requiredDays)} 日` }}</b>
        </div>
        <div v-if="shadows.length" class="nt-term__meter">
          <span>影子</span>
          <b v-for="row in shadows" :key="row.name" class="nt-term__chip">{{ row.name }} {{ row.live ?? '—' }}/{{ row.shadow == null ? '—' : ratioPct(row.shadow) }}</b>
        </div>
      </section>

      <section class="nt-term nt-term--brain">
        <header>
          <b>模型 / GPU</b>
          <span>BRAIN</span>
        </header>
        <ul v-if="tiers.length">
          <li v-for="tier in tiers" :key="tier.tier || tier.model">
            <i :class="tier.circuitOpen ? 'is-down' : 'is-up'">{{ tier.circuitOpen ? '熔断' : '在跑' }}</i>
            <span>{{ tier.tier }} · {{ tier.model || '未命名' }}</span>
            <em>
              调用 {{ intText(tier.calls ?? null) }} 失败 {{ intText(tier.failures) }} P95 {{ tier.p95Ms == null ? '—' : `${Math.round(tier.p95Ms)}ms` }}
              <template v-if="tier.kvCacheUsage != null"> · KV {{ ratioPct(tier.kvCacheUsage, 0) }}</template>
            </em>
          </li>
        </ul>
        <p v-if="brainMissing" class="nt-term__empty">模型统计无返回</p>
        <div v-if="memory" class="nt-term__meter">
          <span>显存</span>
          <div class="nt-meter"><i :style="{ width: `${memory.pct}%` }"></i></div>
          <b>{{ memory.pct }}% · {{ memory.text }}</b>
        </div>
        <p class="nt-term__note">
          队列 {{ queued }} 在跑 {{ inFlight }} · GPU 利用率未接入：需要 GET /v1/ops/gpu · 令牌合计接口未做，花费不折算金额
        </p>
      </section>

      <section class="nt-term nt-term--ledger">
        <header>
          <b>账本</b>
          <span>LEDGER</span>
        </header>
        <p class="nt-term__pnl" :class="pnlTone">{{ account?.ok ? signed(account.dayPnl) : '账户未返回' }}</p>
        <ul v-if="account?.ok">
          <li>
            <span>权益 {{ amount(account.equity) }} · 现金 {{ amount(account.cash) }}</span>
            <em>持仓 {{ account.positions.length }}<template v-if="openOrders != null"> · 委托 {{ openOrders }}</template></em>
          </li>
          <li v-if="risk?.ok">
            <span>模式 {{ risk.mode || '—' }}</span>
            <em :class="risk.killActive ? 'is-down' : ''">{{ risk.killActive ? `急停 ${risk.killReason || '已触发'}` : '风控守着' }}</em>
          </li>
          <li>
            <span>文件单</span>
            <em>{{ brokerLine }}</em>
          </li>
        </ul>
      </section>
    </div>
  </footer>
</template>
