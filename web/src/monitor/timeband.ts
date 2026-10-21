/**
 * 时段带：把一天切成七段交易时段 + 非交易日，页面焦点随段落切换（监控业务监控方案 §4.3）。
 * 纯函数模块：不碰 DOM，时间一律由调用方传入，便于 vitest 冻结时钟。
 *
 * 交易日判定：周六/周日为休市。法定节假日暂无只读日历接口
 * （M00 的 trade_calendar 未对前端暴露），节假日按交易日走，
 * 数据静默由活动条/新鲜度角标（S2）兜底，不在这里编假日表。
 *
 * 时段语义钉市场钟（Asia/Shanghai），与设备时区脱钩：传入的 Date 一律按瞬时理解，
 * 判定时取上海墙钟字段。实现用固定 +8h 偏移 + UTC 读数，不走 Intl.DateTimeFormat ——
 * 域限定等价：上海 1991 年夏令时终止后恒定 UTC+8（更早的 1986–1991 夏令时 UTC+9、
 * 1940 年代战时夏令时均不在域内），而本模块只面对现代交易日瞬时（行情纪元 ≥2019），
 * 故固定 +8h 与 Asia/Shanghai 在域内严格等价；同时躲开 ICU 边角（hourCycle 缺省时
 * 午夜可读作 24 点之类）与环境的 ICU 版本差异。
 * 消费方多半喂 serverNow()（P2-10 服务端钟），即使在海外/UTC 机上打开，
 * 页面焦点仍跟随 A 股时段而不是旅客的本地钟。
 */

const SHANGHAI_OFFSET_MS = 8 * 60 * 60 * 1000

export interface ShanghaiWall {
  year: number
  /** 1–12 */
  month: number
  /** 1–31 */
  day: number
  /** 0=周日 … 6=周六 */
  weekday: number
  second: number
  /** 自上海当日 00:00 起的分钟数 */
  minuteOfDay: number
}

/** 把瞬时 Date 按市场钟拆墙钟字段；NaN 输入透传 NaN（与本地 getDay() 同病，不另造语义）。 */
export function shanghaiWall(date: Date): ShanghaiWall {
  const shifted = new Date(date.getTime() + SHANGHAI_OFFSET_MS)
  return {
    year: shifted.getUTCFullYear(),
    month: shifted.getUTCMonth() + 1,
    day: shifted.getUTCDate(),
    weekday: shifted.getUTCDay(),
    second: shifted.getUTCSeconds(),
    minuteOfDay: shifted.getUTCHours() * 60 + shifted.getUTCMinutes(),
  }
}

export type BandKey = 'preopen' | 'auction' | 'morning' | 'lunch' | 'afternoon' | 'closing' | 'postmarket' | 'offday'

export type FirstPane = 'tape' | 'auction' | 'midday' | 'positions' | 'review' | 'dispatch'

export interface BandSpec {
  key: BandKey
  /** 展示用时间段文本，如 08:30–09:15；非交易日为空串 */
  span: string
  /** 这个时段页面要回答的问题 */
  focus: string
  /** 提升为焦点牌（画布内加宽）的节点 id；空串不提升 */
  promote: '' | 'post:scan' | 'book:center'
  /** 这个时段要加粗的束线：into = 进入该节点的所有边，edges = 精确 `from>to` */
  thicken: { into?: string[]; edges?: string[] }
  /** 画布整体压暗系数：1 常态；午休 0.75；盘后 0.5 */
  veil: number
  /** 停掉环境流向点 */
  calm: boolean
  /** 终端带第一格的时段语义（S4 已接管：tape/auction/midday/positions/review/dispatch，见 TerminalStrip.vue） */
  firstPane: FirstPane
}

export interface BandSegmentView {
  key: BandKey
  label: string
  state: 'past' | 'now' | 'future'
}

export type SparkKey = '' | 'limit' | 'broken' | 'signal' | 'p95' | 'equity'

export interface HudCellDef {
  /** 语义键，resolveHud（hud.ts）按它解析牌面内容 */
  key: string
  label: string
  /** 点击选中哪个画布节点 */
  select: string
  /** sparkline 采样键；空串不画 */
  spark: SparkKey
  hero?: boolean
}

interface Segment {
  key: BandKey
  label: string
  from: number
  to: number
}

/** 一天的七段。竞价段收进 09:25–09:30 的开盘前 5 分钟，避免空档。 */
const SEGMENTS: Segment[] = [
  { key: 'preopen', label: '盘前自检', from: 8 * 60 + 30, to: 9 * 60 + 15 },
  { key: 'auction', label: '竞价', from: 9 * 60 + 15, to: 9 * 60 + 30 },
  { key: 'morning', label: '上午', from: 9 * 60 + 30, to: 11 * 60 + 30 },
  { key: 'lunch', label: '午休', from: 11 * 60 + 30, to: 13 * 60 },
  { key: 'afternoon', label: '下午', from: 13 * 60, to: 14 * 60 + 30 },
  { key: 'closing', label: '尾盘', from: 14 * 60 + 30, to: 15 * 60 },
  { key: 'postmarket', label: '盘后', from: 15 * 60, to: 17 * 60 },
]

const SPECS: Record<BandKey, BandSpec> = {
  preopen: { key: 'preopen', span: '08:30–09:15', focus: '数据链路 + 外围 + 昨日复盘', promote: '', thicken: {}, veil: 1, calm: false, firstPane: 'review' },
  auction: { key: 'auction', span: '09:15–09:25', focus: '竞价异动', promote: 'post:scan', thicken: { into: ['post:scan'] }, veil: 1, calm: false, firstPane: 'auction' },
  morning: { key: 'morning', span: '09:30–11:30', focus: '全链路', promote: '', thicken: {}, veil: 1, calm: false, firstPane: 'tape' },
  lunch: { key: 'lunch', span: '11:30–13:00', focus: '半场小结', promote: '', thicken: {}, veil: 0.75, calm: true, firstPane: 'midday' },
  afternoon: { key: 'afternoon', span: '13:00–14:30', focus: '全链路', promote: '', thicken: {}, veil: 1, calm: false, firstPane: 'tape' },
  closing: { key: 'closing', span: '14:30–15:00', focus: '持仓与平仓决策', promote: 'book:center', thicken: { edges: ['post:risk>book:center'] }, veil: 1, calm: false, firstPane: 'positions' },
  postmarket: { key: 'postmarket', span: '15:00–17:00', focus: '复盘与自检', promote: '', thicken: {}, veil: 0.5, calm: true, firstPane: 'review' },
  offday: { key: 'offday', span: '', focus: '链路自检 + 回测任务', promote: '', thicken: {}, veil: 1, calm: true, firstPane: 'dispatch' },
}

const DEFAULT_HUD: HudCellDef[] = [
  { key: 'phase', label: '阶段', select: 'post:scan', spark: 'limit' },
  { key: 'limit', label: '涨停', select: 'post:scan', spark: 'limit' },
  { key: 'broken', label: '炸板率', select: 'post:scan', spark: 'broken' },
  { key: 'mainline', label: '主线', select: 'dim:news', spark: '' },
  { key: 'signal', label: '信号', select: 'post:desk', spark: 'signal' },
  { key: 'miss', label: '漏选', select: 'post:miss', spark: '' },
  { key: 'risk', label: '风控', select: 'post:risk', spark: '' },
  { key: 'model', label: '大模型', select: 'model:large', spark: 'p95' },
  { key: 'gpu', label: 'GPU', select: 'service:brain', spark: '' },
  { key: 'pnl', label: '当日盈亏', select: 'book:center', spark: 'equity', hero: true },
]

/** 时段特化：只换前三格，其余保持默认（监控业务监控方案 §4.3「HUD 替换」列） */
const SWAP: Partial<Record<BandKey, HudCellDef[]>> = {
  preopen: [
    { key: 'gap:perf', label: '昨日胜率', select: 'post:desk', spark: '' },
    { key: 'gap:misslimit', label: '昨日漏选涨停', select: 'post:miss', spark: '' },
    { key: 'overnight:overseas', label: '外围涨跌', select: 'dim:overseas', spark: '' },
  ],
  auction: [
    { key: 'gap:auction-amount', label: '竞价金额', select: 'post:scan', spark: '' },
    { key: 'gap:auction-limit', label: '竞价涨停', select: 'post:scan', spark: '' },
    { key: 'gap:auction-rush', label: '抢筹前三', select: 'post:scan', spark: '' },
  ],
  lunch: [
    { key: 'midday:pnl', label: '半场盈亏', select: 'book:center', spark: 'equity' },
    { key: 'midday:limit', label: '半场涨停', select: 'post:scan', spark: 'limit' },
    { key: 'midday:focus', label: '下午关注', select: 'post:news', spark: '' },
  ],
  closing: [
    { key: 'close:float', label: '持仓浮盈', select: 'book:center', spark: 'equity' },
    { key: 'close:stop', label: '距止损最近', select: 'post:risk', spark: '' },
    { key: 'close:countdown', label: '距收盘', select: 'book:center', spark: '' },
  ],
  postmarket: [
    { key: 'gap:winrate', label: '今日胜率', select: 'post:desk', spark: '' },
    { key: 'gap:attribution', label: '盈亏归因最大维', select: 'dim:synth', spark: '' },
    { key: 'gap:correlation', label: '评分相关性', select: 'dim:synth', spark: '' },
  ],
  offday: [
    { key: 'off:tasks', label: '回测任务', select: 'task:latest', spark: '' },
    { key: 'off:backfill', label: '数据补全', select: 'source:quality', spark: '' },
    { key: 'off:db', label: '库体积', select: 'service:postgres', spark: '' },
  ],
}

export function isTradingDay(date: Date): boolean {
  const day = shanghaiWall(date).weekday
  return day >= 1 && day <= 5
}

export function detectBand(date: Date, tradingDay = isTradingDay(date)): BandKey {
  if (!tradingDay) return 'offday'
  const minutes = shanghaiWall(date).minuteOfDay
  for (const segment of SEGMENTS) {
    if (minutes >= segment.from && minutes < segment.to) return segment.key
  }
  /* 8:30 前并入盘前自检（链路已经在跑），17:00 后并入盘后，不造新段落 */
  return minutes < SEGMENTS[0].from ? 'preopen' : 'postmarket'
}

export function bandSpec(key: BandKey): BandSpec {
  return SPECS[key]
}

export function bandSegments(key: BandKey): BandSegmentView[] {
  const nowIndex = key === 'offday' ? -1 : SEGMENTS.findIndex((segment) => segment.key === key)
  return SEGMENTS.map((segment, index) => ({
    key: segment.key,
    label: segment.label,
    state: index < nowIndex ? 'past' : index === nowIndex ? 'now' : 'future',
  }))
}

/** 当前时段到下一个锚点的倒计时文案；没有锚点（盘后/非交易日）返回空串 */
export function bandCountdown(date: Date, key: BandKey): string {
  const anchors: Partial<Record<BandKey, { label: string; to: number }>> = {
    preopen: { label: '距竞价', to: 9 * 60 + 15 },
    auction: { label: '距开盘', to: 9 * 60 + 30 },
    morning: { label: '距午休', to: 11 * 60 + 30 },
    lunch: { label: '距开盘', to: 13 * 60 },
    afternoon: { label: '距收盘', to: 15 * 60 },
    closing: { label: '距收盘', to: 15 * 60 },
  }
  const anchor = anchors[key]
  if (!anchor) return ''
  const left = anchor.to - shanghaiWall(date).minuteOfDay
  return left > 0 ? `${anchor.label} ${left} 分` : ''
}

export function hudCells(key: BandKey): HudCellDef[] {
  const swap = SWAP[key]
  return swap ? [...swap, ...DEFAULT_HUD.slice(swap.length)] : DEFAULT_HUD
}
