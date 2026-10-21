/**
 * A 股板块涨跌幅档推断（用于 K 线「封板」高亮）。
 *
 * 已知启发式缺口：ST/*ST（±5%）、退市整理期等特别处理从代码看不出，
 * 一律按普通档算——结论只会「漏亮」不会「误亮」：真实封板日的收盘价
 * 必然精确落在普通档限制价上，放大档只会把判定门槛抬高。
 */

/** 默认涨跌幅档：创业板 300/301/302 与科创板 68x 为 20%，北交所（含 920 段与老 4/8 段）30%，其余主板 10%。 */
export function limitRatio(symbol: string): number {
  const digits = /^\d+/.exec(symbol.trim())?.[0] ?? ''
  if (/^920|^[48]/.test(digits)) return 0.3
  if (/^30[012]|^68/.test(digits)) return 0.2
  return 0.1
}

/** 板块角标册键（status-map board.* 三键）：与 limitRatio 同一份代码前缀口径（单源共存）。
 * 主板返回 null——主板是默认态，不打角标防视觉噪声。 */
export type BoardKey = 'board.cyb' | 'board.kcb' | 'board.bj'

export function boardTag(symbol: string): BoardKey | null {
  const digits = /^\d+/.exec(symbol.trim())?.[0] ?? ''
  if (/^920|^[48]/.test(digits)) return 'board.bj'
  if (/^30[012]/.test(digits)) return 'board.cyb'
  if (/^68/.test(digits)) return 'board.kcb'
  return null
}

export type LimitState = 'up' | 'down'

const round2 = (value: number) => Math.round(value * 100) / 100

/**
 * 涨/跌停封板判定：收盘价精确等于「昨收×(1±档)」取整到 0.01 的理论限制价，
 * 且当日最高/最低=收盘（尾盘封死未开板）。prevClose 缺失 → null（不猜）。
 */
export function limitState(
  close: number | null,
  high: number | null,
  low: number | null,
  prevClose: number | null,
  symbol: string,
): LimitState | null {
  if (!Number.isFinite(close) || !Number.isFinite(high) || !Number.isFinite(low)) return null
  if (prevClose == null || !Number.isFinite(prevClose) || prevClose <= 0) return null
  const ratio = limitRatio(symbol)
  if (high === close && close === round2(prevClose * (1 + ratio))) return 'up'
  if (low === close && close === round2(prevClose * (1 - ratio))) return 'down'
  return null
}
