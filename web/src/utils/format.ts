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

const PHASE: Record<string, string> = {
  ICE: '冰点',
  RECOVER: '修复',
  WARM: '温和',
  HOT: '过热',
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

export function messageOf(error: unknown): string {
  if (!error) return ''
  const raw = error instanceof Error ? error.message : String(error)
  const status = (error as { status?: number })?.status
  if (status === 404) return raw && raw !== 'Not Found' ? raw : '管理网关尚未提供该接口'
  if (status === 0) return raw || '无法连接 admin（2001）'
  return raw || '请求失败'
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
