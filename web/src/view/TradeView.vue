<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { askConfirm } from '@/store/confirm'
import { useAccountStore } from '@/stores/account'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import { getAccount } from '@/api/trade'
import {
  cancelOrder,
  listFills,
  listOrders,
  listPositions,
  placeOrder,
  type Fill,
  type Order,
  type Position,
} from '@/api/trade'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import {
  amount,
  canCancelOrder,
  messageOf,
  orderStatusLabel,
  pnlClass,
  pnlSign,
  price,
  ratioPct,
  sideLabel,
} from '@/utils/format'
import { router } from '@/router'
import { consumePanelParams } from '@/workspace/panel-bus'
import { getWorkbench } from '@/workspace/inject'
import { PAPER_DAYS_REQUIRED } from './decision-desk'

const auth = useAuthStore()
const accountStore = useAccountStore()
const riskStore = useRiskStore()

const account = ref('SIM')
const positions = ref<Position[]>([])
const orders = ref<Order[]>([])
const fills = ref<Fill[]>([])
const error = ref('')
const hint = ref('')
const loading = ref(false)
const orderFilter = ref<'open' | 'all'>('open')
const touched = ref(false)
const cancelAllBusy = ref(false)
/** 一键全撤第一步：再点一次才执行（8 秒内有效）。 */
const cancelAllArmed = ref(false)
let cancelAllTimer: ReturnType<typeof setTimeout> | null = null
/** SIM 账本的模拟交易日数（LIVE 准入判断用；paperDays 是 SIM 口径，与当前账本无关）。 */
const simPaperDays = ref<number | null>(null)
const form = reactive<{ symbol: string; side: 'buy' | 'sell'; price: string; volume: string }>({
  symbol: '',
  side: 'buy',
  price: '',
  volume: '',
})

const liveAllowed = computed(() => simPaperDays.value != null && simPaperDays.value >= PAPER_DAYS_REQUIRED)
const accountOptions = computed(() => [
  { value: 'SIM', label: `SIM 模拟（${simPaperDays.value ?? '—'}/${PAPER_DAYS_REQUIRED} 交易日）` },
  {
    value: 'LIVE',
    label: liveAllowed.value ? 'LIVE 实盘' : `LIVE 实盘（模拟满 ${PAPER_DAYS_REQUIRED} 交易日才可选）`,
    disabled: !liveAllowed.value,
  },
])
const sideOptions = [
  { value: 'buy', label: '买入' },
  { value: 'sell', label: '卖出' },
]

interface PosRow extends Record<string, unknown> {
  symbol: string
  quantity: number | null
  available: number | null
  avgCost: number | null
  lastPrice: number | null
  marketValue: number | null
  pnl: number | null
  pnlPct: number | null
}
interface OrderRow extends Record<string, unknown> {
  id: string
  symbol: string
  side: string
  status: string
  price: number | null
  volume: number | null
  filled: number | null
}
interface FillRow extends Record<string, unknown> {
  id: string
  time: string
  symbol: string
  side: string
  price: number | null
  qty: number | null
  amount: number | null
}

const posColumns: NtTableColumn<PosRow>[] = [
  { key: 'symbol', title: '代码' },
  { key: 'quantity', title: '数量', align: 'right' },
  { key: 'available', title: '可卖', align: 'right' },
  { key: 'avgCost', title: '成本', align: 'right' },
  { key: 'lastPrice', title: '现价', align: 'right' },
  { key: 'marketValue', title: '市值', align: 'right' },
  { key: 'pnl', title: '浮动盈亏', align: 'right' },
  { key: 'pnlPct', title: '盈亏%', align: 'right' },
]
const orderColumns: NtTableColumn<OrderRow>[] = [
  { key: 'id', title: '编号' },
  { key: 'symbol', title: '代码' },
  { key: 'side', title: '方向' },
  { key: 'status', title: '状态' },
  { key: 'price', title: '价格', align: 'right' },
  { key: 'volume', title: '数量', align: 'right' },
  { key: 'filled', title: '已成', align: 'right' },
  { key: 'actions', title: '', width: '70px' },
]
const fillColumns: NtTableColumn<FillRow>[] = [
  { key: 'time', title: '时间' },
  { key: 'symbol', title: '代码' },
  { key: 'side', title: '方向' },
  { key: 'price', title: '价格', align: 'right' },
  { key: 'qty', title: '数量', align: 'right' },
  { key: 'amount', title: '金额', align: 'right' },
]

const posRows = computed<PosRow[]>(() =>
  positions.value.map((p) => {
    const marketValue = p.lastPrice != null && p.quantity != null ? p.lastPrice * p.quantity : null
    const cost = p.avgCost != null && p.quantity != null ? p.avgCost * p.quantity : null
    const pnl = marketValue != null && cost != null ? marketValue - cost : null
    const pnlPct = pnl != null && cost ? pnl / cost : null
    return { ...p, marketValue, pnl, pnlPct }
  }),
)
const orderRows = computed<OrderRow[]>(() => {
  const rows = orders.value.map((o) => ({ ...o, actions: '' }) as OrderRow & { actions: string })
  if (orderFilter.value === 'open') return rows.filter((row) => canCancelOrder(row.status))
  return rows
})
const hiddenTerminal = computed(
  () => orderFilter.value === 'open' && orders.value.some((row) => !canCancelOrder(row.status)),
)
const openOrders = computed(() => orders.value.filter((row) => canCancelOrder(row.status)))
const fillRows = computed<FillRow[]>(() => fills.value.map((f) => ({ ...f })))
/** 当日成交合计（费用字段后端暂无，合计只算成交额）。 */
const fillSummary = computed(() => {
  if (!fills.value.length) return null
  let buy = 0
  let sell = 0
  for (const f of fills.value) {
    const amt = f.amount ?? (f.price != null && f.qty != null ? f.price * f.qty : 0)
    if (f.side === 'buy') buy += amt
    else if (f.side === 'sell') sell += amt
  }
  return { count: fills.value.length, buy, sell, net: sell - buy }
})

const priceNum = computed(() => Number(form.price))
const volumeNum = computed(() => Number(form.volume))
const symbolOk = computed(() => /^\d{6}\.(SZ|SH|BJ)$/i.test(form.symbol.trim()))
const priceOk = computed(() => priceNum.value > 0)
const volumeOk = computed(() => Number.isInteger(volumeNum.value) && volumeNum.value >= 100 && volumeNum.value % 100 === 0)
const canSubmit = computed(() => !auth.isViewer() && symbolOk.value && priceOk.value && volumeOk.value)
const estimate = computed(() => (priceOk.value && volumeNum.value > 0 ? priceNum.value * volumeNum.value : null))
const buyable = computed(() => {
  const cash = accountStore.cash
  if (!priceOk.value || cash == null) return null
  return Math.floor(cash / priceNum.value / 100) * 100
})
/** 持仓里这只票的现价/成本，下单时作参照。 */
const positionRef = computed(() => {
  const code = form.symbol.trim().toUpperCase()
  if (!code) return null
  return positions.value.find((p) => p.symbol.toUpperCase() === code) ?? null
})

watch(
  account,
  (value) => {
    accountStore.book = value
    void accountStore.refresh()
    void load()
  },
  { immediate: true },
)

async function load() {
  loading.value = true
  error.value = ''
  const [pos, ord, fill, sim, riskRes] = await Promise.allSettled([
    listPositions(account.value),
    listOrders(account.value),
    listFills(account.value),
    getAccount('SIM'),
    riskStore.refresh(),
  ])
  positions.value = pos.status === 'fulfilled' ? pos.value : []
  orders.value = ord.status === 'fulfilled' ? ord.value : []
  fills.value = fill.status === 'fulfilled' ? fill.value : []
  if (sim.status === 'fulfilled') simPaperDays.value = sim.value.paperDays
  void riskRes
  const failed = [pos, ord, fill].find((item) => item.status === 'rejected')
  if (failed) error.value = messageOf((failed as PromiseRejectedResult).reason)
  loading.value = false
}

async function submit() {
  hint.value = ''
  error.value = ''
  if (auth.isViewer()) {
    error.value = '当前是查看角色，不能下单'
    return
  }
  if (account.value === 'LIVE' && !liveAllowed.value) {
    error.value = `模拟盘未满 ${PAPER_DAYS_REQUIRED} 个交易日（当前 ${simPaperDays.value ?? '—'}），实盘通道未开`
    return
  }
  touched.value = true
  if (!canSubmit.value) {
    error.value = '请填写 6 位代码（如 600519.SH）、正限价，以及 100 股整数倍'
    return
  }
  const draft = {
    clientOrderId: `manual-${Date.now()}`,
    account: account.value,
    symbol: form.symbol.trim(),
    side: form.side,
    price: priceNum.value,
    volume: volumeNum.value,
    source: 'manual',
    operator: auth.operatorName,
  }
  if (account.value === 'LIVE') {
    const result = await askConfirm(draft, true)
    if (result !== 'ok') {
      hint.value = '实盘确认已作废，委托未提交'
      return
    }
  }
  try {
    await placeOrder(draft)
    hint.value = '委托已提交'
    await load()
    await accountStore.refresh()
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function cancel(id: string) {
  error.value = ''
  try {
    await cancelOrder(id)
    await load()
  } catch (err) {
    error.value = messageOf(err)
  }
}

/** 一键全撤：第一次点进入待确认态，8 秒内再点才执行（不用原生 confirm）。 */
async function cancelAll() {
  const list = openOrders.value
  if (!list.length) return
  if (!cancelAllArmed.value) {
    cancelAllArmed.value = true
    if (cancelAllTimer) clearTimeout(cancelAllTimer)
    cancelAllTimer = setTimeout(() => {
      cancelAllArmed.value = false
    }, 8000)
    return
  }
  cancelAllArmed.value = false
  if (cancelAllTimer) clearTimeout(cancelAllTimer)
  cancelAllBusy.value = true
  error.value = ''
  hint.value = ''
  let ok = 0
  let failed = 0
  for (const order of list) {
    try {
      await cancelOrder(order.id)
      ok += 1
    } catch {
      failed += 1
    }
  }
  cancelAllBusy.value = false
  hint.value = `已提交撤单 ${ok} 笔${failed ? `，${failed} 笔失败` : ''}`
  await load()
}

function openMarket(symbol: string) {
  getWorkbench()?.openPanel('market', { symbol })
}

onMounted(() => {
  // 预填：面板参数优先；路由 query 兜底（面板不在 RouterView 下，勿用 useRoute）。
  const panelParams = consumePanelParams('trade') ?? {}
  const q = router.currentRoute.value.query
  const symbol = panelParams.symbol ?? q.symbol
  const side = panelParams.side ?? q.side
  const px = panelParams.price ?? q.price
  if (typeof symbol === 'string') form.symbol = symbol
  if (side === 'buy' || side === 'sell') form.side = side
  if (typeof px === 'string') form.price = px
  if ((accountStore.book || 'SIM') !== account.value) account.value = accountStore.book
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">交易</h1>
        <p class="nt-note">
          B 买入 · S 卖出 · Shift+K 急停。实盘单先确认。股数按 100 股一手。
          <span v-if="riskStore.buysToday != null" class="head-meta">今日买入 {{ riskStore.buysToday }}/5</span>
        </p>
      </div>
      <NtSelect v-model="account" label="账户" :options="accountOptions" />
    </header>

    <form class="nt-form nt-panel" @submit.prevent="submit">
      <label>
        <span>代码</span>
        <NtInput v-model="form.symbol" placeholder="600519.SH" mono :invalid="touched && !symbolOk" />
      </label>
      <NtSelect v-model="form.side" label="方向" :options="sideOptions" />
      <label>
        <span>限价</span>
        <NtInput v-model="form.price" type="number" mono :invalid="touched && !priceOk" />
      </label>
      <label>
        <span>股数</span>
        <NtInput v-model="form.volume" type="number" mono :invalid="touched && !volumeOk" />
      </label>
      <NtButton type="submit" variant="success" :disabled="!canSubmit">提交委托</NtButton>
    </form>
    <p class="quote">
      可用 {{ amount(accountStore.cash) }}
      · 预估 {{ estimate == null ? '—' : amount(estimate) }}
      · 可买 {{ buyable == null ? '—' : `${buyable} 股` }}
      <template v-if="positionRef">
        · 现价 {{ price(positionRef.lastPrice) }} / 成本 {{ price(positionRef.avgCost) }}
      </template>
    </p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="hint" class="nt-note">{{ hint }}</p>

    <section>
      <h2 class="nt-panel__title">持仓</h2>
      <NtTable :columns="posColumns" :rows="posRows" row-key="symbol" empty="暂无持仓">
        <template #cell-symbol="{ value }">
          <button type="button" class="sym" @click="openMarket(String(value))">{{ value }}</button>
        </template>
        <template #cell-avgCost="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-lastPrice="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-marketValue="{ value }">{{ amount(value as number | null) }}</template>
        <template #cell-pnl="{ value }">
          <span :class="pnlClass(value)">{{ pnlSign(value) }}{{ amount(value) }}</span>
        </template>
        <template #cell-pnlPct="{ value }">
          <span :class="pnlClass(value)">{{ ratioPct(value) }}</span>
        </template>
      </NtTable>
    </section>

    <section>
      <h2 class="nt-panel__title">委托</h2>
      <div class="filters">
        <button type="button" :class="{ on: orderFilter === 'open' }" @click="orderFilter = 'open'">可撤</button>
        <button type="button" :class="{ on: orderFilter === 'all' }" @click="orderFilter = 'all'">全部</button>
        <span v-if="hiddenTerminal" class="nt-note">终态已收起</span>
        <NtButton
          v-if="openOrders.length && !auth.isViewer()"
          size="sm"
          variant="danger"
          :loading="cancelAllBusy"
          class="cancel-all"
          @click="cancelAll"
        >
          {{ cancelAllArmed ? `确认撤销全部 ${openOrders.length} 笔？` : `一键全撤（${openOrders.length}）` }}
        </NtButton>
      </div>
      <NtTable
        :columns="orderColumns"
        :rows="orderRows"
        row-key="id"
        :empty="loading ? '读取中…' : orderFilter === 'open' ? '没有可撤委托' : '暂无委托'"
      >
        <template #cell-side="{ value }">
          <span :class="value === 'buy' ? 'up' : 'down'">{{ sideLabel(value) }}</span>
        </template>
        <template #cell-status="{ value }">{{ orderStatusLabel(value) }}</template>
        <template #cell-price="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-actions="{ row }">
          <NtButton v-if="canCancelOrder(row.status)" size="sm" @click="cancel(String(row.id))">撤单</NtButton>
        </template>
      </NtTable>
    </section>

    <section>
      <h2 class="nt-panel__title">成交</h2>
      <p v-if="fillSummary" class="nt-note fills-sum">
        当日成交 {{ fillSummary.count }} 笔 · 买入 {{ amount(fillSummary.buy) }} · 卖出
        {{ amount(fillSummary.sell) }} · 净额
        <span :class="pnlClass(fillSummary.net)">{{ pnlSign(fillSummary.net) }}{{ amount(fillSummary.net) }}</span>
      </p>
      <NtTable :columns="fillColumns" :rows="fillRows" empty="暂无成交明细">
        <template #cell-side="{ value }">
          <span :class="value === 'buy' ? 'up' : 'down'">{{ sideLabel(value) }}</span>
        </template>
        <template #cell-price="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-amount="{ value }">{{ amount(value as number | null) }}</template>
      </NtTable>
    </section>
  </div>
</template>

<style scoped>
h2 {
  margin: 4px 0 8px;
}

.head-meta {
  margin-left: 6px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-2);
}

.sym {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-accent);
  font: inherit;
  cursor: pointer;
}

.sym:hover {
  text-decoration: underline;
}

.cancel-all {
  margin-left: auto;
}

.fills-sum {
  margin: 0 0 6px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.quote {
  margin: 0;
  font-size: 12px;
  color: var(--nt-text-2);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.filters {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 8px;
}

.filters button {
  min-height: 26px;
  padding: 0 10px;
  font-size: 12px;
  color: var(--nt-text-2);
  background: transparent;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.filters button.on {
  color: var(--nt-bg-0);
  background: var(--nt-accent);
  border-color: transparent;
}
</style>
