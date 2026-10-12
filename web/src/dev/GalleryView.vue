<script setup lang="ts">
/**
 * /dev/gallery —— 组件矩阵走查页（仅 DEV 构建）。
 * Nt* 全组件 × 主题（暗/亮）× 密度（标准/紧凑），静态示例数据，零后端依赖。
 * 每阶段收尾跑一遍，保证令牌与组件改动可视。
 */
import { onBeforeUnmount, onMounted, ref } from 'vue'
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
import DetailSheet from '@/monitor/DetailSheet.vue'
import { demoActivityCells } from '@/monitor/activity'
import { buildDetail } from '@/monitor/detail'
import { computeHealth, healthTone } from '@/monitor/health'
import { resolveHud } from '@/monitor/hud'
import { emptyMonitorInput } from '@/monitor/load-input'
import { buildScene, pushPulse } from '@/monitor/scene-model'
import Sparkline from '@/monitor/Sparkline.vue'
import { TapeSession } from '@/monitor/tape'
import TerminalStrip from '@/monitor/TerminalStrip.vue'
import { bandCountdown, bandSegments, bandSpec, hudCells } from '@/monitor/timeband'
import '@/monitor/monitor.css'
import type { Bar } from '@/api/market'
import type { EquityPoint } from '@/api/backtest'
import type { HealthPart, HealthScore } from '@/monitor/health'
import type { EdgeFlow } from '@/monitor/flow'
import type { MonitorInput, Pulse, SceneGraph } from '@/monitor/types'
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
    /* S3 漏斗光带静态示例数据：初筛 12 → AI≥70 5 → 风控通过 2 → 已下单 2 → 成交 1，
     * 侧漏两组（没进模型前 30 ×4 + 没有行情 ×3；综合分低于 75 ×3）+ 未成交 ×1；
     * 全市场层没有只读出口，恒画「未返回」虚线段。 */
    candidates: {
      ok: true,
      items: [
        { symbol: '600519', name: '贵州茅台', strategy: 'limit_up', stage: 'selected', missReason: '', ruleScore: 86, aiScore: 91, finalScore: 88 },
        { symbol: '000001', name: '平安银行', strategy: 'limit_up', stage: 'selected', missReason: '', ruleScore: 80, aiScore: 88, finalScore: 84 },
        { symbol: '300750', name: '宁德时代', strategy: 'limit_up', stage: 'ai_scored', missReason: 'below_threshold', ruleScore: 72, aiScore: 81, finalScore: 76 },
        { symbol: '601318', name: '中国平安', strategy: 'limit_up', stage: 'ai_scored', missReason: 'below_threshold', ruleScore: 70, aiScore: 74, finalScore: 71 },
        { symbol: '002466', name: '天齐锂业', strategy: 'limit_up', stage: 'ai_scored', missReason: 'below_threshold', ruleScore: 68, aiScore: 72, finalScore: 69 },
        { symbol: '600036', name: '招商银行', strategy: 'limit_up', stage: 'ranked', missReason: 'not_in_ai_top', ruleScore: 66, aiScore: null, finalScore: null },
        { symbol: '601899', name: '紫金矿业', strategy: 'limit_up', stage: 'ranked', missReason: 'not_in_ai_top', ruleScore: 64, aiScore: null, finalScore: null },
        { symbol: '000628', name: '高新发展', strategy: 'limit_up', stage: 'ranked', missReason: 'not_in_ai_top', ruleScore: 62, aiScore: null, finalScore: null },
        { symbol: '600900', name: '长江电力', strategy: 'limit_up', stage: 'ranked', missReason: 'not_in_ai_top', ruleScore: 61, aiScore: null, finalScore: null },
        { symbol: '601088', name: '中国神华', strategy: 'limit_up', stage: 'ranked', missReason: 'no_quote', ruleScore: null, aiScore: null, finalScore: null },
        { symbol: '600028', name: '中国石化', strategy: 'limit_up', stage: 'ranked', missReason: 'no_quote', ruleScore: null, aiScore: null, finalScore: null },
        { symbol: '601857', name: '中国石油', strategy: 'limit_up', stage: 'ranked', missReason: 'no_quote', ruleScore: null, aiScore: null, finalScore: null },
      ],
    },
    orders: {
      ok: true,
      items: [
        { id: 'go1', symbol: '600519', side: 'buy', status: 'FILLED', price: 1682, volume: 100, reason: 'signal:s1' },
        { id: 'go2', symbol: '000001', side: 'buy', status: 'PARTIAL', price: 11.2, volume: 500, reason: 'signal:s2' },
        { id: 'go3', symbol: '600519', side: 'sell', status: 'FILLED', price: 1690, volume: 100, reason: 'take_profit' },
      ],
    },
    fills: { ok: true, items: [{ id: 'gf1', symbol: '600519', side: 'buy', price: 1682, qty: 100, time: '' }] },
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

/* S2 静态示例：给画布牌贴新鲜度角标与 60 格活动条（走真实渲染路径，不是平行实现） */
const demoCellsBusy = demoActivityCells('busy')
const demoCellsQuiet = demoActivityCells('quiet')
const stageGraphS2 = {
  ...stageGraph,
  nodes: stageGraph.nodes.map((node) => {
    if (node.id === 'source:tdx') return { ...node, fresh: { text: '3s 前', tone: 'ok' as const }, activity: demoCellsBusy }
    if (node.id === 'book:center') return { ...node, fresh: { text: '5s 前', tone: 'ok' as const }, activity: demoCellsBusy }
    if (node.id === 'post:scan') return { ...node, fresh: { text: '2m 前', tone: 'warn' as const }, activity: demoCellsQuiet }
    if (node.id === 'model:large') return { ...node, fresh: { text: '12m 前', tone: 'danger' as const }, activity: demoCellsQuiet }
    if (node.id === 'source:overseas') return { ...node, fresh: { text: '上一交易日', tone: 'warn' as const }, activity: demoCellsQuiet }
    return node
  }),
}

/* S2 原语对照表：活动条三态 × 角标三档 */
const demoActivityRows = [
  { label: '在跑（连续有活动）', cells: demoCellsBusy, badge: '3s 前', tone: 'ok' },
  { label: '一顿一顿（零星活动）', cells: demoCellsQuiet, badge: '2m 前', tone: 'warn' },
  { label: '静默（全灰）+ 变红角标', cells: demoCellsQuiet.map(() => false as const), badge: '12m 前', tone: 'danger' },
]

/* ---------- 监控 S6：束线流量宽度 + 高分脉冲 + 急停态（活的画布示例） ---------- */
/* 流量表直接给边 id（监控页里由 flow.ts 的差分/脉冲计数产出，这里静态摆出各档样子） */
const demoFlow: Record<string, EdgeFlow> = {
  'source:tdx>post:scan': { rate: 420, stalled: false, circuit: false },
  'source:tdx>dim:tech': { rate: 420, stalled: false, circuit: false },
  'source:rss>dim:news': { rate: 12, stalled: false, circuit: false },
  /* 资金流整窗计数没动 → 断流：束线照画，流向点停住转 --nt-warn α0.35 */
  'source:fund>dim:capital': { rate: 0, stalled: true, circuit: false },
  /* 外围没有可信观测 → rate null → 固定 1.3px，不猜 */
  'source:overseas>dim:overseas': { rate: null, stalled: false, circuit: false },
  'model:small>dim:tech': { rate: 95, stalled: false, circuit: false },
  'model:small>dim:capital': { rate: 95, stalled: false, circuit: false },
  /* 大模型熔断：它到三个维度的边转 --nt-danger α0.4 并停流向点 */
  'model:large>dim:news': { rate: 6, stalled: false, circuit: true },
  'model:large>dim:overseas': { rate: 6, stalled: false, circuit: true },
  'model:large>dim:synth': { rate: 6, stalled: false, circuit: true },
  'post:risk>book:center': { rate: 2, stalled: false, circuit: false },
}
/* ≥85 的信号 id/symbol → 升级脉冲（发光预算内的五个对象之一） */
const demoScores: Record<string, number> = { s1: 88, '600519': 88 }

const stageS6 = ref<SceneGraph>({ ...stageGraphS2, pulses: [] })
let s6Timer = 0
let s6Flip = false

onMounted(() => {
  s6Timer = window.setInterval(() => {
    const at = Date.now()
    s6Flip = !s6Flip
    const pulse: Pulse = { id: `gs6:${at}`, from: 'post:model', to: 'post:desk', identifier: { kind: 'signal', value: '600519' }, at }
    stageS6.value = {
      ...stageS6.value,
      pulses: pushPulse(stageS6.value.pulses, pulse, at),
      /* 综合分每拍翻一下（88↔86），走真渲染路径演示「数字变化闪」 */
      nodes: stageS6.value.nodes.map((node) => (node.id === 'dim:synth' ? { ...node, face: s6Flip ? '88' : '86' } : node)),
    }
  }, 1600)
})

onBeforeUnmount(() => {
  if (s6Timer) window.clearInterval(s6Timer)
})

/* ---------- 监控 S1 原语：时段带 / 健康分 / 十格 HUD（静态） ---------- */
const demoNow = new Date(2026, 9, 12, 10, 24).getTime()
const demoInput = {
  ...emptyMonitorInput(),
  now: demoNow,
  sentiment: {
    ok: true,
    phase: '上午',
    up: 3021,
    down: 1502,
    asOf: new Date(demoNow - 4000).toISOString(),
    stale: false,
    brokenRate: 0.324,
    maxHeight: 5,
  },
  limits: {
    ok: true,
    rows: [
      { symbol: '600519', consecutive: 2, status: 'sealed' },
      { symbol: '000001', consecutive: 5, status: 'sealed' },
      { symbol: '300750', consecutive: 1, status: 'sealed' },
      { symbol: '601318', consecutive: 1, status: 'sealed' },
    ],
  },
  sectors: {
    ok: true,
    rows: [
      { name: '固态电池', avgPct: 3.42, overseas: 1.2, leader: '002466' },
      { name: '算力租赁', avgPct: 1.85, overseas: null, leader: '000628' },
    ],
  },
  signals: {
    ok: true,
    items: [
      {
        id: 'gs1',
        symbol: '600519',
        name: '贵州茅台',
        score: 88,
        highValue: true,
        degraded: false,
        status: 'risk_checking',
        evidence: [],
        dims: {},
        entry: 1682,
        stop: 1650,
        take: 1736,
        time: '10:12',
      },
    ],
  },
  risk: { ok: true, mode: 'SIM', killActive: false, killReason: '', buysToday: 1, marketBreaker: '' },
  brainStats: {
    ok: true,
    tiers: [
      { tier: 'large', model: 'qwen-32b', configured: true, circuitOpen: false, calls: 12, failures: 0, p95Ms: 1820 },
      { tier: 'small', model: 'qwen-4b', configured: true, circuitOpen: false, calls: 214, failures: 1, p95Ms: 86 },
    ],
  },
  ops: {
    ok: true,
    services: [
      { name: 'admin', status: 'ok', detail: '' },
      { name: 'brain', status: 'ok', detail: '' },
      { name: 'nats', status: 'up', detail: '' },
    ],
    sources: [],
    resources: [{ name: '显存 GPU0', status: 'ok', detail: '显存 GPU0 已用 6104 MiB / 共 8188 MiB' }],
  },
  sources: {
    ok: true,
    items: [{ id: 'tdx', name: '通达信', status: 'live', detail: '', lastAt: new Date(demoNow - 5000).toISOString() }],
  },
  account: {
    ok: true,
    book: 'SIM',
    equity: 1284902.44,
    cash: 412205.1,
    dayPnl: 18204.1,
    positions: [{ symbol: '600519', quantity: 100, avgCost: 1680, lastPrice: 1683.5 }],
  },
}
const demoSegments = bandSegments('morning')
const demoSpec = bandSpec('morning')
const demoCountdown = bandCountdown(new Date(demoNow), 'morning')
const demoHealth = computeHealth(demoInput, demoNow)
const demoHud = resolveHud(demoInput, hudCells('morning'), demoNow)
const demoSpark = (values: number[]) => values as (number | null)[]
const demoSparks: Record<string, (number | null)[]> = {
  limit: demoSpark([2, 3, 3, 4, 4, 5, 4, 6, 5, 4]),
  broken: demoSpark([22, 24, 27, 26, 30, 31, 29, 32, 32, 32]),
  signal: demoSpark([0, 1, 1, 0, 2, 1, 1, 1, 0, 1]),
  p95: demoSpark([1200, 1450, 1380, 1600, 1820, 1750, 1900, 1820, 1700, 1820]),
  equity: demoSpark([4200, 8600, 6400, 11200, 9800, 13400, 15100, 12800, 16900, 18204]),
}

/* ---------- 监控 S4 原语：事件流 TAPE 与五格终端带（静态） ---------- */
const demoTapeInput: MonitorInput = {
  ...demoInput,
  notices: {
    ok: true,
    items: [
      { id: 'gn1', title: '通达信行情 42 秒没有新数据', level: 'warn', time: '2026-10-12 10:23:58' },
      { id: 'gn2', title: '回测任务 queue 堆积 3 条', level: 'info', time: '2026-10-12 10:21:04' },
    ],
  },
  audit: {
    ok: true,
    items: [
      { id: 'ga1', action: 'kill_switch_arm', target: 'account SIM', time: '2026-10-12 10:23:41' },
      { id: 'ga2', action: 'order_submit', target: '600519 买入 100', time: '2026-10-12 10:23:12' },
      { id: 'ga3', action: 'brain_call', target: 'large Kimi-32b', time: '2026-10-12 10:22:57' },
    ],
  },
  fills: {
    ok: true,
    items: [
      { id: 'gf1', symbol: '600519', side: 'buy', price: 1682, qty: 100, time: '2026-10-12 10:23:20' },
      { id: 'gf2', symbol: '000001', side: 'sell', price: 11.24, qty: 500, time: '2026-10-12 10:19:03' },
    ],
  },
  /* 尾盘「持仓明细」格示例（§5.1）：一只距止损不足 2% 转橙、一只已跌破止损转红、
   * 一只没有对齐的信号止损价 → 写「止损价未对齐」，不拿别的价冒充 */
  signals: {
    ok: true,
    items: [
      { id: 'gs1', symbol: '600519', name: '贵州茅台', score: 88, highValue: true, degraded: false, status: 'risk_checking', evidence: [], dims: {}, entry: 1682, stop: 1650, take: 1736, time: '10:12' },
      { id: 'gs2', symbol: '000001', name: '平安银行', score: 76, highValue: false, degraded: false, status: 'approved', evidence: [], dims: {}, entry: 11.3, stop: 11.5, take: 11.95, time: '10:18' },
    ],
  },
  account: {
    ok: true,
    book: 'SIM',
    equity: 1284902.44,
    cash: 412205.1,
    dayPnl: 18204.1,
    positions: [
      { symbol: '600519', quantity: 100, avgCost: 1680, lastPrice: 1683.5 },
      { symbol: '000001', quantity: 500, avgCost: 11.6, lastPrice: 11.24 },
      { symbol: '300750', quantity: 200, avgCost: 214.8, lastPrice: 214.2 },
    ],
  },
  orders: {
    ok: true,
    items: [
      { id: 'go1', symbol: '600519', side: 'buy', status: 'FILLED', price: 1682, volume: 100, reason: 'signal:gs1' },
      { id: 'go2', symbol: '000001', side: 'buy', status: 'PARTIAL', price: 11.2, volume: 500, reason: 'signal:gs2' },
    ],
  },
  candidates: {
    ok: true,
    items: [
      { symbol: '600519', name: '贵州茅台', strategy: 'limit_up', stage: 'selected', missReason: '', ruleScore: 86, aiScore: 91, finalScore: 88 },
      { symbol: '300750', name: '宁德时代', strategy: 'limit_up', stage: 'ai_scored', missReason: 'below_threshold', ruleScore: 72, aiScore: 81, finalScore: 76 },
      { symbol: '600036', name: '招商银行', strategy: 'limit_up', stage: 'ranked', missReason: 'not_in_ai_top', ruleScore: 66, aiScore: null, finalScore: null },
      { symbol: '601088', name: '中国神华', strategy: 'limit_up', stage: 'ranked', missReason: 'no_quote', ruleScore: null, aiScore: null, finalScore: null },
    ],
  },
  tasks: {
    ok: true,
    items: [
      { id: 'gt1', name: '回测 涨停接力 2026Q3', status: 'running', message: '第 42/120 天' },
      { id: 'gt2', name: '数据补全 kline', status: 'done', message: '补 12 条' },
      { id: 'gt3', name: '影子对照 T+3', status: 'queued', message: '' },
    ],
  },
  admission: { ok: true, ready: false, tradingDays: 12, requiredDays: 20, items: [] },
  shadow: { ok: true, items: [{ name: 'T+1', live: 1.02, shadow: 0.98 }] },
  brainStats: {
    ok: true,
    tiers: [
      { tier: 'large', model: 'Kimi-32b', configured: true, circuitOpen: false, calls: 12, failures: 0, p95Ms: 1820, inFlight: 1, queued: 2, kvCacheUsage: 0.62 },
      { tier: 'small', model: 'Kimi-4b', configured: true, circuitOpen: true, calls: 214, failures: 7, p95Ms: 86, inFlight: 0, queued: 0, kvCacheUsage: 0.91 },
    ],
  },
}

/* ---------- 监控 S5 详情窗深化（§5.1–5.6，静态） ---------- */
/* 用 demoTapeInput 当底座（它字段最全），补 S5 专用字段：止损触发、风险参数、日报、质量问题、
   T+1 后验，让六张牌的缺口文案与色阶一次走查完 */
const demoDeepInput: MonitorInput = {
  ...demoTapeInput,
  risk: {
    ok: true,
    mode: 'SIM',
    killActive: false,
    killReason: '',
    buysToday: 8,
    marketBreaker: '',
    params: { 'risk.max_daily_buys': '10', 'risk.max_single_pct': '0.05', 'risk.max_total_pct': '0.8', 'risk.daily_loss_pct': '0.02', 'risk.stop_pct': '0.05' },
  },
  riskReport: {
    ok: true,
    date: '2026-10-12',
    summary: '拦截 3 笔，缩量 1 笔',
    checks: 46,
    approved: 42,
    rejected: 3,
    adjusted: 1,
    p99Ms: 41,
    rejects: [
      { rule: 'R10', name: '当日开仓笔数', count: 2 },
      { rule: 'R03', name: '可交易', count: 1 },
    ],
    events: [{ kind: 'kill_switch', active: true, mode: 'SIM', source: 'manual', reason: '手动急停演练', operator: 'admin', at: '2026-10-12 09:45' }],
  },
  sources: {
    ok: true,
    items: [
      {
        id: 'tdx',
        name: '通达信',
        status: 'live',
        detail: '成功率 98%',
        lastAt: new Date(demoNow - 5000).toISOString(),
        kind: 'daily_bar',
        enabled: true,
        successRate: 0.98,
        successCount: 490,
        failureCount: 10,
        latencyMs: 120,
        avgLatencyMs: 210,
        consecutiveFailures: 0,
        quotaLimit: 0,
        lastError: '',
      },
    ],
  },
  quality: {
    ok: true,
    items: [
      { id: 'gq1', domain: 'daily_bar', checkName: 'daily_bar_missing', symbol: '000001', tradeDate: '2026-10-12', severity: 'high', source: 'tdx', otherSource: '', message: '缺当日日线', hits: 12, lastSeen: '2026-10-12 10:00' },
      { id: 'gq2', domain: 'daily_bar', checkName: 'tick_stale', symbol: '600519', tradeDate: '2026-10-12', severity: 'medium', source: 'tdx', otherSource: '', message: '快照过期', hits: 3, lastSeen: '2026-10-12 10:05' },
    ],
  },
  signals: {
    ok: true,
    items: [
      {
        id: 'gs1',
        symbol: '600519',
        name: '贵州茅台',
        score: 88,
        highValue: true,
        degraded: false,
        status: 'risk_checking',
        evidence: ['F:ma20=+2.3%', 'N:123', 'R07'],
        dims: { technical: 78, news: 62 },
        entry: 1682,
        stop: 1650,
        take: 1736,
        time: '10:12',
        ruleScore: 72,
        aiScore: 88,
        reason: '首板弱转强',
        entryLow: 1670,
        entryHigh: 1690,
        atr: 12.4,
        positionPct: 0.08,
        validUntil: new Date(demoNow + 30 * 60000).toISOString(),
        holdingDays: 3,
      },
      { id: 'gs2', symbol: '000001', name: '平安银行', score: 76, highValue: false, degraded: true, status: 'approved', evidence: [], dims: {}, entry: 11.3, stop: 11.5, take: 11.95, time: '10:18' },
    ],
  },
  account: {
    ok: true,
    book: 'SIM',
    equity: 1284902.44,
    cash: 412205.1,
    dayPnl: 18204.1,
    realized: 42150.3,
    unrealized: -3120.5,
    positions: [
      { symbol: '600519', quantity: 100, avgCost: 1680, lastPrice: 1683.5 },
      { symbol: '000001', quantity: 500, avgCost: 11.6, lastPrice: 11.24 },
    ],
  },
  orders: {
    ok: true,
    items: [
      { id: 'go1', symbol: '600519', side: 'buy', status: 'FILLED', price: 1682, volume: 100, filled: 100, reason: 'signal:gs1' },
      { id: 'go2', symbol: '000001', side: 'sell', status: 'PARTIAL', price: 11.3, volume: 500, filled: 500, reason: '' },
    ],
  },
  fills: {
    ok: true,
    items: [
      { id: 'go1', symbol: '600519', side: 'buy', price: 1683.1, qty: 60, time: '2026-10-12 10:20:01' },
      { id: 'go1', symbol: '600519', side: 'buy', price: 1683.3, qty: 40, time: '2026-10-12 10:20:03' },
      { id: 'go2', symbol: '000001', side: 'sell', price: 11.24, qty: 500, time: '2026-10-12 10:21:11' },
    ],
  },
  candidates: {
    ok: true,
    items: [
      { symbol: '600519', name: '贵州茅台', strategy: 'limit_up', stage: 'selected', missReason: '', ruleScore: 86, aiScore: 91, finalScore: 88 },
      { symbol: '300750', name: '宁德时代', strategy: 'limit_up', stage: 'ai_scored', missReason: 'below_threshold', ruleScore: 72, aiScore: 81, finalScore: 76, retT1: 0.061, hasT1: true },
      { symbol: '600036', name: '招商银行', strategy: 'limit_up', stage: 'ranked', missReason: 'not_in_ai_top', ruleScore: 66, aiScore: null, finalScore: null },
    ],
  },
  limits: { ok: true, rows: [{ symbol: '300750', consecutive: 1, status: 'sealed' }] },
}
const demoDeepAccount = buildDetail(demoDeepInput, 'book:center', demoNow, { equityPoints: demoSparks.equity })
const demoDeepSignal = buildDetail(demoDeepInput, 'symbol:600519', demoNow, { intel: { '123': { title: '茅台批价企稳回升', source: '巨潮', time: '09:50' } } })
const demoDeepRisk = buildDetail(demoDeepInput, 'post:risk', demoNow, {
  killHistory: [{ date: '2026-10-11', ok: true, events: [{ kind: 'kill_switch', active: false, mode: 'SIM', source: 'manual', reason: '演练恢复', operator: 'admin', at: '2026-10-11 15:10' }] }],
})
const demoDeepSource = buildDetail(demoDeepInput, 'source:tdx', demoNow)
const demoDeepModel = buildDetail(demoDeepInput, 'post:model', demoNow)
const demoDeepScan = buildDetail(demoDeepInput, 'post:scan', demoNow)

/* 健康分示例：扣分项自己加起来等于总分，避免出现「62 分但只扣了 22」这种对不上的示例 */
const demoTapeParts: HealthPart[] = [
  { key: 'fresh', label: '数据新鲜度', deduction: 16, note: '行情 tick 42s 前', measured: true },
  { key: 'services', label: '服务存活', deduction: 6, note: 'brain 非健康', measured: true },
  { key: 'model', label: '模型可用', deduction: 12, note: 'small 熔断', measured: true },
  { key: 'risk', label: '风控在线', deduction: 0, note: 'SIM 守着', measured: true },
  { key: 'channel', label: '交易通道', deduction: 5, note: '模拟盘按 5 分扣', measured: true },
]
const demoTapeScore = 100 - demoTapeParts.reduce((sum, part) => sum + part.deduction, 0)
const demoTapeHealth: HealthScore = { score: demoTapeScore, tone: healthTone(demoTapeScore), parts: demoTapeParts }

/* 走真实的 TapeSession 出行，不手写行——六域各至少一条，含 §6.3 健康行、§6.1 模型静默与 WS 断开 */
const demoTapeSession = new TapeSession()
demoTapeSession.absorb({ live: true }, demoNow - 60_000)
const demoTapeRows = demoTapeSession.absorb(
  {
    notices: demoTapeInput.notices,
    audit: demoTapeInput.audit,
    fills: demoTapeInput.fills,
    health: demoTapeHealth,
    silence: new Map([['model:small', { text: '10 分钟没有动静', alert: false }]]),
    live: false,
    pulses: [
      { id: 'gp1', from: 'service:market', to: 'post:scan', identifier: { kind: 'symbol', value: '600519' }, at: demoNow - 3000 },
      { id: 'gp2', from: 'service:brain', to: 'post:model', identifier: { kind: 'trace', value: 'tr-88213' }, at: demoNow - 2400 },
      { id: 'gp3', from: 'post:model', to: 'post:desk', identifier: { kind: 'signal', value: '88' }, at: demoNow - 1800 },
      { id: 'gp4', from: 'post:risk', to: 'book:center', identifier: { kind: 'rule', value: 'R01' }, at: demoNow - 1200 },
    ],
  },
  demoNow,
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
      <h2>监控 S1 原语（时段带 / 健康分 / 十格 HUD · 静态）</h2>
      <div class="g-monitor nt-monitor">
        <nav class="nt-band" data-band="morning" aria-label="交易时段（示例）">
          <span
            v-for="bandSeg in demoSegments"
            :key="bandSeg.key"
            class="nt-band__seg"
            :data-state="bandSeg.state"
            :data-key="bandSeg.key"
          >
            {{ bandSeg.label }}
          </span>
          <span class="nt-band__focus">{{ demoSpec.focus }}</span>
          <span v-if="demoCountdown" class="nt-band__count">{{ demoCountdown }}</span>
        </nav>
        <div class="g-monitor__bar">
          <button type="button" class="nt-health__badge" :data-tone="demoHealth.tone">
            健康 {{ demoHealth.score }}<i class="dot" :class="`is-${demoHealth.tone}`"></i>
          </button>
        </div>
        <section class="nt-health__pop g-monitor__pop" aria-label="健康分明细（示例）">
          <h3>健康分 {{ demoHealth.score }} · 五项各 20 分起扣</h3>
          <ul>
            <li v-for="part in demoHealth.parts" :key="part.key" :data-measured="part.measured">
              <span>{{ part.label }}</span>
              <strong v-if="part.measured" :data-deduct="part.deduction > 0">
                {{ part.deduction > 0 ? `−${part.deduction}` : '满分' }}
              </strong>
              <strong v-else>未判定</strong>
              <small>{{ part.note }}</small>
            </li>
          </ul>
        </section>
        <section class="nt-hud" aria-label="总览（示例）">
          <button
            v-for="cell in demoHud"
            :key="cell.key"
            type="button"
            :class="{ 'is-hero': cell.hero }"
            :title="cell.sub"
          >
            <span>{{ cell.label }}</span>
            <strong :class="cell.tone">{{ cell.face }}</strong>
            <Sparkline
              v-if="cell.spark"
              class="nt-hud__spark"
              :points="demoSparks[cell.spark] ?? []"
              :tone="cell.spark === 'equity' ? cell.tone : 'flat'"
            />
            <small v-if="cell.sub">{{ cell.sub }}</small>
          </button>
        </section>
      </div>
    </section>

    <section class="g-sec">
      <h2>监控 S2 原语（活动条 60 格 / 新鲜度角标 · 静态）</h2>
      <div class="g-activity">
        <div v-for="demo in demoActivityRows" :key="demo.label" class="g-activity__row">
          <span class="g-activity__label">{{ demo.label }}</span>
          <span class="g-activity__bar" aria-hidden="true">
            <i v-for="(cell, index) in demo.cells" :key="index" :class="{ on: cell === true, off: cell === false }"></i>
          </span>
          <span class="g-activity__badge" :data-tone="demo.tone">{{ demo.badge }}</span>
        </div>
        <p class="g-activity__note">牌底缘 2px、60 格每格 1 秒；亮格 --nt-accent α0.55，静默格 α0.06，没观测到的格留白。角标三档：正常 / 变橙 / 变红。</p>
      </div>
    </section>

    <section class="g-sec">
      <h2>监控画布（CanvasStage · 静态场景，可拖拽/缩放，已贴 S2 角标活动条 + S3 漏斗光带）</h2>
      <div class="g-stage">
        <CanvasStage :graph="stageGraphS2" :selected-id="stageSelected" :hot-ids="[]" :paused="false" @select="stageSelected = $event" />
      </div>
      <p class="g-activity__note">
        S3 漏斗光带：六层「全市场→初筛→AI≥70→风控通过→已下单→成交」横贯 02/03 之间，主干厚 = sqrt(数量) 12..96px，青色渐变 α0.18→0.30（浅色主题统一 0.12）。
        全市场层没有只读出口 → 画 --nt-text-3 α0.15 虚线段标「未返回」，不 0 填充不跳过；侧漏 --nt-warn α0.25 虚线 [3,3] 汇入「漏选」牌，标签是该层最大过滤原因。
        点光带 = 选中扫描牌（详情窗里有同一组六层数字，含 DOM 等价读出）。旧主干束线（扫描→四维→综合→信号）压暗为兜底。
      </p>
    </section>

    <section class="g-sec">
      <h2>监控 S6 束线流量宽度 + 高分脉冲 + 急停态（CanvasStage · 活示例）</h2>
      <div class="g-stage">
        <CanvasStage
          :graph="stageS6"
          selected-id=""
          :hot-ids="[]"
          :paused="false"
          :flow="demoFlow"
          kill-active
          :signal-scores="demoScores"
        />
      </div>
      <p class="g-activity__note">
        束线宽度 = 0.8 + min(2.4, log10(每分钟条数+1)×0.9) px：通达信 420 条/分最粗、新闻 12 条/分细一档；外围没有可信观测 →
        固定 1.3px 不猜；资金流整窗计数没动 = 断流 → 流向点停住转 --nt-warn α0.35。大模型熔断 → 它到消息/外围/综合的边
        转 --nt-danger α0.4 并停流向点。急停态 → 风控→账户的边 --nt-danger α0.6 宽 2.4，账户牌外圈 1.5px 红环呼吸 1.2s
        （sin 缓动 α0.35↔0.75）。≥85 分信号脉冲升级：r4.5 + shadowBlur 8 + 三格尾迹 + 标签加粗（每 1.6s 续一颗）；
        综合分每拍翻 88↔86 演示数字变化闪——卡片边框 300ms α0.4→0，不放大不跳动。
      </p>
    </section>

    <section class="g-sec">
      <h2>监控 S4 原语（事件流 TAPE / 五格终端带 · 静态）</h2>
      <div class="g-monitor nt-monitor g-terms">
        <TerminalStrip :input="demoTapeInput" :rows="demoTapeRows" first-pane="tape" band-key="morning" />
      </div>
      <p class="g-activity__note">
        六域标签色：信号 --nt-accent、成交 --nt-up/--nt-down（按买卖方向，方向认不出来用中性色不默认红）、风控 --nt-danger、告警 --nt-warn、数据 --nt-info、模型 --nt-accent-2。
        行是真实 TapeSession 出的，不是手写的：含 §6.3「健康 61：数据新鲜度 -16，服务存活 -6，模型可用 -12，交易通道 -5」（必须列扣分项，不写「系统异常」）、§6.1 模型静默与 WS 断开各一行。
        交互：暂停后新行只计数不滚动（按钮变「继续 · N 条新」）；点可定位的行 = 选中对应画布节点并闪一下，没有对应节点的行不可点；新行入场 300ms 背景淡出；上限 40 行。
      </p>
      <div class="g-monitor nt-monitor g-terms">
        <TerminalStrip :input="demoTapeInput" :rows="demoTapeRows" first-pane="positions" band-key="closing" />
      </div>
      <p class="g-activity__note">
        第一格随时段换语义（§4.3）。这里是尾盘「持仓明细」：浮盈走红涨绿跌，距止损不足 2% 整行左边框转橙、已跌破止损转红（两件事不混色），
        没有对齐信号止损价的写「止损价未对齐」不拿别的价冒充。其余时段：盘前「昨日复盘」/盘后「复盘」（点名缺的绩效接口）、竞价（读口未接入先用事件流顶上）、午休「半场小结」、非交易日任务调度扩为两格。
      </p>
    </section>

    <section class="g-sec">
      <h2>监控 S5 详情窗深化（§5.1–5.6 六张牌 · 静态）</h2>
      <p class="g-activity__note">
        六张牌全字段走查：缺口一律点名缺的接口（橙色文字），不写 0 不写破折号；唯一「按钮」是信号牌的「回到交易台确认」——那是一次路由跳转，不是写操作。
        风控限额环形 8/10=80% 转橙、超配 10.8%/5% 转红（NtProgressRing tone）；四维雷达资金面/外围未评 → 断开的弧 + 虚线缺口点，不再按 0 画；
        距止损 0.48% 的持仓整行橙框、跌破止损整行红框；滑点按 fills 精确配对（买入正为劣、卖出取反同口径）；
        盘后漏选复盘只陈述「漏选里 1 只涨停 / T+1 最高 6.1%」，不假设收益。日内曲线标注「仅本次会话」。
      </p>
      <div class="g-deep">
        <div v-if="demoDeepAccount" class="g-deep__card">
          <h3>§5.1 账户</h3>
          <DetailSheet :panel="demoDeepAccount" />
        </div>
        <div v-if="demoDeepSignal" class="g-deep__card">
          <h3>§5.2 信号（600519 钻取）</h3>
          <DetailSheet :panel="demoDeepSignal" />
        </div>
        <div v-if="demoDeepRisk" class="g-deep__card">
          <h3>§5.3 风控</h3>
          <DetailSheet :panel="demoDeepRisk" />
        </div>
        <div v-if="demoDeepSource" class="g-deep__card">
          <h3>§5.4 数据源（通达信）</h3>
          <DetailSheet :panel="demoDeepSource" />
        </div>
        <div v-if="demoDeepModel" class="g-deep__card">
          <h3>§5.5 模型</h3>
          <DetailSheet :panel="demoDeepModel" />
        </div>
        <div v-if="demoDeepScan" class="g-deep__card">
          <h3>§5.6 扫描</h3>
          <DetailSheet :panel="demoDeepScan" />
        </div>
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

/* 监控 S1 原语走查：借 .nt-monitor 让状态光点类生效，但解除整页网格定高 */
.g-monitor {
  display: block;
  height: auto;
  overflow: visible;
  background: transparent;
}

.g-monitor__bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 0;
}

.g-monitor__pop {
  position: static;
  width: 380px;
  margin-bottom: 10px;
  background: var(--nt-bg-2);
  backdrop-filter: none;
}

/* 监控 S4 原语走查：终端带自带 border-top，静态页里再给它一点上下留白 */
.g-terms {
  margin: 8px 0 2px;
  border: 1px solid var(--nt-border-1);
  border-radius: 6px;
  overflow: hidden;
}

/* 监控 S5 详情窗走查：六张牌两列排，借弹层的底色但不 backdrop */
.g-deep {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(430px, 1fr));
  gap: 14px;
}

.g-deep__card {
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-l);
  background: var(--nt-bg-1);
  padding: 14px;
  overflow: auto;
  max-height: 560px;
  scrollbar-width: thin;
}

.g-deep__card > h3 {
  margin: 0 0 10px;
  font-size: 13px;
  color: var(--nt-text-2);
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

.g-activity {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.g-activity__row {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.g-activity__label {
  flex: 0 0 170px;
  font-size: 12px;
  color: var(--nt-text-2);
}

.g-activity__bar {
  display: flex;
  align-items: center;
  gap: 1px;
  height: 10px;
}

.g-activity__bar i {
  width: 3px;
  height: 8px;
  border-radius: 1px;
  background: transparent;
}

.g-activity__bar i.on {
  background: color-mix(in srgb, var(--nt-accent) 55%, transparent);
}

.g-activity__bar i.off {
  background: color-mix(in srgb, var(--nt-accent) 6%, transparent);
}

.g-activity__badge {
  font-family: var(--nt-font-mono, ui-monospace, monospace);
  font-size: 10px;
  color: var(--nt-text-3);
}

.g-activity__badge[data-tone='warn'] {
  color: var(--nt-warn);
}

.g-activity__badge[data-tone='danger'] {
  color: var(--nt-danger);
}

.g-activity__note {
  margin: 0;
  font-size: 11px;
  color: var(--nt-text-3);
}

.g-modal-body {
  margin: 0;
  font-size: 13px;
  color: var(--nt-text-2);
  line-height: 1.7;
}
</style>
