/**
 * 时间统一入口：所有对外的时刻解析与展示都走这里。
 *
 * 约定：
 * - A 股一切展示按 Asia/Shanghai，不看运行机器的时区。
 * - lightweight-charts 的日线用 'YYYY-MM-DD'，分钟线用 UNIX 秒（UTC+8 对齐）。
 * - 后端字段可能混用 ISO 串、'YYYY-MM-DD HH:mm:ss'、秒/毫秒时间戳，一律用 parseTime。
 */

export const SHANGHAI_TZ = 'Asia/Shanghai'

const dateFmt = new Intl.DateTimeFormat('en-CA', {
  timeZone: SHANGHAI_TZ,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})

const clockFmt = new Intl.DateTimeFormat('zh-CN', {
  timeZone: SHANGHAI_TZ,
  hour12: false,
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
})

const minuteFmt = new Intl.DateTimeFormat('zh-CN', {
  timeZone: SHANGHAI_TZ,
  hour12: false,
  hour: '2-digit',
  minute: '2-digit',
})

/** 把后端给的各种时间形状解析为 Date；解析不了返回 null。 */
export function parseTime(value: unknown): Date | null {
  if (value == null || value === '') return null
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value
  if (typeof value === 'number') {
    // 秒级时间戳（< 1e12）自动升到毫秒。
    const ms = value < 1e12 ? value * 1000 : value
    const date = new Date(ms)
    return Number.isNaN(date.getTime()) ? null : date
  }
  const text = String(value).trim()
  if (!text) return null
  if (/^\d{10}$/.test(text)) return parseTime(Number(text))
  if (/^\d{13}$/.test(text)) return parseTime(Number(text))
  if (/^\d{4}-\d{2}-\d{2}$/.test(text)) {
    const date = new Date(`${text}T00:00:00+08:00`)
    return Number.isNaN(date.getTime()) ? null : date
  }
  if (/^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2})?$/.test(text)) {
    const date = new Date(`${text.replace(' ', 'T')}+08:00`)
    return Number.isNaN(date.getTime()) ? null : date
  }
  const date = new Date(text)
  return Number.isNaN(date.getTime()) ? null : date
}

/** 'YYYY-MM-DD'（上海）。 */
export function formatDate(value: unknown): string {
  const date = parseTime(value)
  return date ? dateFmt.format(date) : ''
}

/** 'HH:mm:ss'（上海）。 */
export function formatClock(value: unknown): string {
  const date = parseTime(value)
  return date ? clockFmt.format(date) : ''
}

/** 'HH:mm'（上海）。 */
export function formatMinute(value: unknown): string {
  const date = parseTime(value)
  return date ? minuteFmt.format(date) : ''
}

/** 'YYYY-MM-DD HH:mm:ss'（上海）。 */
export function formatDateTime(value: unknown): string {
  const date = parseTime(value)
  if (!date) return ''
  return `${dateFmt.format(date)} ${clockFmt.format(date)}`
}

/** 上海时区的四个数字，供交易时段判断用。 */
export function shanghaiParts(value: unknown = new Date()): { hour: number; minute: number; second: number } | null {
  const date = parseTime(value)
  if (!date) return null
  const parts = minuteFmt.formatToParts(date)
  const hour = Number(parts.find((item) => item.type === 'hour')?.value ?? '')
  const minute = Number(parts.find((item) => item.type === 'minute')?.value ?? '')
  const seconds = clockFmt.formatToParts(date).find((item) => item.type === 'second')?.value
  if (!Number.isFinite(hour) || !Number.isFinite(minute)) return null
  return { hour, minute, second: Number(seconds ?? 0) }
}

export type MarketSession = 'pre' | 'auction' | 'morning' | 'noon' | 'afternoon' | 'closed'

/** A 股时段：盘前 / 集合竞价 / 上午 / 午休 / 下午 / 已收盘。 */
export function sessionOf(value: unknown = new Date()): MarketSession {
  const parts = shanghaiParts(value)
  if (!parts) return 'closed'
  const mm = parts.hour * 60 + parts.minute
  if (mm < 9 * 60 + 15) return 'pre'
  if (mm < 9 * 60 + 30) return 'auction'
  if (mm < 11 * 60 + 30) return 'morning'
  if (mm < 13 * 60) return 'noon'
  if (mm < 15 * 60) return 'afternoon'
  return 'closed'
}

export const SESSION_TEXT: Record<MarketSession, string> = {
  pre: '盘前',
  auction: '集合竞价',
  morning: '上午盘',
  noon: '午休',
  afternoon: '下午盘',
  closed: '已收盘',
}

/** 交易时段内（集合竞价起、收盘止）。 */
export function inSession(value: unknown = new Date()): boolean {
  const session = sessionOf(value)
  return session === 'auction' || session === 'morning' || session === 'afternoon'
}

/**
 * lightweight-charts 的时间轴值。
 * 日线用 'YYYY-MM-DD'；分钟线用 UNIX 秒并且已按 UTC+8 平移，
 * 否则图表会整体差 8 小时。
 */
export function chartTime(value: unknown, freq = '1d'): string | number | null {
  const date = parseTime(value)
  if (!date) return null
  if (freq === '1d') return dateFmt.format(date)
  return Math.floor(date.getTime() / 1000) + 8 * 3600
}

/** 当天 00:00（上海）的时间戳，用来算「距今天数」。 */
export function startOfDay(value: unknown = new Date()): Date | null {
  const text = formatDate(value)
  return text ? new Date(`${text}T00:00:00+08:00`) : null
}

/** 两个时刻相差几个自然日（按上海日历）。 */
export function daysBetween(from: unknown, to: unknown = new Date()): number | null {
  const a = startOfDay(from)
  const b = startOfDay(to)
  if (!a || !b) return null
  return Math.round((b.getTime() - a.getTime()) / 86400000)
}
