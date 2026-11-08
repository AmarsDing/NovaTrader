/**
 * 决策台用的纯函数：缺口判断 + 一张票拼接（P3）。
 * 界面只负责取数和排版，规则放在这里方便单测。
 * 策略名 / 漏选原因的中文口径集中在 utils/format.ts，这里只做转发，保持旧引用不破。
 */

import type { Candidate, SignalItem } from '@/api/types/strategy'
import type { AuctionSummary } from '@/api/market'
import { formatDateTime, parseTime, sessionOf } from '@/utils/time'
import { isValidExpired, missLabel, price, validRemainText } from '@/utils/format'
import { STATUS_MAP, type StatusEntry } from '@/ui/status-map'

export { strategyLabel, missLabel } from '@/utils/format'

export const PAPER_DAYS_REQUIRED = 20

export type GapLevel = 'block' | 'warn'

export interface DeskGap {
  id: string
  level: GapLevel
  text: string
  /** 点进去看细节的面板 id。 */
  panel: string
}

export interface DeskInput {
  sourcesFailed: boolean
  sourcesEmpty: boolean
  sourcesBlocked: number
  noTape: boolean
  briefingMissing: boolean
  briefingStale: boolean
  anyDegraded: boolean
  paperDays: number | null
  killActive: boolean
  live: boolean
}

/** 断路器打开、源不可用、明确报错，都算这条源现在不能用。 */
export function sourceBlocked(status: string): boolean {
  const state = status.toLowerCase()
  return state === 'open' || state === 'unavailable' || state.includes('down') || state.includes('error')
}

export function countBlockedSources(rows: { enabled: boolean; status: string }[]): number {
  return rows.filter((row) => row.enabled && sourceBlocked(row.status)).length
}

/** 半开、探测中或未知状态不是已确认可用；数据域是否足够还要看本轮市场快照。 */
export function hasAvailableSource(rows: { enabled: boolean; status: string }[]): boolean {
  return rows.some((row) => row.enabled && ['closed', 'healthy', 'ok'].includes(row.status.toLowerCase()))
}

/**
 * 按阻断程度排列。没有缺口时返回空数组，界面显示「链路齐」。
 * 模拟盘天数未知不算缺口：账户服务没通时，上面的数据源/快照会先说话。
 */
export function buildGaps(input: DeskInput): DeskGap[] {
  const gaps: DeskGap[] = []
  if (input.killActive) {
    gaps.push({
      id: 'kill',
      level: 'block',
      text: '急停已触发，买入不会执行',
      panel: 'risk-chain',
    })
  }
  if (input.sourcesFailed) {
    gaps.push({
      id: 'sources',
      level: 'block',
      text: '数据源服务没有应答，行情和新闻都不可信',
      panel: 'datasource-health',
    })
  } else if (input.sourcesEmpty) {
    gaps.push({
      id: 'sources-empty',
      level: 'warn',
      text: '没有登记数据源',
      panel: 'datasource-health',
    })
  } else if (input.sourcesBlocked > 0) {
    gaps.push({
      id: 'sources-blocked',
      level: 'warn',
      text: `${input.sourcesBlocked} 个数据源不可用。通达信插件、Tushare 或新闻令牌没接上时，盘面和情报都是空的`,
      panel: 'datasource-health',
    })
  }
  if (input.noTape) {
    gaps.push({
      id: 'tape',
      level: 'block',
      text: '全市场快照未入库，盘中选股没有价格',
      panel: 'datasource-health',
    })
  }
  if (!input.live) {
    gaps.push({
      id: 'live',
      level: 'warn',
      text: '行情通道不是实时，屏上数字停在上次推送',
      panel: 'ops',
    })
  }
  if (input.briefingMissing) {
    gaps.push({
      id: 'brief',
      level: 'warn',
      text: '今日晨报没有生成。模型地址未配时只有降级晨报，并且要到 08:30',
      panel: 'morning-briefing',
    })
  } else if (input.briefingStale) {
    gaps.push({
      id: 'brief-stale',
      level: 'warn',
      text: '晨报已过期，不要拿它当今天的结论',
      panel: 'morning-briefing',
    })
  }
  if (input.anyDegraded) {
    gaps.push({
      id: 'ai',
      level: 'warn',
      text: '信号走规则分。四维模型未在线，消息面和资金面没有复核',
      panel: 'brain-monitor',
    })
  }
  if (input.paperDays != null && input.paperDays < PAPER_DAYS_REQUIRED) {
    gaps.push({
      id: 'paper',
      level: 'warn',
      text: `模拟账本累计 ${input.paperDays}/${PAPER_DAYS_REQUIRED} 日；连续合格运行证据需另核验，累计天数不代表实盘许可`,
      panel: 'trade',
    })
  }
  return gaps
}

/* ================= FE-O1 四卡：可交易状态 ================= */

export interface TradeStateCard {
  /** 阻断 n / 链路齐 */
  value: string
  tone: 'neutral' | 'warn' | 'danger'
  sub: string
}

/**
 * FE-O1 四卡之「可交易状态」：阻断优先于告警，值只给阻断数，子句留告警数。
 * 链路齐是常态不是利好，tone 保持 neutral 不着色。
 */
export function buildTradeState(gaps: DeskGap[]): TradeStateCard {
  const blocks = gaps.filter((gap) => gap.level === 'block').length
  const warns = gaps.length - blocks
  if (blocks > 0) {
    return {
      value: `阻断 ${blocks}`,
      tone: 'danger',
      sub: warns > 0 ? `另有 ${warns} 项告警，点击下方清单定位` : '点击下方清单定位',
    }
  }
  if (warns > 0) return { value: '链路齐', tone: 'warn', sub: `${warns} 项告警待看` }
  return { value: '链路齐', tone: 'neutral', sub: '链路检查全部通过' }
}

/* ================= 一张票（P3） =================
 * 只用现有信号/候选字段拼，不新开模型调用、不编理由：
 * 没有研判正文时写「未调用」或「规则分」，由界面原样展示。
 */

export type TicketLane = 'pending' | 'executed'

export interface DimVote {
  /** brain 的四维键：technical / news / capital / external */
  key: string
  label: string
  /** 这一维的分；模型没跑到是 null，界面写「—」 */
  score: number | null
  /** < 50 记反对（中立线 50 与 brain neutralScore 同口径） */
  against: boolean
  /** 模型给这一维的一句话评语（dims 对象值带 comment 才有，scale 票体收编四维评语的载体）；无为空串 */
  comment: string
}

export interface TicketRisk {
  /** 信号建议仓位（0–1），来自 positionPct */
  requestedPct: number | null
  /** 按当前风控参数估算的允许仓位；参数没读到为 null，界面写「以风控实检为准」 */
  allowedPct: number | null
  /** 夹住它的规则号（证据里的 Rxx），没有为空串 */
  rule: string
  /** 给界面原样展示的一行说明 */
  note: string
}

export interface DeskTicket {
  id: string
  executedPrice?: number | null
  executedVolume?: number | null
  book: string
  strategyVersionId: string
  traceId: string
  lane: TicketLane
  symbol: string
  name: string
  side: string
  strategy: string
  /** 综合分（finalScore）；模型降级时界面改标「规则分」 */
  score: number | null
  ruleScore: number | null
  aiScore: number | null
  degraded: boolean
  highValue: boolean
  status: string
  /** 形成日（交易日），已执行票标题用 */
  tradeDate: string
  time: string
  entry: number | null
  entryLow: number | null
  entryHigh: number | null
  stop: number | null
  take: number | null
  /** ATR（信号卡事实位，决策台价区同源显示） */
  atr: number | null
  /** 已了结票的出场事实（SignalView 卡尾同源字段，票体收编时一并归位） */
  exitPrice: number | null
  closedAt: string
  exitKind: string
  /** 后验盈亏是否有效；hasPnl 为真才带 pnlPct */
  hasPnl: boolean
  pnlPct: number | null
  holdingDays: number | null
  validUntil: string
  holdDaysMax: number | null
  votes: DimVote[]
  /** 异议先行：反对维与降级说明，一票只留一轮 */
  objections: string[]
  /** 1–5 条理由，拆自信号 reason；空就是空，界面写「未调用」/「规则分」 */
  reasons: string[]
  /** 原始规则/研判理由，行动层沿用，不重新生成。 */
  reason: string
  evidence: string[]
  risk: TicketRisk
  /** 已执行票的一句话了结（平仓方式 + 后验盈亏）；待执行为空串 */
  closedNote: string
}

export interface MissTicket {
  symbol: string
  name: string
  strategy: string
  stage: string
  /** 漏斗原值；中文口径由 missLabel 转 */
  reason: string
  ruleScore: number | null
  finalScore: number | null
}

const DIM_DEFS: { key: string; label: string; aliases: string[] }[] = [
  { key: 'technical', label: '技术面', aliases: ['technical', 'tech', '技术面', '技术'] },
  { key: 'news', label: '消息面', aliases: ['news', '消息面', '消息'] },
  { key: 'capital', label: '资金面', aliases: ['capital', '资金面', '资金'] },
  { key: 'external', label: '外围（情绪）', aliases: ['external', 'overseas', '外围', '外围/情绪'] },
]

/** 中立线：与 brain neutralScore 同口径，低于它记反对 */
export const DIM_NEUTRAL = 50

const PENDING_STATUS = new Set(['approved', 'risk_checking', 'pending'])
const EXECUTED_STATUS = new Set(['executing', 'done', 'closed'])

/** 待执行口径：approved / risk_checking / pending。CommandBar 的「待执行 N」与决策台左栏用同一套。 */
export function isPendingStatus(status: string): boolean {
  return PENDING_STATUS.has(status.toLowerCase())
}

function dimScore(raw: unknown): number | null {
  if (typeof raw === 'number' && Number.isFinite(raw)) return raw
  if (raw && typeof raw === 'object') {
    const score = (raw as Record<string, unknown>).score
    if (typeof score === 'number' && Number.isFinite(score)) return score
  }
  return null
}

/** 评语抽取：dims 对象值带非空 comment 才取（与 utils/format dimComment 同口径，此处一并进票不另查）。 */
function dimCommentOf(raw: unknown): string {
  if (raw && typeof raw === 'object') {
    const comment = (raw as Record<string, unknown>).comment
    if (typeof comment === 'string' && comment.trim()) return comment.trim()
  }
  return ''
}

/** 四维投票：按固定四行出，缺的维给 null 而不是跳过，界面才看得出哪一维没评 */
export function dimVotes(dims: Record<string, unknown>): DimVote[] {
  const lookup = new Map<string, unknown>()
  for (const [key, value] of Object.entries(dims ?? {})) lookup.set(key.toLowerCase(), value)
  return DIM_DEFS.map((def) => {
    let raw: unknown
    for (const alias of def.aliases) {
      if (lookup.has(alias.toLowerCase())) {
        raw = lookup.get(alias.toLowerCase())
        break
      }
    }
    const score = dimScore(raw)
    return { key: def.key, label: def.label, score, against: score != null && score < DIM_NEUTRAL, comment: dimCommentOf(raw) }
  })
}

/** 异议：反对维在前；模型降级单独一条。只保留一轮，不循环辩论 */
export function objectionsOf(votes: DimVote[], degraded: boolean): string[] {
  const out = votes.filter((vote) => vote.against).map((vote) => `${vote.label} ${vote.score} 分，反对`)
  if (degraded) out.push('模型降级，只有规则分，消息面和资金面未经复核')
  return out
}

/** 理由拆条：按句号/分号/换行切，去空去重，最多 5 条 */
export function splitReasons(reason: string): string[] {
  const parts = reason
    .split(/[。；;\n]+/)
    .map((part) => part.trim())
    .filter(Boolean)
  const seen = new Set<string>()
  const out: string[] = []
  for (const part of parts) {
    if (seen.has(part)) continue
    seen.add(part)
    out.push(part)
    if (out.length >= 5) break
  }
  return out
}

/** 证据/理由里出现的第一个风控规则号（R00–R16 口径） */
export function ruleOf(ticket: { evidence: string[]; reason?: string }): string {
  const haystack = [...ticket.evidence, ticket.reason ?? ''].join(' ')
  const match = /R\d{2}/.exec(haystack)
  return match ? match[0] : ''
}

/**
 * 盈亏比（入场区间中值 vs 止损/止盈）：决策台票体与信号中枢卡片同源，唯一出处。
 * 缺任一腿或止损不在入场区间之下不折算，显「—」——缺位不是 0R。
 */
export function riskRewardOf(ticket: {
  entryLow: number | null
  entryHigh: number | null
  stop: number | null
  take: number | null
}): string {
  if (ticket.entryLow == null || ticket.entryHigh == null || ticket.stop == null || ticket.take == null) return '—'
  const mid = (ticket.entryLow + ticket.entryHigh) / 2
  const risk = mid - ticket.stop
  if (!(risk > 0)) return '—'
  return `${((ticket.take - mid) / risk).toFixed(1)}R`
}

/**
 * 风控块。允许仓位只按已读到的风控参数估算，读不到就如实写空；
 * 无论哪种都带「以风控实检为准」——真正的夹紧发生在下单时（trade 引擎按风控裁决改写）。
 */
export function ticketRisk(
  signal: Pick<SignalItem, 'positionPct' | 'evidence' | 'reason'>,
  maxSinglePct: number | null,
): TicketRisk {
  const requestedPct = signal.positionPct
  const rule = ruleOf({ evidence: signal.evidence, reason: signal.reason })
  let allowedPct: number | null = null
  const notes: string[] = []
  if (requestedPct != null && maxSinglePct != null && maxSinglePct > 0) {
    allowedPct = Math.min(requestedPct, maxSinglePct)
    if (requestedPct > allowedPct) notes.push('夹掉的部分留在现金')
  }
  notes.push('以风控实检为准')
  return { requestedPct, allowedPct, rule, note: notes.join('；') }
}

function closedNote(signal: SignalItem): string {
  if (signal.status === 'closed' && signal.exitKind) {
    const pnl = signal.hasPnl && signal.pnlPct != null ? `，后验 ${signal.pnlPct.toFixed(1)}%` : ''
    return `已了结（${signal.exitKind}${pnl}）`
  }
  if (signal.status === 'executing') return '下单中'
  if (signal.status === 'done') return signal.executedVolume != null ? `已有成交 ${signal.executedVolume} 股` : '已有成交，数量待核验'
  return ''
}

function toTicket(signal: SignalItem, lane: TicketLane, maxSinglePct: number | null): DeskTicket {
  const votes = dimVotes(signal.dims)
  return {
    id: signal.id,
    executedPrice: signal.executedPrice,
    executedVolume: signal.executedVolume,
    book: signal.book,
    strategyVersionId: signal.strategyVersionId,
    traceId: signal.traceId,
    lane,
    symbol: signal.symbol,
    name: signal.name,
    side: signal.side,
    strategy: signal.strategy,
    score: signal.score,
    ruleScore: signal.ruleScore,
    aiScore: signal.aiScore,
    degraded: signal.degraded,
    highValue: signal.highValue,
    status: signal.status,
    tradeDate: signal.tradeDate,
    time: signal.time,
    entry: signal.entry,
    entryLow: signal.entryLow,
    entryHigh: signal.entryHigh,
    stop: signal.stop,
    take: signal.take,
    atr: signal.atr,
    exitPrice: signal.exitPrice,
    closedAt: signal.closedAt,
    exitKind: signal.exitKind,
    hasPnl: signal.hasPnl,
    pnlPct: signal.pnlPct,
    holdingDays: signal.holdingDays,
    validUntil: signal.validUntil,
    holdDaysMax: signal.holdDaysMax,
    votes,
    objections: objectionsOf(votes, signal.degraded),
    reasons: splitReasons(signal.reason),
    reason: signal.reason,
    evidence: signal.evidence,
    risk: ticketRisk(signal, maxSinglePct),
    closedNote: lane === 'executed' ? closedNote(signal) : '',
  }
}

/** 信号中枢卡片票体（W2 全量收编 TicketDetail 的转换口）：lane 按状态归位——执行/了结票带出场事实与成交说明，其余归待执行位。 */
export function toDeskTicket(signal: SignalItem, maxSinglePct: number | null = null): DeskTicket {
  return toTicket(signal, EXECUTED_STATUS.has(signal.status.toLowerCase()) ? 'executed' : 'pending', maxSinglePct)
}

/**
 * 待执行 = approved / risk_checking（被 Rxx 拒绝的不进这里）；已执行 = executing / done / closed。
 * 其余状态（rejected / expired / cancelled）不进左栏。
 */
export function buildTickets(
  signals: SignalItem[],
  maxSinglePct: number | null = null,
): { pending: DeskTicket[]; executed: DeskTicket[] } {
  const pending: DeskTicket[] = []
  const executed: DeskTicket[] = []
  for (const signal of signals) {
    const status = signal.status.toLowerCase()
    if (PENDING_STATUS.has(status)) pending.push(toTicket(signal, 'pending', maxSinglePct))
    else if (EXECUTED_STATUS.has(status)) executed.push(toTicket(signal, 'executed', maxSinglePct))
  }
  // 保护性退出优先，买入队列内部仍按原有策略分排序。
  pending.sort((a, b) => Number(b.side === 'sell') - Number(a.side === 'sell') || (b.score ?? 0) - (a.score ?? 0))
  executed.sort((a, b) => (b.time || '').localeCompare(a.time || ''))
  return { pending, executed }
}

/** 漏选列：漏斗里没入选且带原因的候选，按规则分倒序。FE-O3 起不再截断，完整列表交给分组与滚动收口。 */
export function buildMissed(candidates: Candidate[]): MissTicket[] {
  return candidates
    .filter((row) => row.stage !== 'selected' && row.missReason)
    .sort((a, b) => (b.ruleScore ?? 0) - (a.ruleScore ?? 0))
    .map((row) => ({
      symbol: row.symbol,
      name: row.name,
      strategy: row.strategy,
      stage: row.stage,
      reason: row.missReason,
      ruleScore: row.ruleScore,
      finalScore: row.finalScore,
    }))
}

/** FE-O3 漏选分组：一组一个 missReason，计数头用的聚合结果。 */
export interface MissGroup {
  reason: string
  /** missLabel 转好的中文口径 */
  label: string
  count: number
  rows: MissTicket[]
}

/** 组按条数降序（同数按中文标签定序保证稳定），组内保持 buildMissed 的规则分倒序。 */
export function groupMissed(rows: MissTicket[]): MissGroup[] {
  const byReason = new Map<string, MissTicket[]>()
  for (const row of rows) {
    const list = byReason.get(row.reason)
    if (list) list.push(row)
    else byReason.set(row.reason, [row])
  }
  return [...byReason.entries()]
    .map(([reason, groupRows]) => ({
      reason,
      label: missLabel(reason),
      count: groupRows.length,
      rows: groupRows,
    }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label, 'zh'))
}

/* ================= FE-O2：票卡徽标与有效期倒计时（左列票卡标准化的纯函数收口） ================= */

/** signal 册查词：动态状态码查册，册外返回 null（模板原样回显原始状态，禁自封文案）。 */
function signalEntry(status: string): StatusEntry | null {
  return (STATUS_MAP as Readonly<Record<string, StatusEntry>>)[`signal.${status}`] ?? null
}

/** 票卡状态徽标（FE-O2）：文案/tone 唯一出处=signal 册；未终态且有效期已过按「已过期」论
 *  （与信号中枢 stateBadge 同口径）；终态（已有成交/已平仓/已取消/已拦截）不做过期覆盖。 */
export function laneBadge(ticket: Pick<DeskTicket, 'status' | 'validUntil'>, now: number): StatusEntry | null {
  const status = ticket.status.toLowerCase() === 'canceled' ? 'cancelled' : ticket.status.toLowerCase()
  const terminal = status === 'done' || status === 'closed' || status === 'cancelled' || status === 'rejected'
  if (!terminal && isValidExpired(ticket.validUntil, now)) return signalEntry('expired')
  return signalEntry(status)
}

/** 票卡有效期倒计时（FE-O2）：缺有效期写「无有效期」；余量走 validRemainText，过点/无法解析写「已过期」
 *  （与票体 valid 行同口径，卡片行是列表不是票头，档位压低不抢读数）。 */
export function laneRemainText(validUntil: string, now: number): string {
  if (!validUntil) return '无有效期'
  return validRemainText(validUntil, now) || '已过期'
}

/* ================= FE-O5：竞价强弱条（09:15–09:30 时段显示，过时段自动隐藏） ================= */

export interface AuctionStrip {
  /** false=截面未落库/接口未应答——时段内条仍显（模板给缺口文案）；时段外整条 null 不渲。 */
  found: boolean
  /** 全市场竞价成交额（元）。 */
  totalAmount: number
  /** 全市场竞价涨停家数。 */
  limitUpCount: number
  /** 参与样本只数（竞价涨停 ∪ 抢筹榜按代码去重；列表是 top 截断样本，非全市场口径）。 */
  sampleCount: number
  /** 样本竞价均涨幅（百分点口径，与 pkg/market/auction.go auction_pct=(open-preClose)/preClose*100 同源）；
   *  样本全部未返回涨幅 → null 显「—」（没取到≠0）。 */
  avgPct: number | null
  /** 样本未匹配净买量合计（股；auction_unmatched=bv−av，09:20–09:25 无快照为 null）；
   *  全部未返回 → null 显「—」。 */
  unmatchedShares: number | null
  asOf: string
}

/** 竞价强弱条：仅集合竞价时段（09:15–09:30，上海钟）出条；指标由 GetAuction 截面推导，
 *  越页签/过时段由宿主用 serverNow 判定（P2-10）。 */
export function buildAuctionStrip(summary: AuctionSummary | null, nowMs: number): AuctionStrip | null {
  if (sessionOf(new Date(nowMs)) !== 'auction') return null
  const rows = [...(summary?.limitUps ?? []), ...(summary?.topRush ?? [])]
  const symbols = new Set(rows.map((row) => row.symbol).filter(Boolean))
  const pcts = rows.map((row) => row.auctionPct).filter((value): value is number => value != null)
  const unmatched = rows.map((row) => row.auctionUnmatched).filter((value): value is number => value != null)
  return {
    found: summary?.found ?? false,
    totalAmount: summary?.totalAmount ?? 0,
    limitUpCount: summary?.limitUpCount ?? 0,
    sampleCount: symbols.size,
    avgPct: pcts.length ? pcts.reduce((acc, value) => acc + value, 0) / pcts.length : null,
    unmatchedShares: unmatched.length ? unmatched.reduce((acc, value) => acc + value, 0) : null,
    asOf: summary?.asOf || '',
  }
}

/** 本轮只读检查的上下文；接口失败时必须置为 false，不能沿用 store 的上次成功值。 */
export interface DecisionContext {
  checkedAt: number
  accountBook: string
  accountReady: boolean
  positionsReady: boolean
  signalsReady: boolean
  riskReady: boolean
  riskMode: string
  killActive: boolean
  openHalted: boolean
  haltReason: string
  marketReady: boolean
  marketAsOf: string
  marketStale: boolean
  sourcesReady: boolean
  live: boolean
  positions: { symbol: string; quantity: number | null; available: number | null }[]
}

export interface DecisionAction {
  state: 'review' | 'wait' | 'blocked' | 'executing' | 'held' | 'closed'
  title: string
  timing: string
  invalidation: string
  blocker: string
  caution: string
  canOpenTrade: boolean
}

type ActionPlan = Pick<
  DeskTicket,
  | 'id'
  | 'book'
  | 'strategyVersionId'
  | 'symbol'
  | 'side'
  | 'status'
  | 'entry'
  | 'entryLow'
  | 'entryHigh'
  | 'stop'
  | 'take'
  | 'validUntil'
  | 'holdDaysMax'
  | 'degraded'
  | 'reason'
>

/** 账本身份只能显式映射，未知值不默认使用 SIM。 */
export function signalBook(book: string): 'SIM' | 'LIVE' | '' {
  if (book.toLowerCase() === 'paper' || book.toUpperCase() === 'SIM') return 'SIM'
  if (book.toLowerCase() === 'live') return 'LIVE'
  return ''
}

function symbolKey(symbol: string): string {
  return symbol.toUpperCase().split('.')[0] || ''
}

/**
 * 白话行动层只翻译已有计划和检查结果。没有个股实时条件/实检结果时，永远不写「现在可以买」。
 * 允许打开的是确认准备页；最终价格、股数与风控仍由交易服务重新校验。
 */
export function buildDecisionAction(
  plan: ActionPlan & { dims?: Record<string, unknown>; votes?: DimVote[] },
  context: DecisionContext | null,
  now: number, // P2-10：必填——有效性判定基准一律 serverNow，禁本地钟默认值
): DecisionAction {
  const status = plan.status.toLowerCase()
  const sell = plan.side === 'sell'
  const votes = plan.votes ?? dimVotes(plan.dims ?? {})
  const caution =
    objectionsOf(votes, plan.degraded)[0] ||
    (votes.some((vote) => vote.score == null)
      ? '四维证据未完整返回，不把缺失当成赞成'
      : '评分不是胜率，执行前仍需核对最新条件')
  const deadline = parseTime(plan.validUntil)?.getTime()
  const until = formatDateTime(plan.validUntil)
  const invalidation = sell
    ? `信号过期、可卖数量变化或已有在途卖单时，重新核对退出计划${until ? `；有效至 ${until}` : ''}`
    : `价格超出入场区间、信号过期或风控拒绝时停止本次计划${until ? `；有效至 ${until}` : ''}`
  const action: DecisionAction = {
    state: 'review',
    title: sell ? '退出计划待执行校验' : '买入计划待执行校验',
    timing: sell
      ? `${plan.reason || '已有卖出信号'}；确认可卖数量、最新限价和在途委托后再行动`
      : `最新价格仍在 ${price(plan.entryLow)}–${price(plan.entryHigh)} 时，核对条件后进入确认；超出区间先等待`,
    invalidation,
    blocker: '',
    caution,
    canOpenTrade: false,
  }

  if (status === 'closed')
    return {
      ...action,
      state: 'closed',
      title: '该笔交易已平仓',
      timing: '查看实际成交与后验记录，不重复执行这张计划',
      invalidation: '历史计划仅用于复盘',
    }
  if (status === 'done')
    return {
      ...action,
      state: sell ? 'closed' : 'held',
      title: sell ? '卖出已有成交' : '已开仓，查看持仓退出计划',
      timing: sell
        ? '核对实际成交和剩余持仓；有成交不等于全部卖完'
        : `继续跟踪止损 ${price(plan.stop)}、目标 ${price(plan.take)}${plan.holdDaysMax != null ? `及最长持有 ${plan.holdDaysMax} 个交易日` : ''}；这张买入票不重复下单`,
      invalidation: '以实际持仓、可卖数量和新的退出信号为准',
    }
  if (status === 'executing')
    return {
      ...action,
      state: 'executing',
      title: '订单执行中，先核对成交',
      timing: '在交易面板查看部分成交、撤单与剩余委托，不重复提交',
      invalidation: '订单状态以交易服务及对账结果为准',
    }

  const blockers: string[] = []
  if (status === 'rejected') blockers.push(plan.reason || '该信号已被拒绝')
  else if (status === 'cancelled') blockers.push('该信号已取消')
  else if (status === 'expired' || (deadline != null && now >= deadline))
    blockers.push('该信号已过期，等待重新形成计划')
  else if (status === 'risk_checking') blockers.push('风险检查尚未完成')
  else if (status === 'pending') blockers.push('信号仍待处理，尚未得到批准')
  else if (status !== 'approved') blockers.push('信号状态未知，无法准备执行')
  if (deadline == null) blockers.push('信号有效期缺失或无法解析')
  if (plan.side !== 'buy' && !sell) blockers.push('买卖方向不明确')
  if (!plan.id || plan.id === '0') blockers.push('信号身份缺失')
  if (!plan.strategyVersionId || plan.strategyVersionId === '0') blockers.push('策略版本未返回或尚未归档，不能准备执行')
  const book = signalBook(plan.book)
  if (!book) blockers.push('信号账本不明确')
  if (!(plan.entry != null && plan.entry > 0)) blockers.push('计划限价缺失')
  if (
    !sell &&
    !(
      plan.stop != null &&
      plan.entryLow != null &&
      plan.entry != null &&
      plan.entryHigh != null &&
      plan.take != null &&
      plan.stop > 0 &&
      plan.stop < plan.entryLow &&
      plan.entryLow <= plan.entry &&
      plan.entry <= plan.entryHigh &&
      plan.entryHigh < plan.take
    )
  ) {
    blockers.push('入场、止损和目标价格不完整或顺序不合法')
  }
  if (!context || now - context.checkedAt > 45_000 || now < context.checkedAt)
    blockers.push('账户、行情和风控状态尚未完成本轮检查')
  else {
    if (book && book !== signalBook(context.accountBook)) blockers.push('信号账本与当前账户不一致，请先切换账户')
    if (!context.accountReady || !context.positionsReady)
      blockers.push('账户或持仓读取失败，不能确认可用资金与可卖数量')
    if (!context.signalsReady) blockers.push('信号状态未能重新读取，请刷新后核对')
    if (!context.riskReady) blockers.push('风控状态未返回')
    if (!context.live) blockers.push('实时推送未连接，当前信息可能已变化')
    const marketTime = parseTime(context.marketAsOf)?.getTime()
    if (
      !context.marketReady ||
      context.marketStale ||
      marketTime == null ||
      now - marketTime > 90_000 ||
      marketTime > now + 5_000
    )
      blockers.push('市场快照缺失或已过期，等待最新数据')
    if (!context.sourcesReady) blockers.push('数据源状态未确认或没有可用数据源')
    if (!sell && context.killActive) blockers.unshift('急停已触发，暂停买入')
    if (!sell && context.openHalted)
      blockers.unshift(`开仓已停止${context.haltReason ? `：${context.haltReason}` : ''}`)
    if (!sell && context.riskMode === 'L0') blockers.push('当前处于只看模式，不能买入')
    if (!['L0', 'L1', 'L2', 'L3'].includes(context.riskMode)) blockers.push('运行模式未知')
    if (book === 'LIVE') blockers.push('实盘执行通道尚未验收，本轮仅支持模拟盘计划')
    const holding = context.positions.find((row) => symbolKey(row.symbol) === symbolKey(plan.symbol))
    if (sell && !(holding?.available != null && holding.available > 0))
      blockers.push('当前没有已确认的可卖数量；当日买入可能仍受 T+1 约束')
    if (!sell && holding?.quantity != null && holding.quantity > 0)
      blockers.push('当前已持有该股票，先查看持仓计划，避免重复开仓')
  }
  action.blocker = blockers[0] || ''
  action.canOpenTrade = blockers.length === 0
  if (!action.canOpenTrade) {
    action.state = status === 'pending' || status === 'risk_checking' ? 'wait' : 'blocked'
    action.title = sell ? '退出计划暂不能执行' : '买入计划暂不能执行'
  }
  return action
}
