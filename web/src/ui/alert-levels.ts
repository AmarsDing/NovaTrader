/**
 * 告警分级册（方案 §8）：推送帧 → L1–L4 四级归一，以及同因聚合、冷却、静默时段、
 * 五段式文案渲染的纯函数层。不依赖 Vue/Pinia；时钟一律注入，便于测试。
 *
 * 分级语义（§8.1）：
 * - L4 致命：强平/熔断/拒单飙升/订单状态未知，或后端已标 critical；直通一切静默/冷却；
 * - L3 重要：告警规则命中、拉升/涨停/破板/回撤等盘中异动；
 * - L2 提醒：warn/warning 级别、数据质量与数据源域、扫描零命中；
 * - L1 其余。
 */
export type AlertLevel = 'L1' | 'L2' | 'L3' | 'L4'

/** 告警帧视图：字段全可选，容忍老帧与异源帧。 */
export interface AlertEventView {
  topic?: string
  event?: string
  kind?: string
  level?: string
  category?: string
  symbol?: string
  title?: string
  body?: string
  dedupKey?: string
  at?: number
}

/** L4 kind 白名单（§8.1）：风控最紧急四事。 */
const L4_KINDS = new Set(['killswitch', 'breaker', 'reject_rate', 'order_unknown'])

/** L3 kind 白名单（§8.1）：规则命中与盘中异动。 */
const L3_KINDS = new Set(['alert-rule', 'surge', 'limit_up', 'break', 'drawdown'])

/** 归级：critical 判定优先于一切 kind 白名单。 */
export function levelOf(e: AlertEventView): AlertLevel {
  if (e.level === 'critical') return 'L4'
  if (e.kind && L4_KINDS.has(e.kind)) return 'L4'
  if (e.kind && L3_KINDS.has(e.kind)) return 'L3'
  if (e.level === 'warn' || e.level === 'warning') return 'L2'
  if (e.category === 'datahub' || e.category === 'quality') return 'L2'
  if (e.kind === 'scan_zero_hit') return 'L2'
  return 'L1'
}

/** 同因键：dedupKey 按冒号截前两段（防后端去重计数尾段漂移），否则 kind:symbol 回落，再兜底「系统」。 */
export function causeKey(e: AlertEventView): string {
  const dk = e.dedupKey?.trim()
  if (dk) return dk.split(':').slice(0, 2).join(':')
  return `${e.kind?.trim() || 'alert'}:${e.symbol?.trim() || '系统'}`
}

/** 五段式文案（§8.2）的单条建议动作：点击直达对象所在面板。 */
export interface AlertCopyAction {
  id: string
  label: string
  panel: string
  symbol?: string
}

/** 五段式文案：对象 + 事件 + 条件 + 时间 + 建议动作。 */
export interface AlertCopy {
  object: string
  event: string
  condition: string
  time: string
  actions: AlertCopyAction[]
}

/** 渲染五段式：名称字典命中给中文名，否则裸代码，再否则「系统」。 */
export function renderAlertCopy(
  e: AlertEventView,
  nameDict?: Record<string, string>,
  now: () => number = () => Date.now(),
): AlertCopy {
  const symbol = e.symbol?.trim()
  const object = (symbol && nameDict?.[symbol]) || symbol || '系统'
  const actions: AlertCopyAction[] = []
  if (symbol) actions.push({ id: 'quote', label: '看行情', panel: 'quote-sidebar', symbol })
  if (e.kind === 'alert-rule') actions.push({ id: 'rule', label: '看规则', panel: 'alert-rules' })
  return {
    object,
    event: e.title || '',
    condition: e.body || '',
    time: formatHHMMSS(e.at ?? now()),
    actions,
  }
}

/** 同因聚合器：同窗同因 count 累计、first 不变；跨窗重置；异因互不影响。 */
export function createCauseAggregator(windowMs = 10_000, now: () => number = () => Date.now()) {
  const buckets = new Map<string, { count: number; first: number }>()
  return {
    seen(key: string): { count: number; first: number } {
      const t = now()
      let b = buckets.get(key)
      if (!b || t - b.first >= windowMs) {
        b = { count: 0, first: t }
        buckets.set(key, b)
      }
      b.count += 1
      return { count: b.count, first: b.first }
    },
  }
}

/**
 * 冷却判定：last（键 → 上次放行时刻）由调用方持有。
 * 窗内返回 true（应抑制，且不写时刻，防持续事件把窗口滑到无限）；
 * 窗外（含边界等值）写入 now 并返回 false。
 */
export function cooldownCheck(last: Map<string, number>, key: string, now: number, cooldownMs: number): boolean {
  const prev = last.get(key)
  if (prev != null && now - prev < cooldownMs) return true
  last.set(key, now)
  return false
}

/** 静默时段窗：'HH:MM' 起止 + 适用级别名单；支持跨午夜。 */
export interface QuietHoursWindow {
  start: string
  end: string
  levels: AlertLevel[]
}

/** 静默判定：L4 永远直通；级别不在该窗 levels 名单 → 该窗不适用。 */
export function quietHours(level: AlertLevel, windows: QuietHoursWindow[], now: number): boolean {
  if (level === 'L4') return false
  for (const w of windows) {
    if (!w.levels.includes(level)) continue
    if (inQuietWindow(w.start, w.end, now)) return true
  }
  return false
}

/** 时段窗判定（本地时区）：同日起止走闭开区间；跨午夜走两段并集。 */
function inQuietWindow(start: string, end: string, now: number): boolean {
  const s = parseHHMM(start)
  const e = parseHHMM(end)
  if (s == null || e == null) return false
  const d = new Date(now)
  const cur = d.getHours() * 60 + d.getMinutes()
  if (s <= e) return cur >= s && cur < e
  return cur >= s || cur < e
}

function parseHHMM(s: string): number | null {
  const m = /^(\d{1,2}):(\d{2})$/.exec(s.trim())
  if (!m) return null
  const h = Number(m[1])
  const mi = Number(m[2])
  if (h > 23 || mi > 59) return null
  return h * 60 + mi
}

function formatHHMMSS(ts: number): string {
  const d = new Date(ts)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
