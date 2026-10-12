/**
 * 展示层格式化：后端字段容错取值、数字/金额/涨跌展示。
 * 时间相关一律转调 utils/time.ts，本文件不自己造日期。
 */
import { chartTime as toChartTime, formatClock } from './time'

export type RawRecord = Record<string, unknown>

/** 后端起字段名可能 snake/camel 混用，按顺序取第一个非空值。 */
export function pick<T = unknown>(obj: unknown, ...keys: string[]): T | undefined {
  if (!obj || typeof obj !== 'object') return undefined
  const record = obj as RawRecord
  for (const key of keys) {
    const value = record[key]
    if (value !== undefined && value !== null && value !== '') return value as T
  }
  return undefined
}

/** 转数字；转不了返回 null（界面据此显示「—」）。 */
export function num(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null
  const n = Number(value)
  return Number.isFinite(n) ? n : null
}

/** 从包装对象里取列表；本身就是数组直接用。 */
export function asList<T = RawRecord>(data: unknown, ...keys: string[]): T[] {
  if (Array.isArray(data)) return data as T[]
  if (!data || typeof data !== 'object') return []
  const record = data as RawRecord
  for (const key of keys) {
    if (Array.isArray(record[key])) return record[key] as T[]
  }
  return []
}

export function amount(value: unknown): string {
  const n = num(value)
  if (n === null) return '—'
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

export function signed(value: unknown): string {
  const n = num(value)
  if (n === null) return '—'
  const text = amount(n)
  return n > 0 ? `+${text}` : text
}

export function price(value: unknown): string {
  return amount(value)
}

export function ratioPct(value: unknown, digits = 2): string {
  const n = num(value)
  if (n === null) return '—'
  return `${(n * 100).toFixed(digits)}%`
}

/** 已经是百分点口径的数字（如 avgPct = 1.23 表示 1.23%）。 */
export function pctText(value: unknown, digits = 2): string {
  const n = num(value)
  if (n === null) return '—'
  return `${n.toFixed(digits)}%`
}

/** 整数计数；没有给短横线。 */
export function intText(value: unknown): string {
  const n = num(value)
  if (n === null) return '—'
  return String(Math.round(n))
}

/** 大额压缩：1.23 万 / 4.56 亿。 */
export function compactAmount(value: unknown): string {
  const n = num(value)
  if (n === null) return '—'
  const abs = Math.abs(n)
  if (abs >= 1e8) return `${(n / 1e8).toFixed(2)} 亿`
  if (abs >= 1e4) return `${(n / 1e4).toFixed(2)} 万`
  return amount(n)
}

export function clock(value: unknown): string {
  return formatClock(value)
}

// 与 panels/market/SentimentStages.vue 的五阶段口径一致，全仓只用这一套中文。
const PHASE: Record<string, string> = {
  ICE: '冰点',
  RECOVER: '修复',
  WARM: '升温',
  HOT: '高潮',
  FADE: '退潮',
}

export function phaseName(code: unknown): string {
  if (!code) return '—'
  const key = String(code)
  return PHASE[key] || key
}

const DIM_KEYS: Record<string, string[]> = {
  技术面: ['技术面', 'tech', 'technical'],
  消息面: ['消息面', 'news', 'message', 'intel'],
  资金面: ['资金面', 'capital', 'money', 'fund'],
  外围: ['外围', 'peripheral', 'overseas', 'external'],
}

export function dimValue(dims: unknown, label: string): number | null {
  const keys = DIM_KEYS[label] || [label]
  for (const key of keys) {
    const value = pick(dims, key)
    if (value !== undefined) return num(value)
  }
  return null
}

const ORDER_STATUS: Record<string, string> = {
  new: '待报',
  pending: '待报',
  accepted: '已报',
  partial: '部成',
  filled: '已成',
  cancelled: '已撤',
  canceled: '已撤',
  rejected: '已拒绝',
  expired: '已过期',
}

/** 委托方向中文。红涨绿跌由调用方加 .up / .down。 */
export function sideLabel(side: unknown): string {
  const key = String(side || '').toLowerCase()
  if (key === 'buy') return '买入'
  if (key === 'sell') return '卖出'
  return key || '—'
}

export function orderStatusLabel(status: unknown): string {
  const key = String(status || '').toLowerCase()
  return ORDER_STATUS[key] || (key ? key : '—')
}

/** 只有未终态委托可以撤。 */
export function canCancelOrder(status: unknown): boolean {
  const key = String(status || '').toLowerCase()
  return key === 'new' || key === 'pending' || key === 'accepted' || key === 'partial'
}

const STRATEGY_LABEL: Record<string, string> = {
  first_board_weak2strong: '首板弱转强',
  pullback_dip: '强势回踩',
}

/** 策略模板代码 → 中文。 */
export function strategyLabel(code: unknown): string {
  const key = String(code || '')
  if (!key) return '—'
  return STRATEGY_LABEL[key] || key
}

const MISS_LABEL: Record<string, string> = {
  held: '已持仓',
  cooldown: '冷却中',
  not_in_ai_top: '没进模型前 30',
  ai_invalid: '模型结果作废',
  below_threshold: '综合分低于 75',
  daily_cap: '当日买入已满 5 只',
  price_invalid: '价格不合法',
  killswitch: '急停中',
  after_cutoff: '已过 14:50',
  no_quote: '没有行情',
}

/** 漏斗漏选原因代码 → 中文。 */
export function missLabel(code: unknown): string {
  const key = String(code || '')
  if (!key) return '—'
  return MISS_LABEL[key] || key
}

const SIGNAL_STATUS: Record<string, string> = {
  risk_checking: '风控审核',
  approved: '已通过',
  rejected: '已拦截',
  executing: '执行中',
  done: '已执行',
  closed: '已平仓',
  expired: '已过期',
  cancelled: '已取消',
  canceled: '已取消',
}

/** 信号状态机代码 → 中文。 */
export function signalStatusLabel(status: unknown): string {
  const key = String(status || '').toLowerCase()
  return SIGNAL_STATUS[key] || (key ? key : '—')
}

/** 有效期剩余的文字；已结束返回空串，由调用方改显「已过期」。 */
export function validRemainText(validUntil: unknown, now = Date.now()): string {
  if (!validUntil) return ''
  const ts = new Date(String(validUntil)).getTime()
  if (!Number.isFinite(ts)) return ''
  const ms = ts - now
  if (ms <= 0) return ''
  const minutes = Math.ceil(ms / 60000)
  if (minutes >= 60) return `剩 ${Math.floor(minutes / 60)} 时 ${minutes % 60} 分`
  return `剩 ${minutes} 分`
}

export function isValidExpired(validUntil: unknown, now = Date.now()): boolean {
  if (!validUntil) return false
  const ts = new Date(String(validUntil)).getTime()
  if (!Number.isFinite(ts)) return false
  return ts <= now
}

export function messageOf(error: unknown): string {
  if (!error) return ''
  const raw = error instanceof Error ? error.message : String(error)
  const status = (error as { status?: number })?.status
  if (status === 404) {
    const cleaned = raw.replace(/^not found:?\s*/i, '').trim()
    return cleaned && !/^not found$/i.test(raw) ? cleaned : '还没有这份记录'
  }
  if (status === 0) return raw || '无法连接 admin（2001）'
  const text = raw
    .replace(/^invalid:\s*/i, '')
    .replace(/^bad request:\s*/i, '')
    .replace(/^not found:?\s*/i, '')
    .trim()
  if (/rejected/i.test(text) && /撤/.test(text)) return '这笔委托已拒绝，不能再撤'
  if (/filled/i.test(text) && /撤/.test(text)) return '这笔委托已成交，不能再撤'
  if (/cancel/i.test(text) && /撤/.test(text)) return '这笔委托已撤销'
  return text || '请求失败'
}

export function chartTime(value: unknown, freq = '1d'): string | number | null {
  return toChartTime(value, freq)
}

/** 涨跌着色用的类名。红涨绿跌（A 股约定）由 CSS 令牌控制，这里只分方向。 */
export function pnlClass(value: unknown): 'up' | 'down' | '' {
  const n = num(value)
  if (n === null || n === 0) return ''
  return n > 0 ? 'up' : 'down'
}

/** 涨跌符号，配合 pnlClass 用。 */
export function pnlSign(value: unknown): '+' | '-' | '' {
  const n = num(value)
  if (n === null || n === 0) return ''
  return n > 0 ? '+' : '-'
}
