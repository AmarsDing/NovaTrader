<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  createTask,
  getReport,
  getTask,
  listStrategies,
  listTasks,
  listTrades,
  type BacktestReport,
  type BacktestTask,
  type BacktestTaskDetail,
  type BacktestTrade,
  type StrategyMeta,
} from '@/api/backtest'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtMissing from '@/ui/NtMissing.vue'
import NtSegmented from '@/ui/NtSegmented.vue'
import NtSelect from '@/ui/NtSelect.vue'
import { statusOf } from '@/ui/status-map'
import NtStatCard from '@/display/NtStatCard.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTrustBar from '@/display/NtTrustBar.vue'
import NtEquityCurve from '@/viz/NtEquityCurve.vue'
import ResearchJobs from './ResearchJobs.vue'
import TradeReplayModal from './TradeReplayModal.vue'
import { aggregateSymbolStats, type SymbolStat } from './backtest-symbol-stats'
import { amount, messageOf, price, ratioPct, signed } from '@/utils/format'
import { downloadCsv, type CsvSection } from '@/utils/csv'

const strategies = ref<StrategyMeta[]>([])
const tasks = ref<BacktestTask[]>([])
const report = ref<BacktestReport | null>(null)
const trades = ref<BacktestTrade[]>([])
const selected = ref<BacktestTask | null>(null)
/** 详情读口（W8b）：信任条判据+原始参数；自取失败 → null 全缺位灰显，不阻断报告本体。 */
const detail = ref<BacktestTaskDetail | null>(null)
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
type SymbolRow = SymbolStat & Record<string, unknown>

const taskColumns: NtTableColumn<TaskRow>[] = [
  { key: 'name', title: '名称', sortable: true },
  { key: 'strategy', title: '策略', sortable: true },
  { key: 'status', title: '状态' },
  { key: 'totalReturn', title: '收益', align: 'right', sortable: true },
  { key: 'maxDrawdown', title: '回撤', align: 'right', sortable: true },
  { key: 'winRate', title: '胜率', align: 'right', sortable: true },
  { key: 'actions', title: '', width: '70px' },
]
const tradeColumns: NtTableColumn<TradeRow>[] = [
  { key: 'time', title: '时间', mono: true, sortable: true },
  { key: 'symbol', title: '代码', kind: 'symbol', sortable: true },
  { key: 'side', title: '方向', sortable: true },
  { key: 'price', title: '价格', align: 'right', sortable: true },
  { key: 'quantity', title: '数量', align: 'right', sortable: true },
  { key: 'amount', title: '金额', align: 'right', sortable: true },
  { key: 'pnl', title: '盈亏', align: 'right', sortable: true },
  { key: 'reason', title: '理由' },
  // 回放直达钮：键盘可达路径（行点击是鼠标密径，二者同入口）——§4.1
  { key: 'replay', title: '', width: '64px' },
]
/** B1 分标的统计表（TSP picks 表移植）：全列 sortable，行点击定位该标的最近一笔回放。 */
const symbolColumns: NtTableColumn<SymbolRow>[] = [
  { key: 'symbol', title: '证券', kind: 'symbol', sortable: true },
  { key: 'fills', title: '成交笔数', align: 'right', sortable: true },
  { key: 'buys', title: '买', align: 'right', sortable: true },
  { key: 'sells', title: '卖', align: 'right', sortable: true },
  { key: 'closedPnl', title: '合计盈亏', align: 'right', sortable: true },
  { key: 'winRate', title: '胜率', align: 'right', sortable: true },
  { key: 'avgPnl', title: '均盈亏', align: 'right', sortable: true },
]

/** 批次 A6：运行中任务的进度条文本（progress 是 0..1 比率，后端 SetProgress(1) 收口；null=未上报）。 */
function progressText(row: BacktestTask): string {
  return row.progress == null ? '' : ratioPct(row.progress, 0)
}
function isActiveTask(row: BacktestTask): boolean {
  return ['pending', 'queued', 'running'].includes(String(row.status || '').toLowerCase())
}

function taskStatus(row: BacktestTask): string {
  const labels: Record<string, string> = {
    pending: '排队',
    queued: '排队',
    running: '运行中',
    done: '完成',
    success: '完成',
    failed: '失败',
    cancelled: '已取消',
    canceled: '已取消',
  }
  const label = labels[String(row.status || '').toLowerCase()] || row.status || '—'
  const message = row.message && row.message !== label ? row.message : ''
  return [label, message].filter(Boolean).join(' · ')
}

/** 有排队/运行中任务时 5 秒自动刷新。 */
const hasActive = computed(() => tasks.value.some(isActiveTask))
let timer: ReturnType<typeof setInterval> | null = null

/** KPI 注脚：区间缺腿就直说，不拿空串糊弄（数据纪律「没取到≠空」）。 */
const rangeText = computed(() => {
  const task = selected.value
  if (!task) return ''
  return task.start && task.end ? `${task.start} ~ ${task.end}` : '区间未返回'
})
const sampleText = computed(() => (trades.value.length ? `${trades.value.length} 笔成交样本` : '暂无成交样本'))

/** 册词（R-UI-04 唯一出处）：假设格/参数段缺位徽注从这里取词，不自封。 */
const words = { undeclared: statusOf('missing.undeclared').text }

/** 第③步假设 8 格：后端声明一条填一格（固定 8 槽），未声明槽灰显保位。 */
function assumptionAt(i: number): string {
  return report.value?.assumptions?.[i - 1] ?? ''
}

/** 第⑥步原始参数：task.config 原样展开；无参数体走缺位。 */
const configText = computed(() => {
  const cfg = detail.value?.config
  return cfg && Object.keys(cfg).length > 0 ? JSON.stringify(cfg, null, 2) : ''
})

const symbolStats = computed<SymbolStat[]>(() => aggregateSymbolStats(trades.value))

/* ---------- B1：⑤ 交易明细 逐笔/分标的 + 交易回放 + CSV 导出 ---------- */

const tradesTabOptions = [
  { value: 'fills', label: '逐笔' },
  { value: 'symbols', label: '分标的' },
]
const tradesTab = ref<'fills' | 'symbols'>('fills')
const replayOpen = ref(false)
const replayIndex = ref(0)
const tradesStep = ref<HTMLElement | null>(null)
/** 关窗回焦点：回放按钮（键盘可达路径）优先，行点击路径退到 ⑤ 步锚点。 */
let replayReturn: HTMLElement | null = null

function openReplay(row: TradeRow, event?: MouseEvent) {
  const idx = trades.value.indexOf(row)
  if (idx < 0) return
  replayReturn = (event?.currentTarget as HTMLElement | null) ?? (document.activeElement as HTMLElement | null)
  replayIndex.value = idx
  replayOpen.value = true
}

/** 分标的行 → 该标的最近一笔（aggregateSymbolStats.lastIndex 已记账）。 */
function openSymbolReplay(row: SymbolRow) {
  if (row.lastIndex < 0) return
  replayReturn = document.activeElement as HTMLElement | null
  replayIndex.value = row.lastIndex
  replayOpen.value = true
}

function onReplayToggle(open: boolean) {
  replayOpen.value = open
  if (open) return
  const target = replayReturn
  replayReturn = null
  if (target && document.contains(target) && target.tabIndex >= 0) {
    target.focus()
    return
  }
  tradesStep.value?.focus()
}

/** 导出：概要镜像报告（格式串）；明细/分标的是原始数值（Excel 可再计算）。
 *  缺段写「未返回」（报告未回）；真空段（区间无成交）只发表头，不写占位词。 */
function exportCsv() {
  const task = selected.value
  if (!task) return
  const safe = (s: string) => s.replace(/[\\/:*?"<>|]/g, '_')
  const rep = report.value
  const sections: CsvSection[] = [
    rep
      ? {
          title: '概要',
          header: ['指标', '值'],
          rows: [
            ['任务', task.name || task.id],
            ['策略', `${task.strategy} · ${task.freq}`],
            ['区间', rangeText.value],
            ['总收益', ratioPct(rep.metrics.totalReturn)],
            ['年化', ratioPct(rep.metrics.annualReturn)],
            ['最大回撤', ratioPct(rep.metrics.maxDrawdown)],
            ['胜率', ratioPct(rep.metrics.winRate)],
            ['盈亏比', rep.metrics.profitFactor != null ? rep.metrics.profitFactor.toFixed(2) : null],
            ['夏普', rep.metrics.sharpe],
            ['回合', rep.metrics.rounds],
            ['成交样本', trades.value.length],
          ],
        }
      : { title: '概要', header: ['说明'], rows: [['报告未返回']] },
  ]
  const CAP = 5000
  const list = trades.value.length > CAP ? trades.value.slice(0, CAP) : trades.value
  sections.push({
    title: trades.value.length > CAP ? `明细（已按 ${CAP} 行截断）` : '明细',
    header: ['时间', '代码', '方向', '价格', '数量', '金额', '盈亏', '理由'],
    rows: list.map((t) => [t.time, t.symbol, t.side, t.price, t.quantity, t.amount, t.pnl, t.reason]),
  })
  sections.push({
    title: '分标的',
    header: ['代码', '成交笔数', '买', '卖', '合计盈亏', '胜率', '均盈亏'],
    rows: symbolStats.value.map((s) => [s.symbol, s.fills, s.buys, s.sells, s.closedPnl, s.winRate, s.avgPnl]),
  })
  downloadCsv(`${safe(task.name || task.id) || '回测报告'}.csv`, sections)
}

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
  detail.value = null
  error.value = ''
  try {
    // 详情读口独立降级：信任条/原始参数取不到不拖死报告本体——对应步整步灰显（R-UI-23）。
    const [rep, rows, det] = await Promise.all([
      getReport(task.id),
      listTrades(task.id),
      getTask(task.id).catch(() => null),
    ])
    report.value = rep
    trades.value = rows
    detail.value = det
  } catch (err) {
    error.value = messageOf(err)
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => {
    if (hasActive.value) void load()
  }, 5000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">回测</h1>
        <p class="nt-note">
          单次回测任务和报告。参数优化页在增强阶段，这里不提供。
          <span v-if="hasActive" class="active-hint">有任务在排队/运行中，每 5 秒自动刷新。</span>
        </p>
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
    <NtErrorGuide :error="error" />
    <p v-if="hint" class="nt-note">{{ hint }}</p>
    <ResearchJobs />

    <NtTable
      :columns="taskColumns"
      :rows="tasks as TaskRow[]"
      row-key="id"
      :empty="loading ? '读取中…' : '还没有回测任务——先用上方表单建一个，收益 / 回撤 / 胜率和每笔成交都会在这里展开'"
    >
      <template #cell-name="{ row }">{{ row.name || row.id }}</template>
      <template #cell-strategy="{ row }">{{ row.strategy }} · {{ row.freq }}</template>
      <template #cell-status="{ row }">
        <span>{{ taskStatus(row) }}</span>
        <!-- 批次 A6：排队/运行中任务给真实进度（progress 是后端上报的 0..1，未上报不画条） -->
        <span v-if="isActiveTask(row) && row.progress != null" class="task-progress">
          <i class="task-progress__track"><i class="task-progress__bar" :style="{ width: `${Math.min(100, Math.max(0, row.progress * 100))}%` }"></i></i>
          <em>{{ progressText(row) }}</em>
        </span>
      </template>
      <template #cell-totalReturn="{ value }">{{ ratioPct(value) }}</template>
      <template #cell-maxDrawdown="{ value }">{{ ratioPct(value) }}</template>
      <template #cell-winRate="{ value }">{{ ratioPct(value) }}</template>
      <template #cell-actions="{ row }">
        <NtButton size="sm" @click="openTask(row)">报告</NtButton>
      </template>
    </NtTable>

    <section v-if="selected" class="nt-panel">
      <div class="report-head">
        <h2 class="nt-panel__title">报告 · {{ selected.name || selected.id }}</h2>
        <!-- B1：概要+明细+分标的三段单文件导出（TSP 四段导出移植），报告未回则概要段写「未返回」 -->
        <NtButton size="sm" @click="exportCsv">导出 CSV</NtButton>
      </div>

      <!-- W8b 结果页六步重排：步骤序固定（结论→信任条→假设→曲线→交易明细→原始参数），缺步灰显不蒸发（R-UI-23） -->
      <section class="bt-step" data-step="conclusion">
        <h3 class="bt-step__title">① 结论</h3>
        <div v-if="report" class="nt-cards report">
          <!-- KPI 注脚给口径：收益/年化挂算式区间，胜率/回合挂样本量——裸数字不让人猜分母 -->
          <NtStatCard label="总收益" :value="ratioPct(report.metrics.totalReturn)" :sub="rangeText" />
          <NtStatCard label="年化" :value="ratioPct(report.metrics.annualReturn)" :sub="rangeText" />
          <NtStatCard label="最大回撤" :value="ratioPct(report.metrics.maxDrawdown)" />
          <NtStatCard label="胜率" :value="ratioPct(report.metrics.winRate)" :sub="sampleText" />
          <!-- 批次 A6：盈亏比是超短策略核心质量指标（已归一未渲，本批接入） -->
          <NtStatCard
            label="盈亏比"
            :value="report.metrics.profitFactor != null ? report.metrics.profitFactor.toFixed(2) : '—'"
            sub="总盈利 / 总亏损"
          />
          <NtStatCard label="夏普" :value="report.metrics.sharpe != null ? String(report.metrics.sharpe) : '—'" sub="按区间成交与净值" />
          <NtStatCard label="回合" :value="report.metrics.rounds != null ? String(report.metrics.rounds) : '—'" :sub="sampleText" />
        </div>
        <NtMissing v-else label="报告未返回" />
        <p v-for="(line, index) in report?.warnings || []" :key="index" class="nt-note">{{ line }}</p>
      </section>

      <section class="bt-step" data-step="trust">
        <NtTrustBar :bar="detail?.trustBar ?? null" title="② 信任条" />
      </section>

      <section class="bt-step" data-step="assumptions">
        <h3 class="bt-step__title">③ 假设</h3>
        <div class="bt-assume__grid">
          <div v-for="i in 8" :key="i" class="bt-assume__cell" :data-slot="`assume-${i}`">
            <span v-if="assumptionAt(i)" class="bt-assume__text">{{ assumptionAt(i) }}</span>
            <NtMissing v-else :label="words.undeclared" />
          </div>
        </div>
      </section>

      <section class="bt-step" data-step="curve">
        <h3 class="bt-step__title">④ 净值曲线</h3>
        <NtEquityCurve v-if="report" :points="report.equity" height="280px" />
        <NtMissing v-else label="曲线未返回" />
      </section>

      <section ref="tradesStep" class="bt-step" data-step="trades" tabindex="-1">
        <div class="bt-step__bar">
          <h3 class="bt-step__title">⑤ 交易明细</h3>
          <!-- B1：逐笔（行点击/回放钮 → K 线现场）与分标的（聚合统计 → 该标的最近一笔）两视图 -->
          <NtSegmented v-model="tradesTab" :options="tradesTabOptions" size="sm" />
        </div>
        <NtTable
          v-if="tradesTab === 'fills'"
          :columns="tradeColumns"
          :rows="trades as TradeRow[]"
          :row-key="'seq'"
          clickable
          empty="该区间无成交——策略未触发任何交易"
          @row-click="openReplay"
        >
          <template #cell-price="{ value }">{{ price(value) }}</template>
          <template #cell-amount="{ value }">{{ amount(value) }}</template>
          <template #cell-pnl="{ value }">{{ signed(value) }}</template>
          <template #cell-replay="{ row }">
            <NtButton size="sm" variant="quiet" @click.stop="openReplay(row, $event)">回放</NtButton>
          </template>
        </NtTable>
        <NtTable
          v-else
          :columns="symbolColumns"
          :rows="symbolStats as SymbolRow[]"
          row-key="symbol"
          clickable
          empty="该区间无成交——无法聚合分标的统计"
          @row-click="openSymbolReplay"
        >
          <template #cell-closedPnl="{ value }">{{ signed(value) }}</template>
          <template #cell-winRate="{ value }">{{ ratioPct(value) }}</template>
          <template #cell-avgPnl="{ value }">{{ signed(value) }}</template>
        </NtTable>
      </section>

      <section class="bt-step" data-step="params">
        <h3 class="bt-step__title">⑥ 原始参数</h3>
        <pre v-if="configText" class="bt-params">{{ configText }}</pre>
        <NtMissing v-else :label="words.undeclared" />
      </section>
    </section>

    <!-- B1：交易回放弹窗（Esc 关窗回焦，←/→ 换笔；关窗焦点回 ⑤ 步锚点或回放钮） -->
    <TradeReplayModal
      :open="replayOpen"
      :trades="trades"
      :index="replayIndex"
      @update:open="onReplayToggle"
      @update:index="replayIndex = $event"
    />
  </div>
</template>

<style scoped>
.report {
  margin-bottom: 0;
}

.report-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.bt-step__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

/* ⑤ 步锚点：关窗回焦落点（键盘路径经回放钮时回钮上），落点本身不可见框。 */
.bt-step:focus {
  outline: none;
}

.bt-step {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 10px;
}

.bt-step__title {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-text-2);
}

.bt-assume__grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 6px;
}

.bt-assume__cell {
  min-width: 0;
  padding: 7px 9px;
  background: var(--nt-bg-2, var(--nt-bg-1));
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s, 4px);
}

.bt-assume__text {
  font-size: 11px;
  line-height: 1.5;
  color: var(--nt-text-2);
}

.bt-params {
  margin: 0;
  max-height: 180px;
  overflow: auto;
  padding: 8px 10px;
  font-family: var(--nt-font-mono);
  font-size: 11px;
  line-height: 1.5;
  color: var(--nt-text-2);
  background: var(--nt-bg-2, var(--nt-bg-1));
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s, 4px);
  white-space: pre-wrap;
  word-break: break-all;
}

.active-hint {
  color: var(--nt-accent);
}

/* 批次 A6：任务行内进度条——固定轨道 + 真实进度填充，宽度即进度。 */
.task-progress {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: 8px;
}

.task-progress__track {
  display: inline-block;
  width: 72px;
  height: 3px;
  background: var(--nt-border-1);
  border-radius: 2px;
  overflow: hidden;
}

.task-progress__bar {
  display: block;
  height: 100%;
  background: var(--nt-accent);
}

.task-progress em {
  font-style: normal;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--nt-text-3);
}
</style>
