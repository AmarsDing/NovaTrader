<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { askConfirm } from '@/store/confirm'
import { useAccountStore } from '@/stores/account'
import { useAuthStore } from '@/stores/auth'
import { cancelOrder, listFills, listOrders, listPositions, placeOrder, type Fill, type Order, type Position } from '@/api/trade'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { amount, messageOf, price } from '@/utils/format'
import { consumePanelParams } from '@/workspace/panel-bus'

const route = useRoute()
const auth = useAuthStore()
const accountStore = useAccountStore()

const account = ref('SIM')
const positions = ref<Position[]>([])
const orders = ref<Order[]>([])
const fills = ref<Fill[]>([])
const error = ref('')
const hint = ref('')
const loading = ref(false)
const form = reactive<{ symbol: string; side: 'buy' | 'sell'; price: string; volume: string }>({
  symbol: '',
  side: 'buy',
  price: '',
  volume: '',
})

const accountOptions = [
  { value: 'SIM', label: 'SIM 模拟' },
  { value: 'LIVE', label: 'LIVE 实盘' },
]
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

const posRows = computed<PosRow[]>(() => positions.value.map((p) => ({ ...p })))
const orderRows = computed<OrderRow[]>(() =>
  orders.value.map((o) => ({ ...o, actions: '' }) as OrderRow & { actions: string }),
)
const fillRows = computed<FillRow[]>(() => fills.value.map((f) => ({ ...f })))

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
  const [pos, ord, fill] = await Promise.allSettled([
    listPositions(account.value),
    listOrders(account.value),
    listFills(account.value),
  ])
  positions.value = pos.status === 'fulfilled' ? pos.value : []
  orders.value = ord.status === 'fulfilled' ? ord.value : []
  fills.value = fill.status === 'fulfilled' ? fill.value : []
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
  const priceNum = Number(form.price)
  const volume = Number(form.volume)
  if (!form.symbol.trim() || !(priceNum > 0) || !Number.isInteger(volume) || volume <= 0) {
    error.value = '请填写代码、正价格和整数股数'
    return
  }
  const draft = {
    clientOrderId: `manual-${Date.now()}`,
    account: account.value,
    symbol: form.symbol.trim(),
    side: form.side,
    price: priceNum,
    volume,
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

onMounted(() => {
  // 预填来源：面板参数（信号页「去下单」/深链）优先，路由 query 兼容兜底。
  const panelParams = consumePanelParams('trade') ?? {}
  const symbol = panelParams.symbol ?? route.query.symbol
  const side = panelParams.side ?? route.query.side
  const price = panelParams.price ?? route.query.price
  if (typeof symbol === 'string') form.symbol = symbol
  if (side === 'buy' || side === 'sell') form.side = side
  if (typeof price === 'string') form.price = price
  if ((accountStore.book || 'SIM') !== account.value) account.value = accountStore.book
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">交易</h1>
        <p class="nt-note">持仓、委托、成交。实盘单先确认，30 秒超时作废。模拟盘直接提交。</p>
      </div>
      <NtSelect v-model="account" label="账户" :options="accountOptions" />
    </header>

    <form class="nt-form nt-panel" @submit.prevent="submit">
      <label><span>代码</span><NtInput v-model="form.symbol" placeholder="600519.SH" mono /></label>
      <NtSelect v-model="form.side" label="方向" :options="sideOptions" />
      <label><span>限价</span><NtInput v-model="form.price" type="number" mono /></label>
      <label><span>股数</span><NtInput v-model="form.volume" type="number" mono /></label>
      <NtButton variant="success" :disabled="auth.isViewer()" @click="submit">提交</NtButton>
    </form>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="hint" class="nt-note">{{ hint }}</p>

    <section>
      <h2 class="nt-panel__title">持仓</h2>
      <NtTable :columns="posColumns" :rows="posRows" row-key="symbol" empty="暂无持仓">
        <template #cell-avgCost="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-lastPrice="{ value }">{{ price(value as number | null) }}</template>
      </NtTable>
    </section>

    <section>
      <h2 class="nt-panel__title">委托</h2>
      <NtTable
        :columns="orderColumns"
        :rows="orderRows"
        row-key="id"
        :empty="loading ? '读取中…' : '暂无委托'"
      >
        <template #cell-side="{ value }">
          <span :class="value === 'buy' ? 'up' : 'down'">{{ value }}</span>
        </template>
        <template #cell-price="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-actions="{ row }">
          <NtButton size="sm" @click="cancel(row.id)">撤单</NtButton>
        </template>
      </NtTable>
    </section>

    <section>
      <h2 class="nt-panel__title">成交</h2>
      <NtTable :columns="fillColumns" :rows="fillRows" empty="暂无成交明细">
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
</style>
