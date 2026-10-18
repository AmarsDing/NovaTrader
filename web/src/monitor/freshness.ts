/**
 * S2 新鲜度角标 + 静默检测（§6.1 静默、§6.2 陈旧阈值）。
 *
 * 两条不同的尺子，分开用：
 *   角标（fresh）问的是「这份数据本身多旧」——有自带时间戳的牌（数据源 lastAt、
 *     情绪 asOf、晨报 generatedAt、盘后 tradeDate）按它算；没有时间戳的接口
 *     （账户、模型统计、持仓、订单）只能说「这次轮询是什么时候读到的」，
 *     所以角标退化成本次采样年龄，接口挂住不返回时它会自己涨上去，也是诚实的。
 *   活动条（activity.ts）问的是「这张牌这一分钟动过没有」——没有时间戳的那几张
 *     牌只能靠它判断死活，两条尺子不能互相替代。
 *
 * 静默（silence）只做升级：live / idle → attention，绝不把已经 down / gap 的牌往回写。
 * 时间不够长（观测不足阈值）就不下结论——刚打开页面不该满屏「没有动静」。
 */

import type { NodeStatus, SceneGraph, SceneNode } from './types'
import type { ActivityLog } from './activity'

export type FreshKind = 'tape' | 'sentiment' | 'account' | 'model' | 'overseas' | 'post'
export type FreshTone = 'ok' | 'warn' | 'danger'

export interface CardFresh {
  /** 牌面右上角的角标，如 "3s 前" / "上一交易日" */
  text: string
  tone: FreshTone
}

export interface CardSilence {
  /** 副行文字，如 "30 秒没有新数据" */
  text: string
  /** true 表示要升告警（走 AlertCenter），false 只把牌面转 attention */
  alert: boolean
}

/** §6.2 秒级阈值：正常 / 变橙，再旧就是红 */
const SEC_RULES: Record<Exclude<FreshKind, 'post'>, { ok: number; warn: number }> = {
  tape: { ok: 10, warn: 60 },
  sentiment: { ok: 60, warn: 300 },
  account: { ok: 30, warn: 180 },
  model: { ok: 60, warn: 600 },
  overseas: { ok: 7200, warn: 43200 },
}

/** §6.1 静默阈值（秒）。scan 盘后放宽到 30 分钟 */
export const SCAN_SILENT_SEC = 300
export const SCAN_SILENT_OFFDAY_SEC = 1800
export const TAPE_SILENT_SEC = 30
export const MODEL_SILENT_SEC = 600
export const CHANNEL_SILENT_SEC = 60

const UP_STATUS = new Set(['ok', 'up', 'running', 'healthy', 'ready', 'live'])
const AWAITING = new Set(['approved', 'risk_checking', 'pending', '待确认'])

/* ── 时间解析 ─────────────────────────────────────────────── */

/** 解析后端时间戳（RFC3339 / "2006-01-02 15:04:05" / "15:04:05"）。解析不了返回 null，不猜。 */
export function parseStamp(text: string | null | undefined): number | null {
  const raw = (text ?? '').trim()
  if (!raw) return null
  const direct = Date.parse(raw)
  if (Number.isFinite(direct)) return direct
  const clock = /^(\d{1,2}):(\d{2})(?::(\d{2}))?$/.exec(raw)
  if (clock) {
    const day = new Date()
    day.setHours(Number(clock[1]), Number(clock[2]), Number(clock[3] ?? 0), 0)
    return day.getTime()
  }
  return null
}

/** 取日期部分成 yyyymmdd 整数，避开时区把日期推前一天。 */
export function stampDay(text: string | null | undefined): number | null {
  const match = /(\d{4})-(\d{2})-(\d{2})/.exec((text ?? '').trim())
  if (match) return Number(match[1]) * 10000 + Number(match[2]) * 100 + Number(match[3])
  const parsed = parseStamp(text)
  if (parsed == null) return null
  const date = new Date(parsed)
  return date.getFullYear() * 10000 + (date.getMonth() + 1) * 100 + date.getDate()
}

function dayStamp(now: number): number {
  const date = new Date(now)
  return date.getFullYear() * 10000 + (date.getMonth() + 1) * 100 + date.getDate()
}

/** 两个日期之间隔了几个交易日（只跳过周末，节假日按交易日走，与 timeband 同口径）。 */
export function tradingDaysBetween(fromDay: number, toDay: number): number {
  if (!fromDay || !toDay || toDay <= fromDay) return 0
  const year = Math.floor(fromDay / 10000)
  const month = Math.floor((fromDay % 10000) / 100)
  const day = fromDay % 100
  let cursor = new Date(year, month - 1, day)
  let count = 0
  for (let step = 0; step < 40; step += 1) {
    cursor = new Date(cursor.getFullYear(), cursor.getMonth(), cursor.getDate() + 1)
    const stamp = cursor.getFullYear() * 10000 + (cursor.getMonth() + 1) * 100 + cursor.getDate()
    if (stamp > toDay) break
    const week = cursor.getDay()
    if (week !== 0 && week !== 6) count += 1
  }
  return count
}

/* ── 文字与色调 ───────────────────────────────────────────── */

/** 短年龄："3s" / "5m" / "2h" / "3d" */
export function ageShort(ageSec: number): string {
  const sec = Math.max(0, Math.floor(ageSec))
  if (sec < 60) return `${sec}s`
  if (sec < 3600) return `${Math.floor(sec / 60)}m`
  if (sec < 86400) return `${Math.floor(sec / 3600)}h`
  return `${Math.floor(sec / 86400)}d`
}

/** 角标文案。秒级加「前」，日粒度写交易日差 */
export function ageText(ageSec: number): string {
  return `${ageShort(ageSec)} 前`
}

/** 日粒度角标（盘后数据 / 非交易日）：今日 → 上一交易日 → N 个交易日前 */
export function dayText(days: number): string {
  if (days <= 0) return '今日'
  if (days === 1) return '上一交易日'
  return `${days} 个交易日前`
}

/** 日粒度色调：当日正常，前一交易日变橙，更早变红（§6.2 盘后行） */
export function dayTone(days: number): FreshTone {
  if (days <= 0) return 'ok'
  if (days === 1) return 'warn'
  return 'danger'
}

/** 秒级色调：正常 / 变橙 / 变红 */
export function secTone(kind: FreshKind, ageSec: number): FreshTone {
  if (kind === 'post') return 'ok'
  const rule = SEC_RULES[kind]
  if (ageSec <= rule.ok) return 'ok'
  if (ageSec <= rule.warn) return 'warn'
  return 'danger'
}

/** 兼容入口：给一个 kind 和年龄直接要角标。非交易日走日粒度需自行用 dayTone。 */
export function freshBadge(kind: FreshKind, ageSec: number): CardFresh {
  return { text: ageText(ageSec), tone: secTone(kind, ageSec) }
}

/** 静默文案：两分钟以内数秒，更长数分钟（§4.4.1 / §6.1） */
export function silenceText(ageSec: number): string {
  const sec = Math.max(0, Math.floor(ageSec))
  if (sec < 120) return `${sec} 秒没有新数据`
  return `${Math.floor(sec / 60)} 分钟没有动静`
}

/** 数据源（或 ops 源）名字判类型。先外围再行情：「外围行情」带「行情」二字，但阈值按外围走 */
export function sourceKind(id: string, name = ''): FreshKind {
  const text = `${id} ${name}`.toLowerCase()
  if (/overseas|外围|global|macro|外盘/.test(text)) return 'overseas'
  if (/eastmoney|东财|东方财富|涨停|limit|情绪|sentiment/.test(text)) return 'sentiment'
  if (/tdx|通达信|akshare|pyworker|行情|quote|market|tick/.test(text)) return 'tape'
  return 'post'
}

/** 数据源在牌面上的节点 id（与 scene-model 的 slug 同口径：trim + 小写） */
export function sourceNodeId(id: string, name = ''): string {
  return `source:${(id || name).trim().toLowerCase()}`
}

/* ── 逐牌新鲜度 ───────────────────────────────────────────── */

interface FreshCtx {
  now: number
  /** 交易日（周末与节假日一律按盘后阈值，与 timeband 同口径） */
  trading: boolean
  /** 本次采样年龄（秒）。接口挂住不返回时会一直涨 */
  sampleAge: number | null
}

/**
 * 算出每张牌右上角的角标。没有时间戳的牌用采样年龄，宁可说「刚读到」也不编时间。
 */
export function cardFreshness(input: MonitorInputLike, now: number, trading: boolean): Map<string, CardFresh> {
  const sampledAt = input.sampledAt ?? input.now
  const ctx: FreshCtx = {
    now,
    trading,
    sampleAge: sampledAt ? Math.max(0, (now - sampledAt) / 1000) : null,
  }
  const out = new Map<string, CardFresh>()
  const put = (id: string, kind: FreshKind, stamp: string | null | undefined, fallback = true) => {
    const fresh = badgeFor(ctx, kind, stamp, fallback)
    if (fresh) out.set(id, fresh)
  }

  for (const item of input.sources?.ok ? input.sources.items : []) {
    put(sourceNodeId(item.id, item.name), sourceKind(item.id, item.name), item.lastAt, false)
  }
  if (input.quality?.ok) put('source:quality', 'post', oldestQualityDay(input), false)

  if (input.sentiment?.ok) {
    put('post:scan', 'sentiment', input.sentiment.asOf, false)
    put('book:tape', 'sentiment', input.sentiment.asOf, false)
  }
  if (input.signals?.ok) {
    put('post:desk', 'account', newestSignalTime(input))
    for (const id of ['dim:tech', 'dim:capital', 'dim:news', 'dim:overseas', 'dim:synth']) put(id, 'model', newestSignalTime(input))
  }
  if (input.candidates?.ok) put('post:miss', 'account', null)
  if (input.risk?.ok) put('post:risk', 'account', null)
  if (input.account?.ok) put('book:center', 'account', null)
  if (input.orders?.ok) put('book:orders', 'account', null)
  if (input.fills?.ok) put('book:fills', 'account', newestFillTime(input))

  if (input.brainStats?.ok) {
    put('model:large', 'model', null)
    put('model:small', 'model', null)
  }
  if (input.brainP1?.ok && input.brainP1.usage) put('model:token', 'model', null)

  return out
}

function badgeFor(ctx: FreshCtx, kind: FreshKind, stamp: string | null | undefined, useSampleAge: boolean): CardFresh | null {
  /* 非交易日：所有牌按盘后阈值（§6.2 脚注） */
  const daily = !ctx.trading || kind === 'post'
  const day = stampDay(stamp)
  if (daily) {
    if (day == null) {
      if (!useSampleAge || ctx.sampleAge == null) return null
      return { text: `采样 ${ageText(ctx.sampleAge)}`, tone: secTone(kind, ctx.sampleAge) }
    }
    /* 交易日差：周末看周五的数据也是「上一交易日」，不是「今日」 */
    const today = dayStamp(ctx.now)
    const gap = day === today ? 0 : Math.max(1, tradingDaysBetween(day, today))
    return { text: dayText(gap), tone: dayTone(gap) }
  }
  const at = parseStamp(stamp)
  if (at != null) {
    const age = Math.max(0, (ctx.now - at) / 1000)
    return { text: ageText(age), tone: secTone(kind, age) }
  }
  if (!useSampleAge || ctx.sampleAge == null) return null
  return { text: `采样 ${ageText(ctx.sampleAge)}`, tone: secTone(kind, ctx.sampleAge) }
}

function oldestQualityDay(input: MonitorInputLike): string | null {
  let oldest: number | null = null
  let text: string | null = null
  for (const row of input.quality?.items ?? []) {
    const day = stampDay(row.tradeDate)
    if (day == null) continue
    if (oldest == null || day < oldest) {
      oldest = day
      text = row.tradeDate
    }
  }
  return text
}

function newestSignalTime(input: MonitorInputLike): string | null {
  let newest: number | null = null
  let text: string | null = null
  for (const item of input.signals?.items ?? []) {
    const at = parseStamp(item.time)
    if (at == null) continue
    if (newest == null || at > newest) {
      newest = at
      text = item.time
    }
  }
  return text
}

function newestFillTime(input: MonitorInputLike): string | null {
  let newest: number | null = null
  let text: string | null = null
  for (const item of input.fills?.items ?? []) {
    const at = parseStamp(item.time)
    if (at == null) continue
    if (newest == null || at > newest) {
      newest = at
      text = item.time
    }
  }
  return text
}

/* ── 逐牌静默 ─────────────────────────────────────────────── */

export interface SilenceCtx {
  now: number
  trading: boolean
  activity: ActivityLog
}

/** 有信号在等确认（approved / risk_checking）时，模型静默才算异常（§6.1） */
export function awaitingSignal(input: MonitorInputLike): boolean {
  return (input.signals?.items ?? []).some((item) => AWAITING.has(item.status.toLowerCase()))
}

/** 每轮轮询末尾把整张图喂给活动条。签名只取牌面可见字段（状态/主词/副行），不看 detail 长文。 */
export function observeGraph(activity: ActivityLog, graph: SceneGraph, input: MonitorInputLike, now: number): void {
  for (const node of graph.nodes) {
    activity.observe(node.id, `${node.status}${node.face}${node.sub}`, now, stampFor(input, node.id))
  }
  for (const order of input.orders?.items ?? []) {
    activity.observe(`order:${order.id}`, order.status, now)
  }
}

/** 与 cardFreshness 同一套时间戳映射：数据源 lastAt、扫描/情绪 asOf、订单/成交取最新成交时刻 */
function stampFor(input: MonitorInputLike, id: string): number | null {
  if (id.startsWith('source:')) {
    for (const item of input.sources?.items ?? []) {
      if (sourceNodeId(item.id, item.name) === id) return parseStamp(item.lastAt)
    }
    return null
  }
  if (id === 'post:scan' || id === 'book:tape') return parseStamp(input.sentiment?.asOf)
  if (id === 'post:desk') return parseStamp(newestSignalTime(input))
  if (id === 'book:orders' || id === 'book:fills') return parseStamp(newestFillTime(input))
  return null
}

/**
 * 逐牌静默。只对 live / idle 的牌下结论；时间不够长不报。
 * 行情源、扫描、模型的判据分别是自带时间戳年龄、asOf 年龄、活动条。
 */
export function cardSilence(input: MonitorInputLike, ctx: SilenceCtx): Map<string, CardSilence> {
  const out = new Map<string, CardSilence>()
  const ageOf = (stamp: string | null | undefined): number | null => {
    const at = parseStamp(stamp)
    return at == null ? null : Math.max(0, (ctx.now - at) / 1000)
  }

  /* 行情源：30 秒没新数据就转 attention（§6.1 第一行） */
  if (ctx.trading) {
    for (const item of input.sources?.items ?? []) {
      const kind = sourceKind(item.id, item.name)
      if (kind !== 'tape') continue
      if (!UP_STATUS.has(String(item.status ?? '').toLowerCase())) continue
      const age = ageOf(item.lastAt)
      if (age == null || age < TAPE_SILENT_SEC) continue
      out.set(sourceNodeId(item.id, item.name), { text: silenceText(age), alert: false })
    }
  }

  /* 扫描：盘中 5 分钟、盘后 30 分钟（§6.1 第二行） */
  const scanAge = ageOf(input.sentiment?.asOf)
  const scanLimit = ctx.trading ? SCAN_SILENT_SEC : SCAN_SILENT_OFFDAY_SEC
  if (scanAge != null && scanAge >= scanLimit) {
    out.set('post:scan', { text: silenceText(scanAge), alert: false })
  }

  /* 模型：有待确认信号时 10 分钟没动才算静默；判据只能是活动条 */
  if (ctx.trading && awaitingSignal(input)) {
    for (const id of ['model:large', 'model:small', 'dim:synth', 'dim:tech', 'dim:news', 'dim:capital', 'dim:overseas']) {
      if (ctx.activity.observedFor(id, ctx.now) < MODEL_SILENT_SEC) continue
      const dark = ctx.activity.darkFor(id, ctx.now)
      if (dark < MODEL_SILENT_SEC) continue
      out.set(id, { text: silenceText(dark), alert: false })
    }
  }

  return out
}

/** 交易通道：委托挂着超过 60 秒没有新成交 → 升告警（§6.1 第四行） */
export function channelSilence(input: MonitorInputLike, activity: ActivityLog, now: number, trading: boolean): { text: string; orderId: string } | null {
  if (!trading) return null
  const orders = input.orders?.items ?? []
  for (const order of orders) {
    const status = String(order.status ?? '')
    if (!/pending|accepted|submitted|partially|部分|待|已报|risk_checking/i.test(status)) continue
    const since = activity.observedFor(`order:${order.id}`, now)
    if (since < CHANNEL_SILENT_SEC) continue
    const lastFill = newestFillTime(input)
    const fillAt = parseStamp(lastFill)
    const filledRecently = fillAt != null && now - fillAt < CHANNEL_SILENT_SEC * 1000
    if (filledRecently) continue
    return {
      orderId: order.id,
      text: `${order.symbol} ${order.side} 委托挂起 ${ageShort(since)} 未成交，通道可能有阻塞`,
    }
  }
  return null
}

/* ── 落到图上 ─────────────────────────────────────────────── */

/**
 * 把角标、活动格、静默写回牌面。
 * 只升级：live / idle 遇到静默才转 attention，down / gap 保持原样（不掩盖已存在的问题）。
 */
export function decorate(
  graph: SceneGraph,
  fresh: Map<string, CardFresh>,
  silence: Map<string, CardSilence>,
  cells: Map<string, (boolean | null)[]>,
): SceneGraph {
  const nodes: SceneNode[] = graph.nodes.map((node) => {
    const badge = fresh.get(node.id)
    const quiet = silence.get(node.id)
    const next: SceneNode = { ...node }
    if (badge) next.fresh = badge
    const activity = cells.get(node.id)
    if (activity) next.activity = activity
    if (quiet && (node.status === 'live' || node.status === 'idle')) {
      next.status = 'attention' satisfies NodeStatus
      next.sub = quiet.text
    }
    return next
  })
  return { ...graph, nodes }
}

/** cardFreshness / cardSilence 只读这几个字段，用最小结构方便测试 */
export interface MonitorInputLike {
  now?: number
  sampledAt?: number
  lastPush?: number
  account?: { ok: boolean } | null
  risk?: { ok: boolean } | null
  brainStats?: { ok: boolean } | null
  brainP1?: { ok: boolean; usage?: unknown } | null
  candidates?: { ok: boolean } | null
  sources?: { ok: boolean; items: { id: string; name: string; status: string; detail: string; lastAt: string }[] } | null
  sentiment?: { ok: boolean; asOf: string; stale: boolean } | null
  signals?: { ok: boolean; items: { id: string; status: string; time: string }[] } | null
  quality?: { ok: boolean; items: { tradeDate: string }[] } | null
  fills?: { ok: boolean; items: { time: string }[] } | null
  orders?: { ok: boolean; items: { id: string; symbol: string; side: string; status: string }[] } | null
}
