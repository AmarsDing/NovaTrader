/**
 * S4 事件流 TAPE（监控业务监控方案 §4.6 / §6.1 / §6.3 / §7）：
 * 六域（信号/成交/风控/告警/数据/模型）统一时间线，是整个页面上唯一
 * 严格按时间顺序、不做任何聚合的原始流——其它所有视图都是它的摘要。
 *
 * 行形固定四列：[时分秒] [域标签] [内容] [标识符]；倒序，最多 TAPE_MAX_ROWS 行。
 * 数据源全部只读：notices + audit + listFills（轮询快照）+ WS 脉冲 + 三类状态事件
 * （健康分 <70 §6.3、模型静默 §6.1、WS 断开 §6.1）。
 * 不编行：时间解析不了就照排原文并写「时刻未知」，绝不猜。
 *
 * WS 脉冲 2.4s 后会从 input 过期（scene-model PULSE_TTL），状态事件要记住
 * 「第一次看到的时刻」——所以进流的事件由 TapeSession 在会话内累积；
 * 轮询类事件（notices/audit/fills）id 稳定，每帧按快照重算即可。
 */
import { amount, intText } from '@/utils/format'
import { parseStamp } from './freshness'
import type { HealthScore } from './health'
import type { CardSilence } from './freshness'
import type { Pulse } from './types'

export type TapeDomain = 'signal' | 'fill' | 'risk' | 'alert' | 'data' | 'model'

export const TAPE_DOMAIN_LABEL: Record<TapeDomain, string> = {
  signal: '信号',
  fill: '成交',
  risk: '风控',
  alert: '告警',
  data: '数据',
  model: '模型',
}

/** §4.6：倒序，最多 40 行 */
export const TAPE_MAX_ROWS = 40

/** §6.1：只有模型域的静默进事件流（行情源/扫描静默走牌面 attention，不入流） */
const MODEL_SILENCE_IDS = new Set(['model:large', 'model:small', 'dim:synth', 'dim:tech', 'dim:news', 'dim:capital', 'dim:overseas'])

export interface TapeRow {
  /** 去重键：同一事件反复轮询只算一行 */
  id: string
  /** 毫秒时刻，排序用；解析不了为 0（沉底） */
  at: number
  /** 时分秒列 */
  time: string
  domain: TapeDomain
  /** 内容列 */
  text: string
  /** 标识符列（代码/trace/规则号/审计对象），可为空串 */
  ident: string
  /** 点击选中的画布节点；空串 = 没有对应节点，该行不可点 */
  selectId: string
  /** 只给成交域用（红涨绿跌）：买 up / 卖 down */
  tone: 'up' | 'down' | 'flat'
}

/** absorb 一帧要看的快照。字段都可缺省，缺了就少几行，不猜 */
export interface TapeSnapshot {
  notices?: { ok: boolean; items: { id: string; title: string; level: string; time: string }[] } | null
  audit?: { ok: boolean; items: { id: string; action: string; target: string; time: string }[] } | null
  fills?: { ok: boolean; items: { id: string; symbol: string; side: string; price: number | null; qty: number | null; time: string }[] } | null
  pulses?: Pulse[] | null
  health?: HealthScore | null
  silence?: Map<string, CardSilence> | null
  /** WS 是否在线（hub.isLive）；undefined = 这一帧没观测，不做判断 */
  live?: boolean
}

/* ── 时间 ─────────────────────────────────────────────── */

export function clockText(ms: number): string {
  const date = new Date(ms)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

/** 后端时间戳 → { at, time }。解析不了：at=0 沉底，time 照排原文（不编时刻） */
export function stampOf(raw: string | null | undefined): { at: number; time: string } {
  const at = parseStamp(raw)
  if (at != null) return { at, time: clockText(at) }
  const text = (raw ?? '').trim()
  if (text.length >= 19) return { at: 0, time: text.slice(11, 19) }
  return { at: 0, time: text || '时刻未知' }
}

/* ── 域判定 ───────────────────────────────────────────── */

export function buySide(side: string | null | undefined): boolean {
  const key = String(side ?? '').toLowerCase()
  return key === 'buy' || key.includes('买')
}

export interface AuditClass {
  domain: TapeDomain
  selectId: string
  tone: TapeRow['tone']
}

/** 审计行按 动作+对象 关键词归域。归不进专门域的一律算「数据」（运行流水），不硬凑 */
export function auditDomain(action: string, target: string): AuditClass {
  const text = `${action} ${target}`.toLowerCase()
  if (/kill|risk|风控|急停|熔断|拒/.test(text)) return { domain: 'risk', selectId: 'post:risk', tone: 'flat' }
  if (/fill|order|trade|position|成交|委托|下单|持仓|买入|卖出/.test(text)) {
    const tone = /sell|卖/.test(text) ? 'down' : /buy|买/.test(text) ? 'up' : 'flat'
    return { domain: 'fill', selectId: 'book:center', tone }
  }
  if (/signal|信号/.test(text)) return { domain: 'signal', selectId: 'post:desk', tone: 'flat' }
  if (/brain|model|llm|模型|研判/.test(text)) return { domain: 'model', selectId: 'post:model', tone: 'flat' }
  return { domain: 'data', selectId: '', tone: 'flat' }
}

/** 脉冲 → 行。四条推送路径与 pulses.ts 的映射一一对应；selectId 恒为脉冲终点节点 */
export function rowFromPulse(pulse: Pulse): TapeRow {
  const value = pulse.identifier.value
  const at = pulse.at
  const base = { id: `p:${pulse.id}`, at, time: clockText(at), ident: value, selectId: pulse.to, tone: 'flat' as const }
  if (pulse.to === 'post:desk') return { ...base, domain: 'signal', text: '信号推送' }
  if (pulse.to === 'post:scan') return { ...base, domain: 'data', text: '行情推送' }
  if (pulse.from === 'post:risk' || pulse.to === 'book:center') return { ...base, domain: 'risk', text: '风控事件' }
  if (pulse.to === 'post:model') {
    return { ...base, domain: 'model', text: pulse.identifier.kind === 'trace' ? '模型链路' : '模型调用' }
  }
  return { ...base, domain: 'data', text: '推送' }
}

/** 轮询快照里的行（notices/audit/fills）。id 稳定，反复 absorb 不会重复 */
export function tapeRowsFrom(snapshot: TapeSnapshot): TapeRow[] {
  const rows: TapeRow[] = []
  if (snapshot.notices?.ok) {
    for (const item of snapshot.notices.items) {
      const stamp = stampOf(item.time)
      rows.push({
        id: `n:${item.id}`,
        at: stamp.at,
        time: stamp.time,
        domain: 'alert',
        text: item.title || '通知（标题未返回）',
        ident: item.level || '',
        selectId: '',
        tone: 'flat',
      })
    }
  }
  if (snapshot.audit?.ok) {
    for (const item of snapshot.audit.items) {
      const stamp = stampOf(item.time)
      const cls = auditDomain(item.action, item.target)
      rows.push({
        id: `a:${item.id}`,
        at: stamp.at,
        time: stamp.time,
        domain: cls.domain,
        text: item.action || '审计（动作未返回）',
        ident: item.target || '',
        selectId: cls.selectId,
        tone: cls.tone,
      })
    }
  }
  if (snapshot.fills?.ok) {
    for (const item of snapshot.fills.items) {
      const stamp = stampOf(item.time)
      const buy = buySide(item.side)
      const qty = item.qty == null ? '数量未返回' : intText(item.qty)
      const price = item.price == null ? '价格未返回' : amount(item.price)
      rows.push({
        id: `f:${item.id || `${item.symbol}|${item.time}`}`,
        at: stamp.at,
        time: stamp.time,
        domain: 'fill',
        text: `${buy ? '买入' : '卖出'} ${qty} @ ${price}`,
        ident: item.symbol,
        selectId: 'book:center',
        tone: buy ? 'up' : 'down',
      })
    }
  }
  return rows
}

/** 倒序；同刻按 id 升序，保证确定性 */
export function compareRows(a: TapeRow, b: TapeRow): number {
  if (a.at !== b.at) return b.at - a.at
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

/* ── 会话累积 ─────────────────────────────────────────── */

/**
 * 事件流会话（内存态，不落库不上传）。职责只有三件：
 * 1. 脉冲会过期，收进流里就不再丢；
 * 2. 状态事件（健康分/模型静默/WS 通断）记「第一次看到的时刻」，
 *    同一状态持续期间不刷屏，恢复后再次发生 = 新的一行；
 * 3. 与轮询行合并、倒序、切 40 行。
 */
export class TapeSession {
  private events: TapeRow[] = []
  private seenPulse = new Set<string>()
  private healthSig = ''
  private silent = new Set<string>()
  private liveState: boolean | null = null
  private readonly limit: number

  /** limit 是会话内累积上限（视图只切 40，多留些历史给暂停后恢复） */
  constructor(limit = 120) {
    this.limit = Math.max(TAPE_MAX_ROWS, limit)
  }

  absorb(snapshot: TapeSnapshot, now: number): TapeRow[] {
    for (const pulse of snapshot.pulses ?? []) {
      if (this.seenPulse.has(pulse.id)) continue
      this.seenPulse.add(pulse.id)
      this.evict(1)
      this.events.push(rowFromPulse(pulse))
    }
    this.absorbHealth(snapshot.health ?? null, now)
    this.absorbSilence(snapshot.silence ?? null, now)
    this.absorbLive(snapshot.live, now)
    const all = [...this.events, ...tapeRowsFrom(snapshot)]
    all.sort(compareRows)
    return all.slice(0, TAPE_MAX_ROWS)
  }

  /** §6.3：健康分 <70 → 一条告警行，必须列扣分项，绝不写「系统异常」。
   * 分数或扣分项变了才算新事件；回到 ≥70 后再次跌破会再记一条 */
  private absorbHealth(health: HealthScore | null, now: number): void {
    if (!health || health.score >= 70) {
      this.healthSig = ''
      return
    }
    const items = health.parts.filter((part) => part.deduction > 0).map((part) => `${part.label} -${part.deduction}`)
    const sig = `${health.score}|${items.join(',')}`
    if (sig === this.healthSig) return
    this.healthSig = sig
    this.evict(1)
    this.events.push({
      id: `h:${sig}:${now}`,
      at: now,
      time: clockText(now),
      domain: 'alert',
      text: `健康 ${health.score}：${items.join('，') || '扣分项未返回'}`,
      ident: '',
      selectId: '',
      tone: 'flat',
    })
  }

  /** §6.1：模型 10 分钟无调用（有待确认信号时）→ `模型 静默` 一条。
   * 静默解除后再次静默 = 新事件新行；行情源/扫描的静默不进流 */
  private absorbSilence(silence: Map<string, CardSilence> | null, now: number): void {
    const current = new Set<string>()
    for (const [id, entry] of silence ?? []) {
      if (!MODEL_SILENCE_IDS.has(id) || !entry?.text) continue
      current.add(id)
      if (this.silent.has(id)) continue
      this.silent.add(id)
      this.evict(1)
      this.events.push({
        id: `s:${id}:${now}`,
        at: now,
        time: clockText(now),
        domain: 'model',
        text: `静默 ${entry.text}`,
        ident: id.split(':')[1] ?? '',
        selectId: id,
        tone: 'flat',
      })
    }
    for (const id of this.silent) {
      if (!current.has(id)) this.silent.delete(id)
    }
  }

  /** §6.1：WS 断开 → 顶栏「不是实时」+ 事件流一条；恢复也补一条，不留悬案。
   * 首帧只记基线不记行——打开页面不该先看到一条「WS 恢复」 */
  private absorbLive(live: boolean | undefined, now: number): void {
    if (live == null) return
    if (this.liveState == null) {
      this.liveState = live
      return
    }
    if (live === this.liveState) return
    this.liveState = live
    this.evict(1)
    this.events.push({
      id: `w:${live ? 'on' : 'off'}:${now}`,
      at: now,
      time: clockText(now),
      domain: 'data',
      text: live ? 'WS 恢复，推送回到实时' : 'WS 断开，页面不是实时',
      ident: '',
      selectId: '',
      tone: 'flat',
    })
  }

  /** 累积超上限时挤掉最旧的；脉冲 id 同步放行（脉冲本来就会过期，不会回来） */
  private evict(incoming: number): void {
    const over = this.events.length + incoming - this.limit
    if (over <= 0) return
    const dropped = this.events.splice(0, over)
    for (const row of dropped) {
      if (row.id.startsWith('p:')) this.seenPulse.delete(row.id.slice(2))
    }
  }
}
