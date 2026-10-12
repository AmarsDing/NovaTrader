<script setup lang="ts">
/**
 * /dev/gallery —— 组件矩阵走查页（仅 DEV 构建）。
 * Nt* 全组件 × 主题（暗/亮）× 密度（标准/紧凑），静态示例数据，零后端依赖。
 * 每阶段收尾跑一遍，保证令牌与组件改动可视。
 */
import { ref } from 'vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtIcon from '@/ui/NtIcon.vue'
import NtInput from '@/ui/NtInput.vue'
import NtModal from '@/ui/NtModal.vue'
import NtProgressRing from '@/ui/NtProgressRing.vue'
import NtSegmented from '@/ui/NtSegmented.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTabs from '@/ui/NtTabs.vue'
import NtTooltip from '@/ui/NtTooltip.vue'
import { ICONS, type IconName } from '@/ui/icons'
import { useToast } from '@/ui/useToast'
import NtDepthBoard, { type DepthLevel } from '@/viz/NtDepthBoard.vue'
import NtEquityCurve from '@/viz/NtEquityCurve.vue'
import NtGauge from '@/viz/NtGauge.vue'
import NtHeatmap, { type HeatCell } from '@/viz/NtHeatmap.vue'
import NtIndicatorChart, { type IndicatorPoint } from '@/viz/NtIndicatorChart.vue'
import NtKline from '@/viz/NtKline.vue'
import NtRadar from '@/viz/NtRadar.vue'
import NtSectorBar, { type SectorRow } from '@/viz/NtSectorBar.vue'
import NtSparkline from '@/viz/NtSparkline.vue'
import NtFunnel, { type FunnelStage } from '@/display/NtFunnel.vue'
import NtLadderTiers, { type LadderTier } from '@/display/NtLadderTiers.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import CanvasStage from '@/monitor/CanvasStage.vue'
import { emptyMonitorInput } from '@/monitor/load-input'
import { buildScene } from '@/monitor/scene-model'
import type { Bar } from '@/api/market'
import type { EquityPoint } from '@/api/backtest'
import { useSettingsStore, type DensityName, type ThemeName } from '@/stores/settings'
import { pnlClass, pnlSign } from '@/utils/format'

const settings = useSettingsStore()
const toast = useToast()

const iconNames = Object.keys(ICONS) as IconName[]

/* ---------- 交互态 ---------- */
const input = ref('贵州茅台')
const select = ref('sh')
const toggle = ref(true)
const segment = ref('1d')
const tab = ref('positions')
const modalOpen = ref(false)

/* ---------- 表格 ---------- */
// NtTable 泛型约束 Record<string, unknown>，与 audit-trail 面板同款交叉类型写法
type DemoRow = Record<string, unknown> & {
  symbol: string
  name: string
  price: number
  pct: number
  status: string
  ok: boolean
}

const columns: NtTableColumn<DemoRow>[] = [
  { key: 'symbol', title: '代码', mono: true },
  { key: 'name', title: '名称' },
  { key: 'price', title: '现价', align: 'right', mono: true },
  { key: 'pct', title: '涨跌幅', align: 'right', mono: true },
  { key: 'status', title: '状态', align: 'center' },
  { key: 'ok', title: '风控', align: 'center' },
]

const rows: DemoRow[] = [
  { symbol: '600519', name: '贵州茅台', price: 1683.5, pct: 2.31, status: '已成交', ok: true },
  { symbol: '300750', name: '宁德时代', price: 214.2, pct: -1.08, status: '部成', ok: true },
  { symbol: '002594', name: '比亚迪', price: 268.04, pct: 5.66, status: '已报', ok: false },
  { symbol: '601127', name: '赛力斯', price: 98.77, pct: -3.42, status: '已撤', ok: true },
]

/* ---------- viz 示例数据 ---------- */
const spark = [12, 14, 13, 17, 16, 19, 18, 22, 21, 25, 24, 28]

const depth = (base: number, step: number, vol: number): DepthLevel[] =>
  Array.from({ length: 5 }, (_, i) => ({ price: +(base + step * i).toFixed(2), volume: vol - i * 130 }))

const asks = depth(10.36, 0.01, 940)
const bids = depth(10.34, -0.01, 1120)

const sectors: SectorRow[] = [
  { name: '半导体', avgPct: 3.24 },
  { name: 'AI 算力', avgPct: 2.87 },
  { name: '机器人', avgPct: 1.45 },
  { name: '白酒', avgPct: -0.62 },
  { name: '地产', avgPct: -2.11 },
]

const xLabels = ['09:30', '10:00', '10:30', '11:00', '13:30', '14:00', '14:30']
const yLabels = ['半导体', 'AI 算力', '机器人', '汽车', '军工']
const cells: HeatCell[] = yLabels.flatMap((y, yi) =>
  xLabels.map((x, xi) => ({ x, y, value: +(((yi + 1) * (xi + 1)) % 7 - 3 + Math.sin(xi + yi)).toFixed(2) })),
)

const days = Array.from({ length: 30 }, (_, i) => {
  const d = new Date(Date.UTC(2026, 8, 1 + i))
  return d.toISOString().slice(0, 10)
})

const equity: EquityPoint[] = days.map((day, i) => ({
  day,
  equity: +(1 + i * 0.012 + Math.sin(i / 3) * 0.03).toFixed(4),
  benchmark: +(1 + i * 0.004 + Math.cos(i / 4) * 0.015).toFixed(4),
  drawdown: +(-Math.abs(Math.sin(i / 5)) * 0.04).toFixed(4),
}))

const indicators: IndicatorPoint[] = days.map((day, i) => ({
  time: day,
  macd: +(Math.sin(i / 4) * 0.35).toFixed(3),
  rsi6: +(50 + Math.sin(i / 3) * 22).toFixed(1),
}))

const bars: Bar[] = days.map((day, i) => {
  const base = 10 + Math.sin(i / 4) * 1.2 + i * 0.04
  const close = +(base + Math.cos(i / 3) * 0.35).toFixed(2)
  const open = +base.toFixed(2)
  return {
    time: day,
    open,
    close,
    high: +(Math.max(open, close) + 0.18).toFixed(2),
    low: +(Math.min(open, close) - 0.18).toFixed(2),
    volume: Math.round(8e5 * (1 + ((i * 37) % 11) / 10)),
  }
})

/* ---------- display 示例数据 ---------- */
const timeline: TimelineEntry[] = [
  { id: 1, time: '09:15', title: '竞价异动', body: '半导体板块竞价量比 3.2', tone: 'accent', tag: 'M02' },
  { id: 2, time: '09:31', title: '信号触发', body: '600519 综合分 88，入场 1680–1685', tone: 'up', tag: '信号' },
  { id: 3, time: '10:02', title: '风控拦截', body: '单票集中度超限，降级为半仓', tone: 'warn', tag: 'R07' },
  { id: 4, time: '14:56', title: '尾盘跳水预警', body: '炸板率升至 38%', tone: 'down', tag: '情绪' },
]

const funnel: FunnelStage[] = [
  { label: '候选池', value: 5120, hint: '全市场' },
  { label: '初筛', value: 486, hint: '量价异动' },
  { label: 'AI 评分', value: 97, hint: '四维 ≥70' },
  { label: '风控通过', value: 23, hint: 'R01–R15' },
  { label: '下单', value: 8, hint: '模拟盘' },
]

const tiers: LadderTier[] = [
  { height: 3, stocks: [{ symbol: '002594', name: '比亚迪', sealAmount: 4.2e8, openCount: 0, status: '一字' }] },
  {
    height: 2,
    stocks: [
      { symbol: '601127', name: '赛力斯', sealAmount: 1.8e8, openCount: 1, status: '换手板' },
      { symbol: '300750', name: '宁德时代', sealAmount: 9.6e7, openCount: 2, status: '炸板回封' },
    ],
  },
  {
    height: 1,
    stocks: [
      { symbol: '600519', name: '贵州茅台', sealAmount: 6.1e7, openCount: 0, status: '首板' },
      { symbol: '000858', name: '五粮液', sealAmount: 3.3e7, openCount: 3, status: '首板' },
    ],
  },
]

/* ---------- 令牌色板 ---------- */
const tokenGroups: Array<{ title: string; tokens: string[] }> = [
  { title: '背景阶梯', tokens: ['--nt-bg-0', '--nt-bg-1', '--nt-bg-2', '--nt-bg-3', '--nt-bg-glass'] },
  { title: '文本阶梯', tokens: ['--nt-text-1', '--nt-text-2', '--nt-text-3'] },
  { title: '边框', tokens: ['--nt-border-1', '--nt-border-2'] },
  {
    title: '语义色',
    tokens: ['--nt-accent', '--nt-accent-2', '--nt-warn', '--nt-danger', '--nt-success', '--nt-info'],
  },
  { title: '涨跌（红涨绿跌）', tokens: ['--nt-up', '--nt-down'] },
]

/* ---------- 监控画布（静态场景） ---------- */
const stageSelected = ref('')
const stageNow = Date.now()
const stageGraph = buildScene(
  {
    ...emptyMonitorInput(),
    now: stageNow,
    sources: {
      ok: true,
      items: [
        { id: 'tdx', name: '通达信', status: 'live', detail: '', lastAt: '' },
        { id: 'rss', name: '新闻快讯', status: 'live', detail: '', lastAt: '' },
        { id: 'fund', name: '资金流入', status: 'live', detail: '', lastAt: '' },
        { id: 'overseas', name: '外围联动', status: 'gap', detail: '昨夜未更新', lastAt: '' },
      ],
    },
    sentiment: { ok: true, phase: 'WARM', up: 3021, down: 1502, asOf: '', stale: false },
    signals: {
      ok: true,
      items: [
        {
          id: 's1',
          symbol: '600519',
          name: '贵州茅台',
          score: 88,
          highValue: true,
          degraded: false,
          status: 'pending',
          evidence: [],
          dims: {},
          entry: 1682,
          stop: 1650,
          take: 1736,
          time: '',
        },
      ],
    },
    account: { ok: true, book: 'SIM', equity: 1284902.44, cash: 412205.1, dayPnl: 18204.1, positions: [{ symbol: '600519', quantity: 100, avgCost: 1680, lastPrice: 1683.5 }] },
    risk: { ok: true, mode: 'LIVE', killActive: false, killReason: '', buysToday: 2, marketBreaker: '' },
    brainStats: {
      ok: true,
      tiers: [
        { tier: 'large', model: 'qwen-32b', configured: true, circuitOpen: false, calls: 12, failures: 0, p95Ms: 1820, inFlight: 1 },
        { tier: 'small', model: 'qwen-4b', configured: true, circuitOpen: false, calls: 214, failures: 1, p95Ms: 86, inFlight: 0 },
      ],
    },
    pulses: [
      { id: 'gp1', from: 'service:market', to: 'post:scan', identifier: { kind: 'symbol', value: '600519' }, at: stageNow },
      { id: 'gp2', from: 'post:model', to: 'post:desk', identifier: { kind: 'signal', value: '88' }, at: stageNow },
      { id: 'gp3', from: 'post:risk', to: 'book:center', identifier: { kind: 'rule', value: 'R01' }, at: stageNow },
    ],
  },
  stageNow,
)
</script>

<template>
  <div class="gallery">
    <header class="g-head">
      <div>
        <h1>组件矩阵</h1>
        <p class="g-note">/dev/gallery · 仅 DEV 构建 · 静态数据，零后端依赖</p>
      </div>
      <div class="g-prefs">
        <NtSegmented
          :model-value="settings.theme"
          :options="[
            { value: 'dark', label: '暗色' },
            { value: 'light', label: '亮色' },
          ]"
          @update:model-value="settings.setTheme($event as ThemeName)"
        />
        <NtSegmented
          :model-value="settings.density"
          :options="[
            { value: 'normal', label: '标准' },
            { value: 'compact', label: '紧凑' },
          ]"
          @update:model-value="settings.setDensity($event as DensityName)"
        />
      </div>
    </header>

    <section class="g-sec">
      <h2>设计令牌</h2>
      <div v-for="group in tokenGroups" :key="group.title" class="g-tokens">
        <span class="g-tokens__title">{{ group.title }}</span>
        <div class="g-tokens__row">
          <div v-for="token in group.tokens" :key="token" class="g-swatch">
            <i :style="{ background: `var(${token})` }" />
            <code>{{ token }}</code>
          </div>
        </div>
      </div>
    </section>

    <section class="g-sec">
      <h2>按钮</h2>
      <div class="g-row">
        <NtButton variant="primary">primary</NtButton>
        <NtButton>ghost</NtButton>
        <NtButton variant="danger">danger</NtButton>
        <NtButton variant="success">success</NtButton>
        <NtButton variant="quiet">quiet</NtButton>
        <NtButton size="sm" variant="primary">sm</NtButton>
        <NtButton loading>loading</NtButton>
        <NtButton disabled>disabled</NtButton>
        <NtButton icon="refresh" variant="quiet">带图标</NtButton>
      </div>
    </section>

    <section class="g-sec">
      <h2>表单</h2>
      <div class="g-form">
        <NtInput v-model="input" label="代码/名称" placeholder="搜索" icon="search" />
        <NtInput model-value="600519" label="等宽" mono invalid hint="格式不正确" />
        <NtInput model-value="只读演示" label="禁用" type="text" />
        <NtSelect
          v-model="select"
          label="市场"
          :options="[
            { value: 'sh', label: '沪市' },
            { value: 'sz', label: '深市' },
            { value: 'bj', label: '北交所', disabled: true },
          ]"
        />
        <NtSwitch v-model="toggle" label="实时推送" />
        <NtSegmented
          v-model="segment"
          :options="[
            { value: '1m', label: '分时' },
            { value: '1d', label: '日线' },
            { value: '1w', label: '周线' },
          ]"
        />
        <NtTabs
          v-model="tab"
          :items="[
            { value: 'positions', label: '持仓' },
            { value: 'orders', label: '委托' },
            { value: 'fills', label: '成交' },
          ]"
        />
      </div>
    </section>

    <section class="g-sec">
      <h2>徽标 / 状态 / 反馈</h2>
      <div class="g-row">
        <NtBadge>neutral</NtBadge>
        <NtBadge tone="accent">accent</NtBadge>
        <NtBadge tone="success" dot>success</NtBadge>
        <NtBadge tone="warn">warn</NtBadge>
        <NtBadge tone="danger" dot>danger</NtBadge>
        <NtBadge tone="info">info</NtBadge>
      </div>
      <div class="g-row">
        <NtStatusDot status="live" label="live" />
        <NtStatusDot status="ok" label="ok" />
        <NtStatusDot status="stale" label="stale" />
        <NtStatusDot status="down" label="down" />
        <NtStatusDot status="off" label="off" />
      </div>
      <div class="g-row">
        <NtProgressRing :value="72" />
        <NtProgressRing :value="88" hot label="AI" />
        <NtProgressRing :value="null" />
        <NtTooltip text="200ms 延迟浮现，Esc/移出关闭"><NtButton size="sm" variant="quiet">悬停我</NtButton></NtTooltip>
        <NtButton size="sm" @click="modalOpen = true">打开 Modal</NtButton>
        <NtButton size="sm" variant="quiet" @click="toast.info('信息 toast', '来自 useToast')">toast info</NtButton>
        <NtButton size="sm" variant="success" @click="toast.success('下单成功', '600519 买入 100 股')">toast success</NtButton>
        <NtButton size="sm" variant="quiet" @click="toast.warn('风控降级', '单票集中度超限')">toast warn</NtButton>
        <NtButton size="sm" variant="danger" @click="toast.error('链路中断', 'WS 2003 重连中')">toast error</NtButton>
      </div>
      <div class="g-row">
        <span>红涨绿跌：</span>
        <b :class="pnlClass(2.31)">{{ pnlSign(2.31) }}2.31%</b>
        <b :class="pnlClass(-1.08)">{{ pnlSign(-1.08) }}1.08%</b>
        <b :class="pnlClass(0)">0.00%</b>
      </div>
    </section>

    <section class="g-sec">
      <h2>图标（{{ iconNames.length }} 枚）</h2>
      <div class="g-icons">
        <span v-for="name in iconNames" :key="name" class="g-icon" :title="name">
          <NtIcon :name="name" :size="18" />
          <code>{{ name }}</code>
        </span>
      </div>
    </section>

    <section class="g-sec">
      <h2>表格（sticky 表头 + cell 插槽 + 密度）</h2>
      <NtTable :columns="columns" :rows="rows" row-key="symbol" clickable>
        <template #cell-pct="{ row }">
          <span :class="pnlClass(row.pct)">{{ pnlSign(row.pct) }}{{ Math.abs(row.pct).toFixed(2) }}%</span>
        </template>
        <template #cell-status="{ row }">
          <NtBadge :tone="row.status === '已成交' ? 'success' : row.status === '已撤' ? 'neutral' : 'info'">
            {{ row.status }}
          </NtBadge>
        </template>
        <template #cell-ok="{ row }">
          <NtBadge :tone="row.ok ? 'success' : 'danger'" dot>{{ row.ok ? '通过' : '拦截' }}</NtBadge>
        </template>
      </NtTable>
    </section>

    <section class="g-sec">
      <h2>viz · 轻量 SVG</h2>
      <div class="g-viz">
        <div class="g-card">
          <h3>NtSparkline</h3>
          <NtSparkline :values="spark" :width="160" :height="36" />
        </div>
        <div class="g-card">
          <h3>NtGauge</h3>
          <div class="g-row">
            <NtGauge :value="64" label="情绪" unit="°" tone="accent" :size="92" />
            <NtGauge :value="82" label="炸板率" unit="%" tone="warn" :size="92" />
            <NtGauge :value="93" label="集中度" unit="%" tone="danger" :size="92" />
          </div>
        </div>
        <div class="g-card">
          <h3>NtRadar（四维）</h3>
          <NtRadar
            :dims="[
              { label: '技术面', value: 82 },
              { label: '消息面', value: 65 },
              { label: '资金面', value: 90 },
              { label: '外围', value: 48 },
            ]"
            :size="120"
          />
        </div>
        <div class="g-card">
          <h3>NtProgressRing</h3>
          <div class="g-row">
            <NtProgressRing :value="41" :size="44" />
            <NtProgressRing :value="88" :size="44" hot />
          </div>
        </div>
      </div>
    </section>

    <section class="g-sec">
      <h2>viz · ECharts / 图表</h2>
      <div class="g-viz">
        <div class="g-card g-card--wide">
          <h3>NtKline（40 根示例日线）</h3>
          <NtKline :bars="bars" freq="1d" height="280px" />
        </div>
        <div class="g-card g-card--wide">
          <h3>NtEquityCurve</h3>
          <NtEquityCurve :points="equity" height="240px" />
        </div>
        <div class="g-card">
          <h3>NtSectorBar</h3>
          <NtSectorBar :rows="sectors" height="200px" />
        </div>
        <div class="g-card">
          <h3>NtHeatmap</h3>
          <NtHeatmap :cells="cells" :x-labels="xLabels" :y-labels="yLabels" height="200px" />
        </div>
        <div class="g-card g-card--wide">
          <h3>NtIndicatorChart（MACD / RSI6）</h3>
          <NtIndicatorChart :points="indicators" height="180px" />
        </div>
        <div class="g-card">
          <h3>NtDepthBoard（五档，卖红买绿）</h3>
          <NtDepthBoard :asks="asks" :bids="bids" />
        </div>
      </div>
    </section>

    <section class="g-sec">
      <h2>display</h2>
      <div class="g-viz">
        <div class="g-card g-card--wide">
          <h3>NtStatCard</h3>
          <div class="g-stats">
            <NtStatCard label="账户权益" value="1,284,902.44" sub="模拟盘 · D12" tone="neutral" hero />
            <NtStatCard label="当日盈亏" value="+18,204.10" sub="+1.44%" tone="up" />
            <NtStatCard label="最大回撤" value="-4.2%" sub="近 20 日" tone="down" />
            <NtStatCard label="AI 信号" value="88" sub="≥85 高亮" tone="accent" />
            <NtStatCard label="炸板率" value="38%" sub="情绪退潮" tone="warn" />
            <NtStatCard label="风控拦截" value="3" sub="今日" tone="danger" />
          </div>
        </div>
        <div class="g-card">
          <h3>NtTimeline</h3>
          <NtTimeline :items="timeline" />
        </div>
        <div class="g-card">
          <h3>NtFunnel</h3>
          <NtFunnel :stages="funnel" />
        </div>
        <div class="g-card">
          <h3>NtLadderTiers（涨停梯队）</h3>
          <NtLadderTiers :tiers="tiers" />
        </div>
      </div>
    </section>

    <section class="g-sec">
      <h2>监控画布（CanvasStage · 静态场景，可拖拽/缩放）</h2>
      <div class="g-stage">
        <CanvasStage :graph="stageGraph" :selected-id="stageSelected" :hot-ids="[]" :paused="false" @select="stageSelected = $event" />
      </div>
    </section>

    <NtModal v-model:open="modalOpen" title="示例 Modal" width="420px">
      <p class="g-modal-body">
        Modal 走 useOverlay 的 z 栈 + Esc 链，Teleport 到 body。玻璃拟态允许出现的位置之一。
      </p>
      <template #footer>
        <NtButton variant="quiet" @click="modalOpen = false">取消</NtButton>
        <NtButton variant="primary" @click="modalOpen = false">确定</NtButton>
      </template>
    </NtModal>
  </div>
</template>

<style scoped>
.gallery {
  min-height: 100vh;
  padding: 16px 20px 48px;
  background: var(--nt-bg-0);
  color: var(--nt-text-1);
  overflow: auto;
}

.g-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--nt-border-1);
}

.g-head h1 {
  margin: 0;
  font-size: 17px;
}

.g-note {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--nt-text-3);
}

.g-prefs {
  display: flex;
  gap: 10px;
}

.g-sec {
  margin-top: 22px;
}

.g-sec h2 {
  margin: 0 0 10px;
  font-size: 13.5px;
  color: var(--nt-text-2);
  font-weight: 600;
}

.g-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 10px;
  font-size: 13px;
}

.g-form {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
  max-width: 980px;
}

.g-tokens {
  margin-bottom: 10px;
}

.g-tokens__title {
  display: block;
  font-size: 11.5px;
  color: var(--nt-text-3);
  margin-bottom: 4px;
}

.g-tokens__row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.g-swatch {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-1);
}

.g-swatch i {
  width: 18px;
  height: 18px;
  border-radius: 4px;
  border: 1px solid var(--nt-border-2);
}

.g-swatch code {
  font-family: var(--nt-font-mono);
  font-size: 10.5px;
  color: var(--nt-text-2);
}

.g-icons {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
  gap: 6px;
}

.g-icon {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 8px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-1);
  color: var(--nt-text-2);
}

.g-icon code {
  font-family: var(--nt-font-mono);
  font-size: 10px;
}

.g-viz {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 12px;
}

.g-card {
  padding: 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  min-width: 0;
}

.g-card--wide {
  grid-column: 1 / -1;
}

.g-card h3 {
  margin: 0 0 8px;
  font-size: 12px;
  color: var(--nt-text-3);
  font-weight: 600;
}

.g-stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: 10px;
}

.g-stage {
  height: 480px;
  position: relative;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  background: var(--nt-bg-1);
  overflow: hidden;
}

.g-modal-body {
  margin: 0;
  font-size: 13px;
  color: var(--nt-text-2);
  line-height: 1.7;
}
</style>
