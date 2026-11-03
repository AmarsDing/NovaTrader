<script setup lang="ts">
/**
 * 风控日报面板（M08）：当日风控指标汇总（校验/放行/拒绝、规则命中、风控事件）。
 * 数据来自 GET /api/risk/report（DailyReportReply）：后端只给计数与事件，
 * 指标行由 api/risk.ts 按计数拼出，缺的计数写「未返回」。
 */
import { computed, onMounted, ref } from 'vue'
import {
  getRejectRecords,
  getRiskReport,
  getRejectTrend,
  type RejectRecords,
  type RejectTrend,
  type RiskReport,
} from '@/api/risk'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtModal from '@/ui/NtModal.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtEChart from '@/viz/NtEChart.vue'
import { useChartPalette } from '@/theme/tokens'
import type { EChartsCoreOption } from 'echarts/core'
import { statusOf } from '@/ui/status-map'
import { clock, messageOf } from '@/utils/format'

const report = ref<RiskReport | null>(null)
const loading = ref(false)
const error = ref('')
/* 批次 A8：日报日期切换（getRiskReport date 参数早已支持，面板补选择位）；
   空串=服务端默认（最新交易日）。日期是查询输入不是浏览偏好，不入 prefs 档。 */
const dateInput = ref('')
const activeDate = ref('')
/** 空态/注脚的「哪一天」口径：优先报告自带日期，其次当前查询日期，兜底「最新交易日」。 */
const dayText = computed(() => report.value?.date || activeDate.value || '最新交易日')

function applyDate() {
  const d = dateInput.value.trim()
  if (d && !/^\d{4}-\d{2}-\d{2}$/.test(d)) {
    error.value = '日期格式为 YYYY-MM-DD'
    return
  }
  if (d === activeDate.value) return
  activeDate.value = d
  void load()
}

type Row = { metric: string; value: string; note: string } & Record<string, unknown>
const columns: NtTableColumn<Row>[] = [
  { key: 'metric', title: '指标' },
  { key: 'value', title: '值', mono: true },
  { key: 'note', title: '备注' },
]
const rows = computed(() => (report.value?.rows ?? []) as unknown as Row[])

const KIND_LABEL: Record<string, string> = {
  killswitch: '急停',
  mode: '模式变更',
  breaker: '熔断',
  reject_rate: '拒绝率告警',
  param: '参数变更',
}

type EventRow = { at: string; kind: string; state: string; detail: string } & Record<string, unknown>
const eventColumns: NtTableColumn<EventRow>[] = [
  { key: 'at', title: '时间', mono: true },
  { key: 'kind', title: '类别' },
  { key: 'state', title: '状态' },
  { key: 'detail', title: '详情' },
]
const eventRows = computed<EventRow[]>(() =>
  (report.value?.events ?? []).map((item) => ({
    at: item.at ? clock(item.at) : '时刻未返回',
    kind: KIND_LABEL[item.kind] ?? (item.kind || '未返回'),
    state: statusOf(item.active ? 'risk.active' : 'alert.released').text,
    detail: [item.mode, item.source, item.operator, item.reason].filter(Boolean).join(' · ') || '没有更多字段',
  })),
)

async function load() {
  loading.value = true
  error.value = ''
  try {
    report.value = await getRiskReport(activeDate.value)
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
  void loadTrend()
}

/** FE-R2：拒绝聚合（BE-11）独立于日报本体加载，失败不拖垮日报。 */
const trendData = ref<RejectTrend | null>(null)
const trendError = ref('')
const palette = useChartPalette()

async function loadTrend() {
  trendError.value = ''
  try {
    trendData.value = await getRejectTrend(activeDate.value, 7)
  } catch (err) {
    trendData.value = null
    trendError.value = messageOf(err)
  }
}

/** 7 日拦截率曲线（rejectRate 0–1 → 百分比展示）。 */
const trendOption = computed<EChartsCoreOption | null>(() => {
  const trend = trendData.value?.trend ?? []
  if (!trend.length) return null
  return {
    animation: false,
    tooltip: {
      trigger: 'axis',
      valueFormatter: (v: unknown) => (typeof v === 'number' ? `${v.toFixed(1)}%` : '—'),
    },
    grid: { left: 48, right: 16, top: 20, bottom: 24 },
    xAxis: {
      type: 'category',
      data: trend.map((d) => d.date.slice(5)),
      axisLabel: { color: palette.value.text3, fontSize: 10 },
      axisLine: { lineStyle: { color: palette.value.border2 } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: palette.value.text3, fontSize: 10, formatter: '{value}%' },
      splitLine: { lineStyle: { color: palette.value.border1 } },
    },
    series: [
      {
        name: '拦截率',
        type: 'line',
        smooth: true,
        showSymbol: true,
        data: trend.map((d) => +(d.rejectRate * 100).toFixed(2)),
        itemStyle: { color: palette.value.accent },
        lineStyle: { color: palette.value.accent },
      },
    ],
    textStyle: { color: palette.value.text2 },
  }
})

type TopRuleRow = { rule: string; name: string; count: string } & Record<string, unknown>
const topRuleColumns: NtTableColumn<TopRuleRow>[] = [
  { key: 'rule', title: '规则', mono: true },
  { key: 'name', title: '名称' },
  { key: 'count', title: '窗口拦截', mono: true },
]
const topRuleRows = computed<TopRuleRow[]>(() =>
  (trendData.value?.topRules ?? []).map((r) => ({
    rule: r.rule || '未返回',
    name: r.name || '未命名',
    count: r.count === null ? '未返回' : String(r.count),
  })),
)
const trendWindowText = computed(() => {
  const t = trendData.value
  if (!t || !t.trend.length) return ''
  return `${t.trend[0].date} ~ ${t.end || t.trend[t.trend.length - 1].date}`
})

/* ---------- FE-R2 拒绝明细抽屉：TopN 行点击展开，窗口与 TopN 一致（activeDate 起 7 日） ----------
   数据走 GET /api/risk/rejects 真明细（risk_check 行），不用聚合值拼。 */
const detailOpen = ref(false)
const detailData = ref<RejectRecords | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
/** TopN 行带入的规则过滤，可清除查全部。 */
const detailRule = ref('')
const detailSymbol = ref('')
const detailOperator = ref('')
const detailReason = ref('')
const detailOffset = ref(0)
const DETAIL_LIMIT = 50

const detailPageText = computed(() => {
  const d = detailData.value
  // items 空但 total>0：翻页途中数据被并发消减，offset 越过末页，不显示「第 101–50 条」这种倒挂。
  if (!d || d.total === 0 || d.items.length === 0) return ''
  const from = d.offset + 1
  const to = Math.min(d.offset + d.items.length, d.total)
  return `第 ${from}–${to} / ${d.total} 条`
})
const detailHasPrev = computed(() => detailOffset.value > 0)
const detailHasNext = computed(() => {
  const d = detailData.value
  return !!d && d.offset + d.items.length < d.total
})

async function loadDetail() {
  detailLoading.value = true
  detailError.value = ''
  try {
    detailData.value = await getRejectRecords({
      end: activeDate.value,
      days: 7,
      rule: detailRule.value,
      symbol: detailSymbol.value.trim(),
      operator: detailOperator.value.trim(),
      reason: detailReason.value.trim(),
      offset: detailOffset.value,
      limit: DETAIL_LIMIT,
    })
  } catch (err) {
    detailData.value = null
    detailError.value = messageOf(err)
  } finally {
    detailLoading.value = false
  }
}

function openDetail(rule: string) {
  detailRule.value = rule === '未返回' ? '' : rule
  // 清掉上次抽屉残留的过滤，避免再开时带着旧条件却以为是 TopN 全量。
  detailSymbol.value = ''
  detailOperator.value = ''
  detailReason.value = ''
  detailOffset.value = 0
  detailOpen.value = true
  void loadDetail()
}

/** 改过滤条件：offset 归零重查。 */
function applyDetailFilter() {
  detailOffset.value = 0
  void loadDetail()
}

function clearDetailRule() {
  detailRule.value = ''
  applyDetailFilter()
}

function detailPrev() {
  if (!detailHasPrev.value) return
  detailOffset.value = Math.max(0, detailOffset.value - DETAIL_LIMIT)
  void loadDetail()
}

function detailNext() {
  if (!detailHasNext.value) return
  detailOffset.value += DETAIL_LIMIT
  void loadDetail()
}

const SIDE_LABEL: Record<string, string> = { buy: '买入', sell: '卖出' }
const SOURCE_LABEL: Record<string, string> = { auto: '自动', watch: '盯盘', manual: '手动' }

type DetailRow = {
  at: string
  symbol: string
  side: string
  price: string
  volume: string
  rule: string
  reason: string
  actor: string
  latency: string
} & Record<string, unknown>
const detailColumns: NtTableColumn<DetailRow>[] = [
  { key: 'at', title: '时间', mono: true },
  { key: 'symbol', title: '标的', mono: true },
  { key: 'side', title: '方向' },
  { key: 'price', title: '价格', mono: true },
  { key: 'volume', title: '量(请求→放行)', mono: true },
  { key: 'rule', title: '规则' },
  { key: 'reason', title: '原因' },
  { key: 'actor', title: '账户 · 来源 · 操作人' },
  { key: 'latency', title: '延迟', mono: true },
]
const detailRows = computed<DetailRow[]>(() =>
  (detailData.value?.items ?? []).map((it) => ({
    at: it.createdAt ? clock(it.createdAt) : '时刻未返回',
    symbol: it.symbol || '未返回',
    side: SIDE_LABEL[it.side] ?? (it.side || '未返回'),
    price: String(it.price),
    volume: `${it.volumeReq} → ${it.volumeFinal}`,
    rule: it.rule ? `${it.rule} ${it.ruleName}` : '未返回',
    reason: it.reason || '未返回',
    actor: [it.accountType, SOURCE_LABEL[it.source] ?? it.source, it.operator].filter(Boolean).join(' · ') || '未返回',
    latency: `${it.latencyUs}µs`,
  })),
)
/** 三态诚呈（同面板 182 行约定）：读取中 / 失败重试 / 真空分说。 */
const detailEmpty = computed(() => {
  if (detailLoading.value) return '读取中…'
  if (detailError.value) return '明细未返回——点「查询」重试'
  // total>0 而本页空：offset 越过末页（翻页途中数据被并发消减），引导返回而不是谎称窗口无记录。
  if ((detailData.value?.total ?? 0) > 0) return '本页无记录——点「上一页」返回'
  return '窗口内没有符合条件的拒绝记录'
})

onMounted(load)

/** 三分态诚呈（同 alert-rules:182 约定）：首载中 / 读取失败 / 真空表分说，不把失败误示为「暂无」。
 *  批次 A8：空态跟查询日期走，不在看历史日时还说「今天」。 */
const metricsEmpty = computed(() => {
  if (loading.value) return '读取中…'
  if (error.value) return '风控指标未返回——点上方刷新重试'
  return `${dayText.value}没有风控校验记录`
})
const eventsEmpty = computed(() => {
  if (loading.value) return '读取中…'
  if (error.value) return '风控事件未返回——点上方刷新重试'
  return `${dayText.value}没有风控事件`
})

/** KPI 句式补齐（survey：数字要有重量）：校验/放行/拒绝/P99 四格大卡，明细仍走下表。 */
const kpiRows = computed<Row[]>(() => rows.value.slice(0, 4))
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">风控日报</h1>
        <p class="nt-note">
          {{ report?.date || '—' }}
          <span v-if="report?.generatedAt"> · 生成于 {{ clock(report.generatedAt) }}</span>
        </p>
      </div>
      <div class="head-actions">
        <!-- 批次 A8：日期切换——Enter 或点「切换」生效，避免逐键触发请求。 -->
        <NtInput v-model="dateInput" class="date-input" placeholder="YYYY-MM-DD 默认最新" mono @keydown.enter="applyDate" />
        <NtButton size="sm" :disabled="loading" @click="applyDate">切换</NtButton>
        <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
      </div>
    </header>

    <NtErrorGuide :error="error" />

    <section v-if="report?.summary" class="nt-panel">
      <h2 class="nt-panel__title">摘要</h2>
      <p class="summary">{{ report.summary }}</p>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">指标明细</h2>
      <div v-if="kpiRows.length" class="nt-cards">
        <NtStatCard v-for="k in kpiRows" :key="k.metric" :label="k.metric" :value="k.value" :sub="k.note || undefined" />
      </div>
      <NtTable :columns="columns" :rows="rows" row-key="metric" dense :empty="metricsEmpty" />
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">近 7 日拦截率 <span v-if="trendWindowText" class="window">{{ trendWindowText }}</span></h2>
      <p v-if="trendError" class="trend-note">拒绝趋势未返回——{{ trendError }}</p>
      <NtEChart v-else-if="trendOption" :option="trendOption" height="200px" />
      <p v-else class="trend-note">窗口内没有风控校验记录</p>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">
        拒绝规则 TopN（近 7 日窗口）
        <span class="window">点击行查看构成该计数的逐条明细</span>
      </h2>
      <NtTable
        :columns="topRuleColumns"
        :rows="topRuleRows"
        row-key="rule"
        dense
        clickable
        :empty="trendError ? '拒绝聚合未返回' : '窗口内没有规则拒绝记录'"
        @row-click="openDetail($event.rule)" />
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">风控事件（急停 / 模式 / 熔断）</h2>
      <NtTable :columns="eventColumns" :rows="eventRows" row-key="at" dense :empty="eventsEmpty" />
    </section>

    <!-- FE-R2 拒绝明细抽屉：窗口与 TopN 一致（面板日期起 7 日），逐条对应 risk_check 原始记录。 -->
    <NtModal v-model:open="detailOpen" title="拒绝明细" width="920px">
      <div class="detail-filters">
        <span v-if="detailRule" class="rule-chip">
          {{ detailRule }}
          <NtButton size="sm" variant="quiet" @click="clearDetailRule">× 全部规则</NtButton>
        </span>
        <NtInput v-model="detailSymbol" class="filter-input" placeholder="标的 精确" mono @keydown.enter="applyDetailFilter" />
        <NtInput v-model="detailOperator" class="filter-input" placeholder="操作人 精确" mono @keydown.enter="applyDetailFilter" />
        <NtInput v-model="detailReason" class="filter-input" placeholder="原因 包含" mono @keydown.enter="applyDetailFilter" />
        <NtButton size="sm" :loading="detailLoading" @click="applyDetailFilter">查询</NtButton>
      </div>
      <p class="detail-note">
        窗口与 TopN 一致{{ trendWindowText ? `（${trendWindowText}）` : '' }}；按时间倒序，逐条来自风控校验原始记录。
      </p>
      <p v-if="detailError" class="detail-error">{{ detailError }}</p>
      <NtTable :columns="detailColumns" :rows="detailRows" row-key="at" dense :empty="detailEmpty" />
      <div class="detail-pager">
        <NtButton size="sm" :disabled="!detailHasPrev || detailLoading" @click="detailPrev">上一页</NtButton>
        <span class="page-text">{{ detailPageText || '—' }}</span>
        <NtButton size="sm" :disabled="!detailHasNext || detailLoading" @click="detailNext">下一页</NtButton>
      </div>
    </NtModal>
  </div>
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

.date-input {
  width: 170px;
}

.summary {
  margin: 0;
  font-size: var(--nt-fs-panel); /* 收编自 13px 裸值，等值档映射（§5.3），零视觉换挡 */
  line-height: 1.7;
  color: var(--nt-text-1);
  white-space: pre-wrap;
}

.window {
  margin-left: 8px;
  font-size: 12px;
  font-weight: 400;
  color: var(--nt-text-3);
}

.trend-note {
  margin: 0;
  color: var(--nt-text-3);
  font-size: 12px;
}

.detail-filters {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}

.rule-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 4px 2px 10px;
  border: 1px solid var(--nt-border-2);
  border-radius: 999px;
  font-family: var(--nt-font-mono);
  font-size: 12px;
  color: var(--nt-text-1);
}

.filter-input {
  width: 150px;
}

.detail-note {
  margin: 0 0 8px;
  color: var(--nt-text-3);
  font-size: 12px;
}

.detail-error {
  margin: 0 0 8px;
  color: var(--nt-danger);
  font-size: 12px;
}

.detail-pager {
  display: flex;
  gap: 10px;
  align-items: center;
  justify-content: flex-end;
  margin-top: 10px;
}

.page-text {
  color: var(--nt-text-3);
  font-size: 12px;
  font-family: var(--nt-font-mono);
}
</style>
