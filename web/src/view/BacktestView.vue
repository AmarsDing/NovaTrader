<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  createTask,
  getReport,
  listStrategies,
  listTasks,
  listTrades,
  type BacktestReport,
  type BacktestTask,
  type BacktestTrade,
  type StrategyMeta,
} from '@/api/backtest'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtEquityCurve from '@/viz/NtEquityCurve.vue'
import { messageOf, price, ratioPct, signed } from '@/utils/format'

const strategies = ref<StrategyMeta[]>([])
const tasks = ref<BacktestTask[]>([])
const report = ref<BacktestReport | null>(null)
const trades = ref<BacktestTrade[]>([])
const selected = ref<BacktestTask | null>(null)
const error = ref('')
const hint = ref('')
const loading = ref(false)
const form = ref({
  name: '',
  strategy: '',
  freq: '1d',
  start: '',
  end: '',
  cash: '',
})

const freqOptions = [
  { value: '1d', label: '日线' },
  { value: '1m', label: '1 分钟' },
]

type TaskRow = BacktestTask & Record<string, unknown>
type TradeRow = BacktestTrade & Record<string, unknown>

const taskColumns: NtTableColumn<TaskRow>[] = [
  { key: 'name', title: '名称' },
  { key: 'strategy', title: '策略' },
  { key: 'status', title: '状态' },
  { key: 'totalReturn', title: '收益', align: 'right' },
  { key: 'maxDrawdown', title: '回撤', align: 'right' },
  { key: 'winRate', title: '胜率', align: 'right' },
  { key: 'actions', title: '', width: '70px' },
]
const tradeColumns: NtTableColumn<TradeRow>[] = [
  { key: 'time', title: '时间' },
  { key: 'symbol', title: '代码' },
  { key: 'side', title: '方向' },
  { key: 'price', title: '价格', align: 'right' },
  { key: 'quantity', title: '数量', align: 'right' },
  { key: 'pnl', title: '盈亏', align: 'right' },
]

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [list, names] = await Promise.all([listTasks(), listStrategies()])
    tasks.value = list
    strategies.value = names
    if (!form.value.strategy && names[0]) form.value.strategy = names[0].name
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

async function create() {
  hint.value = ''
  error.value = ''
  if (!form.value.strategy || !form.value.start || !form.value.end) {
    error.value = '请填写策略、开始和结束日期'
    return
  }
  const config: Record<string, unknown> = {
    strategy: form.value.strategy,
    freq: form.value.freq,
    start: form.value.start,
    end: form.value.end,
  }
  if (form.value.cash) config.initial_cash = Number(form.value.cash)
  try {
    await createTask({
      kind: 'single',
      name: form.value.name || `${form.value.strategy} ${form.value.start}`,
      config,
    })
    hint.value = '任务已提交'
    await load()
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function openTask(task: TaskRow) {
  selected.value = task
  report.value = null
  trades.value = []
  error.value = ''
  try {
    const [rep, rows] = await Promise.all([getReport(task.id), listTrades(task.id)])
    report.value = rep
    trades.value = rows
  } catch (err) {
    error.value = messageOf(err)
  }
}

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">回测</h1>
        <p class="nt-note">单次回测任务和报告。参数优化页在增强阶段，这里不提供。</p>
      </div>
      <NtButton :loading="loading" @click="load">刷新</NtButton>
    </header>

    <form class="nt-form nt-panel" @submit.prevent="create">
      <label><span>名称</span><NtInput v-model="form.name" /></label>
      <label>
        <span>策略</span>
        <input v-model="form.strategy" class="nt-input" type="text" list="strategy-names" placeholder="breakout_ref" />
        <datalist id="strategy-names">
          <option v-for="item in strategies" :key="item.name" :value="item.name">{{ item.title }}</option>
        </datalist>
      </label>
      <NtSelect v-model="form.freq" label="周期" :options="freqOptions" />
      <label><span>开始</span><NtInput v-model="form.start" placeholder="YYYY-MM-DD" mono /></label>
      <label><span>结束</span><NtInput v-model="form.end" placeholder="YYYY-MM-DD" mono /></label>
      <label><span>初始资金</span><NtInput v-model="form.cash" type="number" placeholder="默认" mono /></label>
      <NtButton variant="primary" @click="create">建立任务</NtButton>
    </form>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="hint" class="nt-note">{{ hint }}</p>

    <NtTable
      :columns="taskColumns"
      :rows="tasks as TaskRow[]"
      row-key="id"
      :empty="loading ? '读取中…' : '暂无任务'"
    >
      <template #cell-name="{ row }">{{ row.name || row.id }}</template>
      <template #cell-strategy="{ row }">{{ row.strategy }} · {{ row.freq }}</template>
      <template #cell-status="{ row }">{{ row.status }} {{ row.message }}</template>
      <template #cell-totalReturn="{ value }">{{ ratioPct(value) }}</template>
      <template #cell-maxDrawdown="{ value }">{{ ratioPct(value) }}</template>
      <template #cell-winRate="{ value }">{{ ratioPct(value) }}</template>
      <template #cell-actions="{ row }">
        <NtButton size="sm" @click="openTask(row)">报告</NtButton>
      </template>
    </NtTable>

    <section v-if="selected" class="nt-panel">
      <h2 class="nt-panel__title">报告 · {{ selected.name || selected.id }}</h2>
      <div v-if="report" class="nt-cards report">
        <NtStatCard label="总收益" :value="ratioPct(report.metrics.totalReturn)" />
        <NtStatCard label="年化" :value="ratioPct(report.metrics.annualReturn)" />
        <NtStatCard label="最大回撤" :value="ratioPct(report.metrics.maxDrawdown)" />
        <NtStatCard label="胜率" :value="ratioPct(report.metrics.winRate)" />
        <NtStatCard label="夏普" :value="report.metrics.sharpe != null ? String(report.metrics.sharpe) : '—'" />
        <NtStatCard label="回合" :value="report.metrics.rounds != null ? String(report.metrics.rounds) : '—'" />
      </div>
      <NtEquityCurve v-if="report" :points="report.equity" height="280px" />
      <p v-for="(line, index) in report?.warnings || []" :key="index" class="nt-note">{{ line }}</p>
      <NtTable
        :columns="tradeColumns"
        :rows="trades as TradeRow[]"
        :row-key="'seq'"
        empty="暂无成交"
      >
        <template #cell-price="{ value }">{{ price(value) }}</template>
        <template #cell-pnl="{ value }">{{ signed(value) }}</template>
      </NtTable>
    </section>
  </div>
</template>

<style scoped>
.report {
  margin-bottom: 12px;
}
</style>
