/**
 * 告警静默/冷却的前端配置（方案 §8.3/§8.5）：localStorage 持久化，读路径全容错。
 * - cooldownMs：同因 toast 的最小间隔（默认 60s）；
 * - windows：静默时段（'HH:MM' 起止 + 适用级别，支持跨午夜），默认空（不静默）；
 * L3/L4 不受静默约束（L4 直通由 quietHours 保证）。
 * 纯 TS、不依赖 Vue；读写统一 try/catch（隐私模式容错）。
 */
import { quietHours, type AlertLevel, type QuietHoursWindow } from '@/ui/alert-levels'

export type QuietWindow = QuietHoursWindow

export interface AlertQuietConfig {
  cooldownMs: number
  windows: QuietWindow[]
}

export const ALERT_QUIET_KEY = 'nt.alertquiet.v1'

const DEFAULTS: AlertQuietConfig = { cooldownMs: 60_000, windows: [] }

const LEVELS: readonly AlertLevel[] = ['L1', 'L2', 'L3', 'L4']

/** 读配置：无键 / 坏 JSON / 缺字段 / 存储不可读 → 回退默认。 */
export function loadQuiet(): AlertQuietConfig {
  try {
    const raw = localStorage.getItem(ALERT_QUIET_KEY)
    if (!raw) return { ...DEFAULTS }
    const parsed = JSON.parse(raw) as Partial<AlertQuietConfig> | null
    if (!parsed || typeof parsed !== 'object') return { ...DEFAULTS }
    return { cooldownMs: sanitizeCooldown(parsed.cooldownMs), windows: sanitizeWindows(parsed.windows) }
  } catch {
    return { ...DEFAULTS }
  }
}

/** 写配置：隐私模式等写失败静默吞掉（下次读回默认，不炸页面）。 */
export function saveQuiet(cfg: AlertQuietConfig): void {
  try {
    localStorage.setItem(ALERT_QUIET_KEY, JSON.stringify(cfg))
  } catch {
    /* 静默吞掉 */
  }
}

/** 当前时刻是否处于静默：内部复用 quietHours（L4 直通）。 */
export function isQuietNow(level: AlertLevel, now: number = Date.now()): boolean {
  return quietHours(level, loadQuiet().windows, now)
}

function sanitizeCooldown(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : DEFAULTS.cooldownMs
}

function sanitizeWindows(v: unknown): QuietWindow[] {
  if (!Array.isArray(v)) return []
  const out: QuietWindow[] = []
  for (const w of v) {
    if (!w || typeof w !== 'object') continue
    const o = w as Record<string, unknown>
    if (typeof o.start !== 'string' || typeof o.end !== 'string') continue
    const levels = Array.isArray(o.levels) ? o.levels.filter((l): l is AlertLevel => LEVELS.includes(l as AlertLevel)) : []
    out.push({ start: o.start, end: o.end, levels })
  }
  return out
}
