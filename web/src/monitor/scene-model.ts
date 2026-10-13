import { amount, compactAmount, intText, missLabel, phaseName, ratioPct, signed } from '@/utils/format'
import { buildFunnel, funnelCountText, funnelRate } from './funnel'
import type {
  ChildCard,
  Identifier,
  MonitorInput,
  MonitorSignal,
  NewsRow,
  NodeStatus,
  Pulse,
  SceneEdge,
  SceneGraph,
  SceneNode,
  TerminalModel,
  TerminalRow,
} from './types'

export type { ChildCard, Identifier, MonitorInput, NewsRow, Pulse, SceneGraph, SceneNode, TerminalModel } from './types'

const PLAIN = { rank: 'ambient' as const, face: '', sub: '', tone: 'flat' as const }

const POSTS: { id: string; title: string }[] = [
  { id: 'post:scan', title: '扫描' },
  { id: 'post:model', title: '模型' },
  { id: 'post:news', title: '消息' },
  { id: 'post:capital', title: '资金' },
  { id: 'post:overseas', title: '外围' },
  { id: 'post:risk', title: '风控' },
  { id: 'post:desk', title: '信号' },
]

const LIVE_STATUS = new Set(['ok', 'up', 'running', 'healthy', 'ready'])
const DOWN_STATUS = new Set(['down', 'error', 'unavailable', 'failed', 'closed'])

const CAPITAL_GAP = '主力、龙虎榜、北向、竞价未接入'
const PULSE_TTL = 2400
const PULSE_CAP = 24

export function pushPulse(current: Pulse[], next: Pulse, now: number): Pulse[] {
  const alive = current.filter((item) => now - item.at <= PULSE_TTL)
  const merged = [...alive, next]
  return merged.length <= PULSE_CAP ? merged : merged.slice(merged.length - PULSE_CAP)
}

export function buildScene(input: MonitorInput, now: number): SceneGraph {
  const signals = input.signals?.ok ? input.signals.items : []
  const ruleIds = uniqueIds(signals.flatMap(ruleIdentifiers))
  const factorIds = uniqueIds(signals.flatMap((item) => evidenceIdentifiers(item.evidence, 'factor')))

  const { nodes: sourceNodes, truncated: sourceCut } = buildSources(input)
  const { nodes: serviceNodes, truncated: serviceCut } = buildServices(input)
  const postNodes = POSTS.map((post) => buildPost(post.id, post.title, input, signals, factorIds, ruleIds))
  const book = buildBook(input)
  const tape = buildTape()
  const dimNodes = [...buildDimNodes(input, signals), buildSynth(input, signals)]
  const modelNodes = buildModelNodes(input)
  const miss = buildMiss(input)

  const nodes = [tape, book, ...postNodes, ...dimNodes, ...modelNodes, miss, ...serviceNodes, ...sourceNodes]
    .map((node) => present(node, input, signals))
    .map(hideLegacy)
  const known = new Set(nodes.map((node) => node.id))
  const edges = buildEdges(sourceNodes, known)

  return {
    nodes,
    edges,
    pulses: input.pulses.filter((item) => now - item.at <= PULSE_TTL).slice(-PULSE_CAP),
    news: buildNews(input),
    terminals: buildTerminals(input, signals),
    focusId: '',
    truncated: sourceCut || serviceCut,
    funnel: buildFunnel(input, missLabel),
  }
}

function buildPost(
  id: string,
  title: string,
  input: MonitorInput,
  signals: MonitorSignal[],
  factorIds: Identifier[],
  ruleIds: Identifier[],
): SceneNode {
  const base = { id, ring: 'post' as const, title, inputs: [] as Identifier[], outputs: [] as Identifier[], ...PLAIN }
  if (id === 'post:scan') return { ...base, ...scanPost(input) }
  if (id === 'post:model') return { ...base, ...modelPost(input, signals, factorIds) }
  if (id === 'post:news') return { ...base, ...newsPost(input) }
  if (id === 'post:capital') return { ...base, ...capitalPost(input) }
  if (id === 'post:overseas') return { ...base, ...overseasPost(input) }
  if (id === 'post:risk') return { ...base, ...riskPost(input), outputs: ruleIds }
  return { ...base, ...deskPost(input, signals) }
}

function scanPost(input: MonitorInput): Pick<SceneNode, 'status' | 'detail' | 'outputs'> {
  const limits = input.limits?.ok ? input.limits.rows.slice(0, 8) : []
  const outputs = limits
    .filter((row) => row.symbol)
    .map((row) => ({ kind: 'symbol' as const, value: row.symbol }))
  const sentiment = input.sentiment
  if (!sentiment || !sentiment.ok) return { status: 'gap', detail: '情绪接口无返回', outputs }
  if (sentiment.stale) return { status: 'gap', detail: `数据已过期，截至 ${sentiment.asOf || '未知时刻'}`, outputs }
  const detail = `${phaseText(sentiment.phase, '阶段未知')}，涨 ${intText(sentiment.up)} 跌 ${intText(sentiment.down)}`
  return { status: sentiment.phase ? 'live' : 'idle', detail, outputs }
}

function modelPost(
  input: MonitorInput,
  signals: MonitorSignal[],
  factorIds: Identifier[],
): Pick<SceneNode, 'status' | 'detail' | 'inputs' | 'outputs'> {
  const brainDown = !input.brainStats || !input.brainStats.ok
  const signalDown = !input.signals || !input.signals.ok
  if (signalDown) {
    const detail = brainDown ? '信号接口无返回；模型统计无返回' : '信号接口无返回'
    return { status: 'gap', detail, inputs: factorIds, outputs: [] }
  }
  const latest = signals[0]
  if (!latest) {
    const detail = brainDown ? '漏斗没有产出；模型统计无返回' : '漏斗没有产出'
    return { status: 'idle', detail, inputs: [], outputs: [] }
  }
  const score = latest.score == null ? '—' : String(latest.score)
  let detail = `最新信号 ${latest.symbol} ${score}`
  if (latest.degraded) detail = `规则分 ${detail}`
  if (brainDown) detail = `${detail}；模型统计无返回`
  const outputs: Identifier[] = []
  if (latest.id) outputs.push({ kind: 'signal', value: latest.id })
  if (latest.symbol) outputs.push({ kind: 'symbol', value: latest.symbol })
  return {
    status: latest.highValue ? 'attention' : 'live',
    detail,
    inputs: factorIds,
    outputs,
  }
}

function newsPost(input: MonitorInput): Pick<SceneNode, 'status' | 'detail' | 'inputs'> {
  const inputs = newsInputs(input)
  const briefing = input.briefing
  if (briefing?.ok && briefing.stale) {
    return { status: 'gap', detail: `数据已过期，截至 ${briefing.generatedAt || '未知时刻'}`, inputs }
  }
  if (briefing?.ok) {
    return { status: 'live', detail: briefing.generatedAt || '晨报已生成', inputs }
  }
  const timelineOk = !!input.focusSymbol && !!input.timeline?.ok && input.timeline.items.length > 0
  if (timelineOk) return { status: 'live', detail: `焦点 ${input.focusSymbol}`, inputs }
  return { status: 'gap', detail: '晨报未生成', inputs }
}

function newsInputs(input: MonitorInput): Identifier[] {
  if (!input.focusSymbol || !input.timeline?.ok) return []
  return input.timeline.items.slice(0, 8).map((item) => ({
    kind: 'N' as const,
    value: item.id || item.title,
  }))
}

function capitalPost(input: MonitorInput): Pick<SceneNode, 'status' | 'detail'> {
  if (!input.sectors || !input.sectors.ok) return { status: 'gap', detail: `板块接口无返回；${CAPITAL_GAP}` }
  const named = input.sectors.rows.find((row) => row.leader || row.name)
  const label = named?.name || named?.leader || ''
  return { status: 'idle', detail: label ? `${label}；${CAPITAL_GAP}` : CAPITAL_GAP }
}

function overseasPost(input: MonitorInput): Pick<SceneNode, 'status' | 'detail'> {
  if (!input.sectors || !input.sectors.ok) return { status: 'gap', detail: '板块接口无返回' }
  const hit = input.sectors.rows.find((row) => row.overseas != null)
  if (!hit) return { status: 'gap', detail: '外围未调用' }
  return { status: 'live', detail: hit.name || hit.leader || '外围有数' }
}

function riskPost(input: MonitorInput): Pick<SceneNode, 'status' | 'detail'> {
  const risk = input.risk
  if (!risk || !risk.ok) return { status: 'gap', detail: '风控状态无返回' }
  const head = [risk.mode, risk.marketBreaker].filter(Boolean).join(' ')
  if (risk.killActive) {
    const reason = risk.killReason || '急停已触发'
    return { status: 'down', detail: head ? `${head} ${reason}` : reason }
  }
  return { status: 'live', detail: head || '风控在线' }
}

function deskPost(input: MonitorInput, signals: MonitorSignal[]): Pick<SceneNode, 'status' | 'detail' | 'outputs'> {
  if (!input.signals || !input.signals.ok) return { status: 'gap', detail: '信号接口无返回', outputs: [] }
  const pending = signals.filter((item) => item.status === 'approved' || item.status === 'risk_checking')
  if (pending.length === 0) return { status: 'live', detail: '没有待确认指令', outputs: [] }
  return {
    status: 'attention',
    detail: `待确认 ${pending.length} 笔`,
    outputs: [{ kind: 'signal', value: String(pending.length) }],
  }
}

function buildBook(input: MonitorInput): SceneNode {
  const base = { id: 'book:center', ring: 'book' as const, title: '账户', inputs: [] as Identifier[], outputs: [] as Identifier[], ...PLAIN }
  const account = input.account
  if (!account || !account.ok) return { ...base, status: 'gap', detail: '账户接口无返回' }
  const lines = [`权益 ${amount(account.equity)}`, `当日 ${signed(account.dayPnl)}`, `现金 ${amount(account.cash)}`]
  if (input.risk?.ok && input.risk.killActive) {
    lines.push(input.risk.killReason || '急停已触发')
    return { ...base, status: 'down', detail: lines.join('\n') }
  }
  return { ...base, status: 'live', detail: lines.join('\n') }
}

function buildSources(input: MonitorInput): { nodes: SceneNode[]; truncated: boolean } {
  const listed =
    input.sources?.ok && input.sources.items.length > 0
      ? capNodes(input.sources.items.map(sourceFromItem), 12)
      : input.ops?.ok && input.ops.sources.length > 0
        ? capNodes(
            input.ops.sources.map((item) => namedNode(`source:${slug(item.name)}`, 'source', item.name, item.status, item.detail)),
            12,
          )
        : {
            nodes: [
              {
                id: 'source:none',
                ring: 'source' as const,
                title: '数据源',
                status: 'gap' as const,
                detail: '数据源未返回',
                inputs: [],
                outputs: [],
                ...PLAIN,
              },
            ],
            truncated: false,
          }
  const quality = qualityNode(input)
  if (!quality) return listed
  return { nodes: [...listed.nodes, quality].slice(0, 12), truncated: listed.truncated || listed.nodes.length + 1 > 12 }
}

function sourceFromItem(item: { id: string; name: string; status: string; detail: string }): SceneNode {
  const key = slug(item.id || item.name)
  return namedNode(`source:${key}`, 'source', item.name || item.id, item.status, item.detail)
}

const LOUD_PHASE = new Set(['HOT', 'FADE', 'ICE'])
const CLOSED_ORDER = new Set(['filled', 'cancelled', 'canceled', 'rejected', 'expired'])

/** 后端 phase 是英文枚举（HOT/WARM/…），牌面统一走 format.phaseName 的中文表 */
function phaseText(phase: string, fallback: string): string {
  return phase ? phaseName(phase) : fallback
}

const HIDDEN = new Set(['book:tape', 'post:model', 'post:news', 'post:capital', 'post:overseas'])

function hideLegacy(node: SceneNode): SceneNode {
  if (!HIDDEN.has(node.id)) return node
  return { ...node, rank: 'ambient', face: '', sub: '' }
}

function present(node: SceneNode, input: MonitorInput, signals: MonitorSignal[]): SceneNode {
  if (node.id.startsWith('dim:') || node.id.startsWith('model:') || node.id === 'post:miss') return node
  if (node.id === 'book:tape') return { ...node, ...tapeFace(input) }
  if (node.id === 'book:center') return { ...node, ...accountFace(input) }
  if (node.ring === 'post') return { ...node, ...postFace(node.id, input, signals) }
  if (node.id === 'source:quality') return { ...node, rank: 'ambient', face: '', sub: '' }
  if (node.ring === 'source') return { ...node, rank: 'brief', face: sourceWord(node.status), sub: '', tone: 'flat' }
  if (node.status === 'down') return { ...node, rank: 'brief', face: node.title, sub: '', tone: 'flat' }
  return { ...node, rank: 'ambient', face: '', sub: '', tone: 'flat' }
}

function sourceWord(status: SceneNode['status']): string {
  if (status === 'live') return '在用'
  if (status === 'down') return '中断'
  if (status === 'gap') return '未返回'
  return '空闲'
}

function tapeFace(input: MonitorInput): Pick<SceneNode, 'status' | 'detail' | 'rank' | 'face' | 'sub' | 'tone'> {
  const sentiment = input.sentiment
  if (!sentiment?.ok) {
    return { status: 'gap', detail: '情绪接口无返回', rank: 'focus', face: '情绪未返回', sub: '', tone: 'flat' }
  }
  const stale = sentiment.stale
  return {
    status: stale ? 'gap' : 'live',
    detail: stale ? `数据已过期，截至 ${sentiment.asOf || '未知时刻'}` : `${phaseText(sentiment.phase, '阶段未知')}，涨 ${intText(sentiment.up)} 跌 ${intText(sentiment.down)}`,
    rank: 'focus',
    face: stale ? '过期' : phaseText(sentiment.phase, '阶段未知'),
    sub: `涨 ${intText(sentiment.up)} 跌 ${intText(sentiment.down)}`,
    tone: 'flat',
  }
}

function accountFace(input: MonitorInput): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  const account = input.account
  if (!account?.ok) return { rank: 'focus', face: '账户未返回', sub: '', tone: 'flat' }
  return {
    rank: 'focus',
    face: signed(account.dayPnl),
    sub: accountSub(input),
    tone: numberTone(account.dayPnl),
  }
}

function postFace(id: string, input: MonitorInput, signals: MonitorSignal[]): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  if (id === 'post:scan') return scanFace(input)
  if (id === 'post:model') return modelFace(input)
  if (id === 'post:news') return newsFace(input)
  if (id === 'post:capital') return capitalFace(input)
  if (id === 'post:overseas') return overseasFace(input)
  if (id === 'post:risk') return riskFace(input)
  return deskFace(input, signals)
}

function scanFace(input: MonitorInput): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  const count = input.limits?.ok ? String(input.limits.rows.length) : ''
  if (!input.sentiment?.ok) return { rank: 'brief', face: '未返回', sub: count, tone: 'flat' }
  const loud = input.sentiment.stale || LOUD_PHASE.has(input.sentiment.phase)
  return {
    rank: loud ? 'focus' : 'brief',
    face: input.sentiment.stale ? '过期' : phaseText(input.sentiment.phase, '未返回'),
    sub: count ? `涨停 ${count}` : '',
    tone: 'flat',
  }
}

function modelFace(input: MonitorInput): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  const usage = input.brainP1?.ok ? input.brainP1.usage : null
  const tokenSub = usage ? `令牌 ${compactAmount(usage.total.totalTokens)}` : '令牌未接入'
  if (!input.brainStats?.ok) return { rank: 'brief', face: '统计未返回', sub: tokenSub, tone: 'flat' }
  const open = input.brainStats.tiers.find((tier) => tier.circuitOpen)
  if (open) return { rank: 'focus', face: open.tier || open.model || '熔断', sub: '熔断', tone: 'flat' }
  const counts = input.brainStats.tiers.map((tier) => tier.calls).filter((value): value is number => value != null)
  if (counts.length === 0) return { rank: 'brief', face: '统计未返回', sub: tokenSub, tone: 'flat' }
  const calls = counts.reduce((sum, value) => sum + value, 0)
  return { rank: 'brief', face: `${calls} 次`, sub: tokenSub, tone: 'flat' }
}

function accountSub(input: MonitorInput): string {
  const account = input.account
  if (!account?.ok) return ''
  const open = openOrderCount(input)
  const book = `权益 ${amount(account.equity)} · 持仓 ${account.positions.length}`
  return open > 0 ? `${book} · 委托 ${open}` : book
}

function openOrderCount(input: MonitorInput): number {
  if (!input.orders?.ok) return 0
  return input.orders.items.filter((item) => !CLOSED_ORDER.has(item.status.toLowerCase())).length
}

function newsFace(input: MonitorInput): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  if (input.briefing?.ok && input.briefing.stale) return { rank: 'focus', face: '过期', sub: '晨报', tone: 'flat' }
  if (input.briefing?.ok) return { rank: 'brief', face: String(input.briefing.items.length), sub: '晨报', tone: 'flat' }
  if (input.hotWords?.ok) return { rank: 'brief', face: String(input.hotWords.items.length), sub: '热词', tone: 'flat' }
  return { rank: 'brief', face: '晨报未生成', sub: '', tone: 'flat' }
}

function capitalFace(input: MonitorInput): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  if (!input.sectors?.ok) return { rank: 'brief', face: '板块未返回', sub: '', tone: 'flat' }
  const named = input.sectors.rows.find((row) => row.name)
  return { rank: 'brief', face: named?.name || '资金未接入', sub: '资金未接入', tone: 'flat' }
}

function overseasFace(input: MonitorInput): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  if (!input.sectors?.ok) return { rank: 'brief', face: '未调用', sub: '', tone: 'flat' }
  const hit = input.sectors.rows.find((row) => row.overseas != null)
  if (!hit) return { rank: 'brief', face: '未调用', sub: '', tone: 'flat' }
  return { rank: 'brief', face: hit.name || '外围', sub: '', tone: 'flat' }
}

function riskFace(input: MonitorInput): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  const risk = input.risk
  if (!risk?.ok) return { rank: 'brief', face: '风控未返回', sub: '', tone: 'flat' }
  if (risk.killActive) return { rank: 'focus', face: '急停', sub: clip(risk.killReason, 12), tone: 'flat' }
  if (risk.marketBreaker) return { rank: 'focus', face: '熔断', sub: clip(risk.marketBreaker, 12), tone: 'flat' }
  return { rank: 'brief', face: '守着', sub: '', tone: 'flat' }
}

function deskFace(input: MonitorInput, signals: MonitorSignal[]): Pick<SceneNode, 'rank' | 'face' | 'sub' | 'tone'> {
  if (!input.signals?.ok) return { rank: 'brief', face: '信号未返回', sub: '', tone: 'flat' }
  const pending = signals.filter((item) => item.status === 'approved' || item.status === 'risk_checking').length
  if (pending > 0) return { rank: 'focus', face: `${pending} 笔`, sub: '', tone: 'flat' }
  return { rank: 'brief', face: '空闲', sub: '', tone: 'flat' }
}

function clip(value: string, max: number): string {
  const text = value.trim()
  if (text.length <= max) return text
  return text.slice(0, max)
}

export function buildChildren(input: MonitorInput, id: string): ChildCard[] {
  if (!id) return []
  if (id === 'book:tape') return capCards(tapeChildren(input))
  if (id === 'book:center') return capCards(accountChildren(input))
  if (id === 'post:scan') return expertCards(input, id)
  if (id === 'post:desk') return capCards(signalChildren(input, true))
  if (id === 'post:miss') return capCards(missChildren(input))
  if (id.startsWith('model:')) return modelExplain(input, id)
  if (id === 'post:news') return capCards(newsChildren(input))
  if (id === 'post:model') return capCards(modelChildren(input))
  if (id === 'post:risk') return capCards(riskChildren(input))
  if (id === 'post:capital' || id === 'post:overseas') return capCards(sectorChildren(input))
  if (id.startsWith('symbol:')) return capCards(symbolChildren(input, id.slice('symbol:'.length)))
  if (id === 'dim:synth' || DIM_LANES.some((dim) => dim.id === id)) return expertCards(input, id)
  if (id === 'book:orders') return capCards(orderChildren(input))
  if (id === 'book:fills') return capCards(fillChildren(input))
  if (id.startsWith('tier:')) return capCards(tierChildren(input, id.slice('tier:'.length)))
  if (id === 'task:latest' || id.startsWith('task:')) return capCards(taskChildren(input))
  if (id === 'admit:board') return capCards(admissionChildren(input))
  if (id === 'shadow:stats') return capCards(shadowChildren(input))
  if (id === 'notice:list') return capCards(noticeChildren(input))
  if (id === 'audit:list') return capCards(auditChildren(input))
  if (id.startsWith('brief:') || id.startsWith('hot:') || id.startsWith('intel:')) return capCards(articleChildren(input, id))
  if (id === 'source:quality') return capCards(qualityChildren(input))
  const node = buildScene(input, input.now).nodes.find((item) => item.id === id)
  if (!node) return []
  return [{ id: `${id}:why`, title: node.title, face: node.status, sub: node.detail, tone: 'flat' }]
}

function tapeChildren(input: MonitorInput): ChildCard[] {
  const sentiment = input.sentiment?.ok ? input.sentiment : null
  const summary: ChildCard[] = sentiment
    ? [
        { id: 'tape:broken', title: '破板率', face: sentiment.brokenRate == null ? '未返回' : ratioPct(sentiment.brokenRate), sub: '', tone: 'flat' },
        { id: 'tape:height', title: '最高板', face: sentiment.maxHeight == null ? '未返回' : String(sentiment.maxHeight), sub: '', tone: 'flat' },
        { id: 'tape:amount', title: '成交额', face: amount(sentiment.amount ?? null), sub: sentiment.stale ? '过期' : '', tone: 'flat' },
      ]
    : []
  const limits = input.limits?.ok
    ? input.limits.rows.map((row) => ({
        id: `symbol:${row.symbol}`,
        title: row.symbol || '涨停',
        face: row.status || '涨停',
        sub: row.consecutive == null ? '' : `${row.consecutive} 连板`,
        tone: 'flat' as const,
      }))
    : []
  return [...summary, ...limits, ...sectorChildren(input)]
}

function accountChildren(input: MonitorInput): ChildCard[] {
  if (!input.account?.ok || input.account.positionsOk === false) {
    return [{ id: 'pos:missing', title: '持仓', face: '持仓未返回', sub: '', tone: 'flat' }]
  }
  const cards: ChildCard[] =
    input.account.positions.length === 0
      ? [{ id: 'pos:empty', title: '持仓', face: '没有持仓', sub: input.account.book || '', tone: 'flat' }]
      : input.account.positions.map((row) => ({
          id: `symbol:${row.symbol}`,
          title: row.symbol || '持仓',
          face: amount(row.lastPrice),
          sub: `成本 ${amount(row.avgCost)} · ${intText(row.quantity)}`,
          tone: priceTone(row.lastPrice, row.avgCost),
        }))
  if (input.orders) {
    cards.push({
      id: 'book:orders',
      title: '委托',
      face: input.orders.ok ? String(openOrderCount(input)) : '未返回',
      sub: input.orders.ok ? `共 ${input.orders.items.length}` : '',
      tone: 'flat',
    })
  }
  if (input.fills) {
    cards.push({
      id: 'book:fills',
      title: '成交',
      face: input.fills.ok ? String(input.fills.items.length) : '未返回',
      sub: '',
      tone: 'flat',
    })
  }
  return cards
}

function signalChildren(input: MonitorInput, desk: boolean): ChildCard[] {
  if (!input.signals?.ok) return [{ id: 'sig:missing', title: '信号', face: '信号未返回', sub: '', tone: 'flat' }]
  const pending = input.signals.items.filter((item) => item.status === 'approved' || item.status === 'risk_checking')
  const rows = (desk ? pending : input.signals.items).map((item) => ({
    id: `symbol:${item.symbol}`,
    title: item.symbol,
    face: item.degraded ? '规则分' : item.score == null ? '—' : String(item.score),
    sub: item.status,
    tone: 'flat' as const,
  }))
  if (!desk && input.candidates?.ok) {
    for (const item of input.candidates.items.filter((row) => row.missReason)) {
      rows.push({
        id: `symbol:${item.symbol}`,
        title: item.symbol || item.name || '漏选',
        face: item.missReason,
        sub: item.strategy,
        tone: 'flat',
      })
    }
  } else if (!desk && input.candidates && !input.candidates.ok) {
    rows.push({ id: 'cand:missing', title: '漏选', face: '候选未返回', sub: '', tone: 'flat' })
  }
  if (desk) rows.push({ id: 'action:trade', title: '确认', face: '回到交易台', sub: '', tone: 'flat' })
  return rows
}

function newsChildren(input: MonitorInput): ChildCard[] {
  const cards = buildNews(input).map((row) => ({
    id: row.id,
    title: row.title || '消息',
    face: row.source || '消息',
    sub: row.time,
    tone: 'flat' as const,
  }))
  if (input.notices) {
    cards.push({
      id: 'notice:list',
      title: '通知',
      face: input.notices.ok ? String(input.notices.items.length) : '未返回',
      sub: '',
      tone: 'flat',
    })
  }
  if (input.audit) {
    cards.push({
      id: 'audit:list',
      title: '审计',
      face: input.audit.ok ? String(input.audit.items.length) : '未返回',
      sub: '',
      tone: 'flat',
    })
  }
  return cards
}

function modelChildren(input: MonitorInput): ChildCard[] {
  const tiers = input.brainStats?.ok ? input.brainStats.tiers : []
  const cards = tiers.map((tier) => ({
    id: `tier:${tier.tier || tier.model}`,
    title: tier.tier || '模型',
    face: tier.circuitOpen ? '熔断' : `${tier.calls ?? 0} 次`,
    sub: tier.model || '',
    tone: 'flat' as const,
  }))
  /* S7：usage 通了就出真令牌合计，不再用「令牌未接入」占位 */
  const usage = input.brainP1?.ok ? input.brainP1.usage : null
  if (usage) {
    cards.push({
      id: 'token:total',
      title: '令牌',
      face: compactAmount(usage.total.totalTokens),
      sub: `入 ${compactAmount(usage.total.promptTokens)} · 出 ${compactAmount(usage.total.completionTokens)}`,
      tone: 'flat' as const,
    })
  } else {
    cards.push({ id: 'token:gap', title: '令牌', face: '令牌未接入', sub: '', tone: 'flat' })
  }
  if (input.tasks) {
    const task = input.tasks.ok ? input.tasks.items[0] : undefined
    cards.push({
      id: task ? `task:${task.id}` : 'task:latest',
      title: '回测',
      face: !input.tasks.ok ? '未返回' : task?.status || '没有任务',
      sub: '回测曲线，不是模拟盘权益',
      tone: 'flat',
    })
  }
  if (input.admission) {
    cards.push({
      id: 'admit:board',
      title: '准入',
      face: !input.admission.ok ? '未返回' : input.admission.ready ? '通过' : '未通过',
      sub: input.admission.ok ? `${intText(input.admission.tradingDays)} / ${intText(input.admission.requiredDays)}` : '',
      tone: 'flat',
    })
  }
  if (input.shadow) {
    cards.push({
      id: 'shadow:stats',
      title: '影子',
      face: input.shadow.ok ? String(input.shadow.items.length) : '未返回',
      sub: '',
      tone: 'flat',
    })
  }
  return cards
}

function riskChildren(input: MonitorInput): ChildCard[] {
  if (!input.risk?.ok) return [{ id: 'risk:missing', title: '风控', face: '风控未返回', sub: '', tone: 'flat' }]
  const risk = input.risk
  const cards: ChildCard[] = [
    { id: 'risk:mode', title: '模式', face: risk.mode || '—', sub: '', tone: 'flat' },
    { id: 'risk:kill', title: '急停', face: risk.killActive ? '急停' : '守着', sub: risk.killReason, tone: 'flat' },
    { id: 'risk:buys', title: '买单', face: intText(risk.buysToday), sub: '', tone: 'flat' },
    { id: 'risk:break', title: '熔断', face: risk.marketBreaker || '无', sub: '', tone: 'flat' },
  ]
  const rules = uniqueIds((input.signals?.ok ? input.signals.items : []).flatMap(ruleIdentifiers))
  for (const rule of rules) {
    cards.push({ id: `rule:${rule.value}`, title: rule.value, face: '命中', sub: '', tone: 'flat' })
  }
  if (input.riskReport) {
    cards.push({
      id: 'risk:report',
      title: '日报',
      face: input.riskReport.ok ? '摘要' : '未返回',
      sub: input.riskReport.summary,
      tone: 'flat',
    })
  }
  return cards
}

function sectorChildren(input: MonitorInput): ChildCard[] {
  if (!input.sectors?.ok) return [{ id: 'sector:missing', title: '板块', face: '板块未返回', sub: '', tone: 'flat' }]
  return input.sectors.rows.map((row) => ({
    id: row.leader ? `symbol:${row.leader}` : `sector:${row.name}`,
    title: row.name || '板块',
    face: row.avgPct == null ? '—' : `${row.avgPct.toFixed(2)}%`,
    sub: row.leader,
    tone: numberTone(row.avgPct),
  }))
}

function symbolChildren(input: MonitorInput, symbol: string): ChildCard[] {
  const signal = input.signals?.ok ? input.signals.items.find((item) => item.symbol === symbol) : undefined
  const dims = signal ? radarRows(signal.dims) : RADAR.map((dim) => ({ label: dim.label, value: '未调用', tone: 'flat' as const }))
  const cards: ChildCard[] = dims.map((row) => ({
    id: `dim:${symbol}:${row.label}`,
    title: row.label,
    face: row.value,
    sub: symbol,
    tone: 'flat',
  }))
  const tape = input.timeline?.ok && input.timeline.symbol === symbol ? input.timeline.items : []
  for (const item of tape) {
    cards.push({ id: item.id || item.title, title: item.title || '情报', face: item.source || '情报', sub: item.time, tone: 'flat' })
  }
  if (tape.length === 0) cards.push({ id: `intel:${symbol}`, title: '情报', face: '时间线未返回', sub: symbol, tone: 'flat' })
  const factors = input.factors
  if (!factors || factors.symbol !== symbol) {
    cards.push({ id: `capital:${symbol}`, title: '资金因子', face: '未拉取', sub: symbol, tone: 'flat' })
  } else if (!factors.ok) {
    cards.push({ id: `capital:${symbol}`, title: '资金因子', face: '未返回', sub: symbol, tone: 'flat' })
  } else if (factors.items.length === 0) {
    cards.push({ id: `capital:${symbol}`, title: '资金因子', face: '未接入', sub: symbol, tone: 'flat' })
  } else {
    for (const item of factors.items) {
      cards.push({
        id: `capital:${symbol}:${item.name}`,
        title: item.name,
        face: item.value == null ? '—' : String(item.value),
        sub: '资金',
        tone: 'flat',
      })
    }
  }
  return cards
}

function orderChildren(input: MonitorInput): ChildCard[] {
  if (!input.orders?.ok) return [{ id: 'ord:missing', title: '委托', face: '委托未返回', sub: '', tone: 'flat' }]
  if (input.orders.items.length === 0) return [{ id: 'ord:empty', title: '委托', face: '没有委托', sub: '', tone: 'flat' }]
  return input.orders.items.map((item) => ({
    id: `order:${item.id || item.symbol}`,
    title: item.symbol || '委托',
    face: item.status || '—',
    sub: `${item.side} ${amount(item.price)} × ${intText(item.volume)} ${item.reason}`.trim(),
    tone: 'flat' as const,
  }))
}

function fillChildren(input: MonitorInput): ChildCard[] {
  if (!input.fills?.ok) return [{ id: 'fill:missing', title: '成交', face: '成交未返回', sub: '', tone: 'flat' }]
  if (input.fills.items.length === 0) return [{ id: 'fill:empty', title: '成交', face: '没有成交', sub: '', tone: 'flat' }]
  return input.fills.items.map((item) => ({
    id: `fill:${item.id || item.symbol}`,
    title: item.symbol || '成交',
    face: amount(item.price),
    sub: `${item.side} ${intText(item.qty)} ${item.time}`.trim(),
    tone: 'flat' as const,
  }))
}

function tierChildren(input: MonitorInput, key: string): ChildCard[] {
  const tier = input.brainStats?.ok ? input.brainStats.tiers.find((item) => (item.tier || item.model) === key) : undefined
  if (!tier) return [{ id: 'tier:missing', title: key || '模型', face: '统计未返回', sub: '令牌未接入', tone: 'flat' }]
  /* S7：usage 按档给出真令牌数；接口没通仍写「令牌未接入」 */
  const usageTier = (input.brainP1?.ok ? input.brainP1.usage?.tiers : undefined)?.find((item) => item.tier === tier.tier)
  return [
    { id: `tier:${key}:calls`, title: tier.model || key, face: tier.calls == null ? '未返回' : `${tier.calls} 次`, sub: tier.circuitOpen ? '熔断' : '', tone: 'flat' },
    { id: `tier:${key}:fail`, title: '失败', face: intText(tier.failures), sub: '', tone: 'flat' },
    { id: `tier:${key}:p50`, title: 'P50', face: tier.p50Ms == null ? '未返回' : `${tier.p50Ms} ms`, sub: '', tone: 'flat' },
    { id: `tier:${key}:p95`, title: 'P95', face: tier.p95Ms == null ? '未返回' : `${tier.p95Ms} ms`, sub: '', tone: 'flat' },
    { id: `tier:${key}:fly`, title: '在途', face: intText(tier.inFlight), sub: '', tone: 'flat' },
    { id: `tier:${key}:queue`, title: '排队', face: intText(tier.queued), sub: '', tone: 'flat' },
    { id: `tier:${key}:kv`, title: 'KV', face: tier.kvCacheUsage == null ? '未返回' : String(tier.kvCacheUsage), sub: '', tone: 'flat' },
    {
      id: `tier:${key}:token`,
      title: '令牌',
      face: usageTier ? compactAmount(usageTier.totalTokens) : '令牌未接入',
      sub: usageTier ? `入 ${compactAmount(usageTier.promptTokens)} · 出 ${compactAmount(usageTier.completionTokens)}` : '',
      tone: 'flat',
    },
  ]
}

function taskChildren(input: MonitorInput): ChildCard[] {
  if (!input.tasks?.ok) return [{ id: 'task:missing', title: '回测', face: '任务未返回', sub: '回测曲线，不是模拟盘权益', tone: 'flat' }]
  const task = input.tasks.items[0]
  if (!task) return [{ id: 'task:empty', title: '回测', face: '没有任务', sub: '回测曲线，不是模拟盘权益', tone: 'flat' }]
  return [
    { id: `task:${task.id}:status`, title: task.name || '回测', face: task.status || '—', sub: task.message, tone: 'flat' },
    { id: `task:${task.id}:curve`, title: '曲线', face: '回测曲线', sub: equityCaption(input), tone: 'flat' },
  ]
}

function admissionChildren(input: MonitorInput): ChildCard[] {
  if (!input.admission?.ok) return [{ id: 'admit:missing', title: '准入', face: '未返回', sub: '', tone: 'flat' }]
  return input.admission.items.map((item, index) => ({
    id: `admit:${index}`,
    title: item.label || '准入',
    face: item.status || '—',
    sub: item.detail,
    tone: 'flat' as const,
  }))
}

function shadowChildren(input: MonitorInput): ChildCard[] {
  if (!input.shadow?.ok) return [{ id: 'shadow:missing', title: '影子', face: '未返回', sub: '', tone: 'flat' }]
  if (input.shadow.items.length === 0) return [{ id: 'shadow:empty', title: '影子', face: '没有统计', sub: '', tone: 'flat' }]
  return input.shadow.items.map((item) => ({
    id: `shadow:${item.name}`,
    title: item.name || '影子',
    face: item.live == null ? '—' : String(item.live),
    sub: item.shadow == null ? '' : `影子 ${ratioPct(item.shadow)}`,
    tone: 'flat' as const,
  }))
}

function noticeChildren(input: MonitorInput): ChildCard[] {
  if (!input.notices?.ok) return [{ id: 'notice:missing', title: '通知', face: '未返回', sub: '', tone: 'flat' }]
  if (input.notices.items.length === 0) return [{ id: 'notice:empty', title: '通知', face: '没有通知', sub: '', tone: 'flat' }]
  return input.notices.items.map((item) => ({
    id: item.id || item.title,
    title: item.title || '通知',
    face: item.level || '通知',
    sub: item.time,
    tone: 'flat' as const,
  }))
}

function auditChildren(input: MonitorInput): ChildCard[] {
  if (!input.audit?.ok) return [{ id: 'audit:missing', title: '审计', face: '未返回', sub: '', tone: 'flat' }]
  if (input.audit.items.length === 0) return [{ id: 'audit:empty', title: '审计', face: '没有记录', sub: '', tone: 'flat' }]
  return input.audit.items.map((item) => ({
    id: item.id || item.action,
    title: item.action || '审计',
    face: item.target || '—',
    sub: item.time,
    tone: 'flat' as const,
  }))
}

function articleChildren(input: MonitorInput, id: string): ChildCard[] {
  const briefing = input.briefing?.ok ? input.briefing.items.find((_, index) => id.startsWith(`brief:${index}:`)) : undefined
  const row = buildNews(input).find((item) => item.id === id)
  const cards: ChildCard[] = [
    {
      id: `${id}:body`,
      title: row?.title || briefing?.title || '消息',
      face: row?.source || briefing?.kind || '消息',
      sub: briefing?.body || row?.time || '',
      tone: 'flat',
    },
  ]
  const symbol = briefing?.symbol || row?.symbol || ''
  if (symbol) cards.push({ id: `symbol:${symbol}`, title: symbol, face: '个股', sub: '', tone: 'flat' })
  return cards
}

function expertCards(input: MonitorInput, id: string): ChildCard[] {
  if (id === 'post:scan') {
    const funnel = buildFunnel(input, missLabel)
    /* 六层数字卡（与画布光带同源，DOM 侧等价物），加输入与正文共 8 张 */
    const layers = funnel.layers.map((layer, index) => {
      const rate = funnelRate(layer.count, funnel.layers[index - 1]?.count ?? null)
      const leak = funnel.leaks.find((item) => item.at === index)
      const bits = [rate ? `转化 ${rate}` : '', leak ? `侧漏 ${leak.count} · ${leak.label}` : ''].filter(Boolean)
      return {
        id: `scan:layer:${layer.key}`,
        title: layer.name,
        face: funnelCountText(layer.count),
        sub: bits.join(' · '),
        tone: 'flat' as const,
      }
    })
    const phase = input.sentiment?.ok ? phaseText(input.sentiment.phase, '未返回') : '未返回'
    return [
      { id: 'scan:in', title: '输入', face: phase, sub: '', tone: 'flat' },
      ...layers,
      { id: 'scan:body', title: '正文', face: '研判正文未接入', sub: funnel.note, tone: 'flat' },
    ]
  }
  const dim = id === 'dim:synth' ? { title: '综合', keys: [] as string[] } : DIM_LANES.find((item) => item.id === id)
  const title = dim?.title || '专家'
  const signals = input.signals?.ok ? input.signals.items : []
  const scored = dim && dim.keys.length > 0 ? signals.map((item) => readDim(item.dims, dim.keys)).filter((value): value is string => value != null) : []
  const score = id === 'dim:synth' ? signals.find((item) => item.score != null)?.score : scored[0]
  const evidence = signals.flatMap((item) => item.evidence)
  const factors = evidence.filter((item) => item.startsWith('F:')).length
  const news = evidence.filter((item) => item.startsWith('N:')).length
  const degraded = signals.some((item) => item.degraded)
  return [
    { id: `${id}:in`, title: '输入', face: signals.length === 0 ? '这次没有研判' : `F:${factors} N:${news}`, sub: '', tone: 'flat' },
    { id: `${id}:out`, title: '输出', face: score == null ? '未调用' : degraded ? '规则分' : String(score), sub: title, tone: 'flat' },
    { id: `${id}:act`, title: '动作', face: '只出分', sub: '不下单', tone: 'flat' },
    { id: `${id}:body`, title: '正文', face: '研判正文未接入', sub: '', tone: 'flat' },
  ]
}

function missChildren(input: MonitorInput): ChildCard[] {
  if (!input.candidates?.ok) return [{ id: 'miss:missing', title: '漏选', face: '候选未返回', sub: '', tone: 'flat' }]
  const rows = input.candidates.items.filter((item) => item.missReason)
  if (rows.length === 0) return [{ id: 'miss:empty', title: '漏选', face: '没有', sub: '', tone: 'flat' }]
  return rows.map((item) => ({
    id: `symbol:${item.symbol}`,
    title: item.symbol || item.name || '漏选',
    face: item.missReason,
    sub: item.strategy,
    tone: 'flat' as const,
  }))
}

function modelExplain(input: MonitorInput, id: string): ChildCard[] {
  if (id === 'model:token') {
    return [{ id: 'token:gap', title: '花费', face: '未接入', sub: '日志里有每次令牌，统计接口没有合计，不能写成金额', tone: 'flat' }]
  }
  const tierKey = id === 'model:small' ? 'small' : 'large'
  const tiers = input.brainStats?.ok ? input.brainStats.tiers : []
  const tier = tiers.find((item) => item.tier === tierKey) ?? (tierKey === 'large' ? tiers[0] : tiers[1])
  const route = id === 'model:small' ? '技术、资金' : '消息、外围、综合'
  const api = !input.brainStats?.ok ? '统计未返回' : !tier || tier.configured === false ? '接口未配置' : tier.circuitOpen ? '熔断' : '接口已接'
  const calls = tier?.calls == null ? '未返回' : `${tier.calls} 次`
  const fail = tier?.failures == null ? '失败未返回' : `失败 ${tier.failures}`
  const latency = tier?.p95Ms == null ? '延迟未返回' : `P95 ${Math.round(tier.p95Ms)} 毫秒`
  const queue = tier?.inFlight == null ? '' : `在途 ${tier.inFlight}`
  return [
    { id: `${id}:api`, title: '接口', face: api, sub: tier?.model || '模型名未返回', tone: 'flat' },
    { id: `${id}:route`, title: '负责', face: route, sub: '只出分，不下单', tone: 'flat' },
    { id: `${id}:calls`, title: '调用', face: calls, sub: [fail, latency, queue].filter(Boolean).join(' · '), tone: 'flat' },
    { id: `${id}:cost`, title: '花费', face: '未接入', sub: '没有令牌合计，调用次数不能折算金额', tone: 'flat' },
  ]
}

function qualityChildren(input: MonitorInput): ChildCard[] {
  if (!input.quality?.ok) return [{ id: 'quality:missing', title: '质量', face: '未返回', sub: '', tone: 'flat' }]
  if (input.quality.items.length === 0) {
    return [{ id: 'quality:clean', title: '质量', face: '没有问题记录', sub: '', tone: 'flat' }]
  }
  return input.quality.items.map((item) => ({
    id: `quality:${item.id || `${item.domain}:${item.checkName}:${item.symbol}`}`,
    title: item.domain || '未分域',
    face: item.hits == null ? item.checkName || '问题' : `${item.checkName || '问题'} ${item.hits} 条`,
    sub: [item.symbol, item.tradeDate, item.severity].filter(Boolean).join(' · '),
    tone: 'flat' as const,
  }))
}

function capCards(cards: ChildCard[]): ChildCard[] {
  if (cards.length <= 8) return cards
  return [...cards.slice(0, 8), { id: 'more', title: '还有', face: String(cards.length - 8), sub: '', tone: 'flat' }]
}

function buildTape(): SceneNode {
  return {
    id: 'book:tape',
    ring: 'book',
    title: '盘面',
    status: 'gap',
    detail: '情绪接口无返回',
    inputs: [],
    outputs: [],
    ...PLAIN,
  }
}

function buildServices(input: MonitorInput): { nodes: SceneNode[]; truncated: boolean } {
  if (!input.ops?.ok) return { nodes: [], truncated: false }
  const services = input.ops.services.map((item) => namedNode(`service:${slug(item.name)}`, 'service', item.name, item.status, item.detail))
  const resources = (input.ops.resources ?? []).map((item) =>
    namedNode(`resource:${slug(item.name)}`, 'service', item.name, item.status, item.detail),
  )
  if (services.length === 0 && resources.length === 0) return { nodes: [], truncated: false }
  return capNodes([...services, ...resources], 16)
}

/** 质量牌：后端给的是逐条问题，这里按域合并成一行摘要（详情窗才展开逐条） */
function qualityNode(input: MonitorInput): SceneNode | null {
  if (!input.quality?.ok) return null
  if (input.quality.items.length === 0) return null
  const tally = new Map<string, { issues: number; hits: number }>()
  for (const item of input.quality.items) {
    const key = item.domain || '未分域'
    const cur = tally.get(key) ?? { issues: 0, hits: 0 }
    cur.issues += 1
    cur.hits += item.hits ?? 0
    tally.set(key, cur)
  }
  const detail = [...tally.entries()].map(([domain, item]) => `${domain} ${item.issues} 条问题 / 命中 ${intText(item.hits)}`).join('\n')
  return { id: 'source:quality', ring: 'source', title: '质量', status: 'gap', detail, inputs: [], outputs: [], ...PLAIN }
}

function namedNode(id: string, ring: 'source' | 'service', title: string, status: string, detail: string): SceneNode {
  const mapped = mapStatus(status, detail)
  return { id, ring, title: title || id, status: mapped.status, detail: mapped.detail, inputs: [], outputs: [], ...PLAIN }
}

function mapStatus(raw: string, detail: string): { status: NodeStatus; detail: string } {
  const key = raw.trim().toLowerCase()
  if (LIVE_STATUS.has(key)) return { status: 'live', detail: detail || '正常' }
  if (DOWN_STATUS.has(key)) return { status: 'down', detail: detail || `状态为 ${raw}` }
  if (key) return { status: 'idle', detail: detail || raw }
  return { status: 'idle', detail: detail || '状态为空' }
}

function capNodes(nodes: SceneNode[], limit: number): { nodes: SceneNode[]; truncated: boolean } {
  const unique: SceneNode[] = []
  const seen = new Set<string>()
  for (const node of nodes) {
    if (seen.has(node.id)) continue
    seen.add(node.id)
    unique.push(node)
  }
  return { nodes: unique.slice(0, limit), truncated: unique.length > limit }
}

function buildEdges(sources: SceneNode[], known: Set<string>): SceneEdge[] {
  const edges: SceneEdge[] = []
  const add = (from: string, to: string) => {
    if (!known.has(from) || !known.has(to)) return
    const id = `${from}>${to}`
    if (edges.some((edge) => edge.id === id)) return
    edges.push({ id, from, to })
  }

  for (const source of sources) {
    if (source.id === 'source:none' || source.id === 'source:quality') continue
    for (const target of sourceTargets(`${source.id} ${source.title}`)) add(source.id, target)
  }
  for (const dim of DIM_LANES) add('post:scan', dim.id)
  for (const dim of DIM_LANES) add(dim.id, 'dim:synth')
  add('dim:synth', 'post:desk')
  add('post:desk', 'post:risk')
  add('post:risk', 'book:center')
  return edges
}

function sourceTargets(label: string): string[] {
  const text = label.toLowerCase()
  if (includesAny(text, ['新闻', '快讯', '公告', '研报', 'news', 'announce'])) return ['dim:news']
  if (includesAny(text, ['资金', '龙虎榜', '北向', 'capital'])) return ['dim:capital']
  if (includesAny(text, ['外围', '宏观', 'global', 'overseas'])) return ['dim:overseas']
  if (includesAny(text, ['涨停', '情绪', '板块'])) return ['post:scan']
  if (includesAny(text, ['通达信', 'tdx', 'tushare', 'akshare'])) return ['post:scan', 'dim:tech']
  return ['post:scan']
}

function buildSynth(input: MonitorInput, signals: MonitorSignal[]): SceneNode {
  const latest = signals.find((item) => item.score != null) ?? signals[0]
  const missing = !input.signals?.ok
  const face = missing || !latest || latest.score == null ? '未调用' : String(latest.score)
  return {
    id: 'dim:synth',
    ring: 'post',
    title: '综合',
    status: missing ? 'gap' : latest ? 'live' : 'idle',
    detail: missing ? '信号未返回' : latest ? `${latest.symbol} ${face}` : '综合没有分数',
    rank: 'brief',
    face,
    sub: latest?.degraded ? '规则分' : missing ? '信号未返回' : '',
    tone: 'flat',
    inputs: [],
    outputs: [],
  }
}

function buildModelNodes(input: MonitorInput): SceneNode[] {
  const tiers = input.brainStats?.ok ? input.brainStats.tiers : []
  const large = tiers.find((tier) => tier.tier === 'large') ?? tiers[0]
  const small = tiers.find((tier) => tier.tier === 'small') ?? tiers[1]
  return [
    modelNode('model:large', '大模型', input.brainStats?.ok ?? false, large),
    modelNode('model:small', '小模型', input.brainStats?.ok ?? false, small),
    tokenNode(input),
  ]
}

function modelNode(
  id: string,
  title: string,
  ok: boolean,
  tier?: { tier: string; model: string; configured: boolean; circuitOpen: boolean; calls?: number | null },
): SceneNode {
  const face = !ok ? '统计未返回' : !tier || tier.configured === false ? '接口未接' : tier.circuitOpen ? '熔断' : '接口已接'
  const calls = !tier || tier.calls == null ? '' : `${tier.calls} 次`
  const sub = !ok || !tier ? '统计未返回' : [tier.model || '未命名', calls].filter(Boolean).join(' · ')
  return {
    id,
    ring: 'post',
    title,
    status: tier?.circuitOpen ? 'down' : !ok || !tier || tier.configured === false ? 'gap' : 'live',
    detail: `${title} ${face}`,
    rank: tier?.circuitOpen ? 'focus' : 'brief',
    face,
    sub,
    tone: 'flat',
    inputs: [],
    outputs: [],
  }
}

/** S7：令牌节点——usage 通了就出真合计（仍是计数不是金额），没通保持 gap 占位 */
function tokenNode(input: MonitorInput): SceneNode {
  const usage = input.brainP1?.ok ? input.brainP1.usage : null
  if (!usage) {
    return {
      id: 'model:token',
      ring: 'post',
      title: '花费',
      status: 'gap',
      detail: '令牌未接入。调用次数不能折算金额',
      rank: 'brief',
      face: '令牌未接入',
      sub: '不能折算金额',
      tone: 'flat',
      inputs: [],
      outputs: [],
    }
  }
  const total = usage.total
  const sub = `入 ${compactAmount(total.promptTokens)} · 出 ${compactAmount(total.completionTokens)}`
  return {
    id: 'model:token',
    ring: 'post',
    title: '令牌',
    status: 'live',
    detail: `今日令牌合计 ${total.totalTokens.toLocaleString()}（入 ${total.promptTokens.toLocaleString()} / 出 ${total.completionTokens.toLocaleString()}，调用 ${intText(total.calls)} 次）。计数不是金额。`,
    rank: 'brief',
    face: compactAmount(total.totalTokens),
    sub,
    tone: 'flat',
    inputs: [],
    outputs: [],
  }
}

function buildMiss(input: MonitorInput): SceneNode {
  const candidates = input.candidates
  const misses = candidates?.ok ? candidates.items.filter((item) => item.missReason) : []
  const face = !candidates ? '候选未返回' : !candidates.ok ? '候选未返回' : misses.length === 0 ? '没有' : String(misses.length)
  return {
    id: 'post:miss',
    ring: 'post',
    title: '漏选',
    status: candidates?.ok ? 'live' : 'gap',
    detail: face,
    rank: 'brief',
    face,
    sub: '',
    tone: 'flat',
    inputs: [],
    outputs: [],
  }
}

const DIM_LANES: { id: string; title: string; keys: string[] }[] = [
  { id: 'dim:tech', title: '技术', keys: ['技术', '技术面', 'tech', 'technical'] },
  { id: 'dim:news', title: '消息', keys: ['消息', '消息面', 'news', 'intel'] },
  { id: 'dim:capital', title: '资金', keys: ['资金', '资金面', 'capital'] },
  { id: 'dim:overseas', title: '外围', keys: ['外围', 'overseas', 'external'] },
]

function buildDimNodes(input: MonitorInput, signals: MonitorSignal[]): SceneNode[] {
  return DIM_LANES.map((dim) => {
    const scored = signals
      .map((item) => ({ item, value: readDim(item.dims, dim.keys) }))
      .filter((row): row is { item: MonitorSignal; value: string } => row.value != null)
    const degraded = signals.some((item) => item.degraded)
    const missing = !input.signals?.ok
    const face = missing || scored.length === 0 ? '未调用' : scored.length === 1 ? scored[0].value : `${scored.length} 只`
    const sub = missing ? '信号未返回' : degraded && scored.length > 0 ? '规则分' : ''
    return {
      id: dim.id,
      ring: 'post' as const,
      title: dim.title,
      status: missing ? 'gap' : scored.length > 0 ? 'live' : 'idle',
      detail: missing ? '信号未返回' : scored.length === 0 ? `${dim.title}这一维没有分数` : `${dim.title} ${face}`,
      rank: 'brief' as const,
      face,
      sub,
      tone: 'flat' as const,
      inputs: [] as Identifier[],
      outputs: [] as Identifier[],
    }
  })
}

function readDim(dims: Record<string, unknown>, keys: string[]): string | null {
  const found = keys.find((key) => dims[key] != null && dims[key] !== '')
  if (!found) return null
  const raw = dims[found]
  const score = raw && typeof raw === 'object' ? (raw as { score?: unknown }).score : raw
  if (typeof score === 'number' && Number.isFinite(score)) return String(score)
  if (score == null || score === '') return null
  const text = String(score)
  return text === '未调用' ? null : text
}

function buildTerminals(input: MonitorInput, signals: MonitorSignal[]): TerminalModel[] {
  return [
    positionsTerminal(input),
    sectorsTerminal(input),
    radarTerminal(input, signals),
    heatTerminal(input),
    equityTerminal(input),
    riskTerminal(input),
  ]
}

function positionsTerminal(input: MonitorInput): TerminalModel {
  const term = blankTerm('term:positions', '持仓')
  if (!input.account || !input.account.ok || input.account.positionsOk === false) return { ...term, empty: '持仓接口无返回' }
  if (input.account.positions.length === 0) return { ...term, empty: '当前账本没有持仓' }
  return {
    ...term,
    empty: '',
    rows: input.account.positions.map((row) => ({
      label: row.symbol || '未知代码',
      value: `数量 ${intText(row.quantity)} 成本 ${amount(row.avgCost)} 现价 ${amount(row.lastPrice)}`,
      tone: priceTone(row.lastPrice, row.avgCost),
    })),
  }
}

function sectorsTerminal(input: MonitorInput): TerminalModel {
  const term = blankTerm('term:sectors', '板块')
  if (!input.sectors || !input.sectors.ok) return { ...term, empty: '板块接口无返回' }
  if (input.sectors.rows.length === 0) return { ...term, empty: '' }
  return {
    ...term,
    empty: '',
    rows: input.sectors.rows.map((row) => ({
      label: row.name || '未命名板块',
      value: row.avgPct == null ? '—' : `${row.avgPct.toFixed(2)}%`,
      tone: numberTone(row.avgPct),
    })),
  }
}

function radarTerminal(input: MonitorInput, signals: MonitorSignal[]): TerminalModel {
  const term = blankTerm('term:radar', '四维')
  if (!input.signals || !input.signals.ok) return { ...term, empty: '信号接口无返回' }
  const focus = input.focusSymbol
  const picked = focus ? signals.find((item) => item.symbol === focus) : signals[0]
  if (!picked) {
    return { ...term, empty: focus ? `没有 ${focus} 的信号，四维未形成` : '没有信号，四维未形成' }
  }
  return { ...term, empty: '', rows: radarRows(picked.dims) }
}

function heatTerminal(input: MonitorInput): TerminalModel {
  const term = blankTerm('term:heat', '涨停')
  if (!input.limits || !input.limits.ok) return { ...term, empty: '涨停接口无返回' }
  const rows = input.limits.rows.slice(0, 8)
  if (rows.length === 0) return { ...term, empty: '' }
  return {
    ...term,
    empty: '',
    rows: rows.map((row) => ({
      label: row.symbol || '未知代码',
      value: `${intText(row.consecutive)} ${row.status || ''}`.trim(),
      tone: 'flat' as const,
    })),
  }
}

function equityTerminal(input: MonitorInput): TerminalModel {
  const term = blankTerm('term:equity', '权益')
  const caption = equityCaption(input)
  if (!input.account || !input.account.ok) return { ...term, empty: '账户接口无返回', caption }
  return {
    ...term,
    empty: '',
    caption,
    rows: [
      { label: '权益', value: amount(input.account.equity), tone: 'flat' },
      { label: '当日', value: signed(input.account.dayPnl), tone: numberTone(input.account.dayPnl) },
      { label: '现金', value: amount(input.account.cash), tone: 'flat' },
    ],
  }
}

function riskTerminal(input: MonitorInput): TerminalModel {
  const term = blankTerm('term:risk', '风控')
  if (!input.risk || !input.risk.ok) return { ...term, empty: '风控状态无返回' }
  const risk = input.risk
  return {
    ...term,
    empty: '',
    rows: [
      { label: '模式', value: risk.mode || '—', tone: 'flat' },
      { label: '急停', value: risk.killActive ? risk.killReason || '已触发' : '未触发', tone: 'flat' },
      { label: '当日买单', value: intText(risk.buysToday), tone: 'flat' },
      { label: '熔断', value: risk.marketBreaker || '—', tone: 'flat' },
    ],
  }
}

function equityCaption(input: MonitorInput): string {
  if (input.equityCurve && !input.equityCurve.ok) return '回测报告无返回'
  const points = input.equityCurve?.points ?? []
  const valued = points.filter((point) => point.equity != null).length
  if (points.length >= 2 && valued >= 2) return '回测曲线，不是模拟盘权益'
  return '没有权益序列'
}

function buildNews(input: MonitorInput): NewsRow[] {
  if (input.focusSymbol && input.timeline?.ok && input.timeline.items.length > 0) {
    return input.timeline.items.slice(0, 12).map((item) => ({
      id: item.id || item.title,
      title: item.title,
      source: item.source,
      time: item.time,
      symbol: input.timeline?.symbol || input.focusSymbol,
    }))
  }
  if (input.briefing?.ok && input.briefing.items.length > 0) {
    return input.briefing.items.slice(0, 12).map((item, index) => ({
      id: `brief:${index}:${item.title}`,
      title: item.title,
      source: item.kind,
      time: input.briefing?.generatedAt || '',
      symbol: item.symbol,
    }))
  }
  if (input.hotWords?.ok && input.hotWords.items.length > 0) {
    return input.hotWords.items.slice(0, 12).map((item, index) => ({
      id: `hot:${index}:${item.target}`,
      title: item.target,
      source: item.type,
      time: '',
      symbol: '',
    }))
  }
  return []
}

const RADAR: { label: string; keys: string[] }[] = [
  { label: '技术', keys: ['技术', '技术面', 'tech', 'technical'] },
  { label: '消息', keys: ['消息', '消息面', 'news', 'intel'] },
  { label: '资金', keys: ['资金', '资金面', 'capital'] },
  { label: '外围', keys: ['外围', 'overseas', 'external'] },
]

function radarRows(dims: Record<string, unknown>): TerminalRow[] {
  return RADAR.map((dim) => {
    const found = dim.keys.find((key) => dims[key] != null && dims[key] !== '')
    if (!found) return { label: dim.label, value: '未调用', tone: 'flat' }
    const raw = dims[found]
    const score =
      raw && typeof raw === 'object' ? (raw as { score?: unknown }).score : raw
    const text = typeof score === 'number' && Number.isFinite(score) ? String(score) : score == null || score === '' ? '未调用' : String(score)
    return { label: dim.label, value: text, tone: 'flat' }
  })
}

function evidenceIdentifiers(evidence: string[], mode: 'factor' | 'rule'): Identifier[] {
  const out: Identifier[] = []
  for (const item of evidence) {
    if (mode === 'factor' && item.startsWith('F:')) out.push({ kind: 'F', value: item.slice(2) })
    if (mode === 'factor' && item.startsWith('N:')) out.push({ kind: 'N', value: item.slice(2) })
    if (mode === 'rule' && /^R\d{2}$/.test(item)) out.push({ kind: 'rule', value: item })
  }
  return out
}

function ruleIdentifiers(item: MonitorSignal): Identifier[] {
  const fromDims = Object.keys(item.dims)
    .filter((key) => /^R\d{2}$/.test(key))
    .map((key) => ({ kind: 'rule' as const, value: key }))
  return [...evidenceIdentifiers(item.evidence, 'rule'), ...fromDims]
}

function uniqueIds(items: Identifier[]): Identifier[] {
  const seen = new Set<string>()
  const out: Identifier[] = []
  for (const item of items) {
    const key = `${item.kind}:${item.value}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(item)
  }
  return out
}

function blankTerm(id: TerminalModel['id'], title: string): TerminalModel {
  return { id, title, rows: [], empty: '', caption: '' }
}

function numberTone(value: number | null): TerminalRow['tone'] {
  if (value == null || value === 0) return 'flat'
  return value > 0 ? 'up' : 'down'
}

function priceTone(last: number | null, cost: number | null): TerminalRow['tone'] {
  if (last == null || cost == null) return 'flat'
  return numberTone(last - cost)
}

function slug(value: string): string {
  return value.trim().toLowerCase()
}

function includesAny(text: string, keys: string[]): boolean {
  return keys.some((key) => text.includes(key.toLowerCase()))
}
