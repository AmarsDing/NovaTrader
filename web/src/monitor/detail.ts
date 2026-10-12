/**
 * S5 详情窗字段深化（监控驾驶舱业务监控方案 §5.1–§5.6）：账户/信号/风控/数据源/模型/扫描六张牌
 * 的字段级模型。纯函数，不取数——取数在 MonitorView（轮询与懒加载），这里只把 MonitorInput
 * 拼成人读的节/行/表。
 *
 * 纪律（§7/§10）：
 * - 缺字段写「未接入」并点名缺的接口，不写 0、不写破折号、不拿别的口径冒充；
 * - 全部只读。唯一的「出口」是 §5.2 的「回到交易台确认」——那是一次路由跳转，不是写操作；
 * - 日内曲线/回撤来自前端环形缓冲，注明「仅本次会话」。
 */
import { dimVotes, ticketRisk } from '@/view/decision-desk'
import {
  amount,
  clock,
  compactAmount,
  intText,
  missLabel,
  pctText,
  ratioPct,
  signalStatusLabel,
  signed,
  strategyLabel,
  validRemainText,
  isValidExpired,
} from '@/utils/format'
import { ageText, parseStamp } from './freshness'
import { buildFunnel, funnelCountText, funnelRate } from './funnel'
import { gpuMemory } from './hud'
import { sourceNodeId } from './freshness'
import type { MonitorInput, MonitorSignal } from './types'

/* ---------- 面板模型 ---------- */

export type DetailTone = 'up' | 'down' | 'flat' | 'warn' | 'danger'

export interface DetailRow {
  key: string
  label: string
  value: string
  /** 口径与依据——§5 要求每个字段都能回答「所以呢」 */
  note?: string
  tone?: DetailTone
  /** §5.3 限额环形：0–100 的用量比；>80% warn、=100% danger 由 ringTone 决定 */
  ring?: { pct: number; tone: 'ok' | 'warn' | 'danger' }
}

export interface DetailTable {
  key: string
  columns: string[]
  rows: string[][]
  /** 整行左边框（§5.1 距止损 <2% 转橙、已触发转红；§5.4 严重度） */
  flags?: ('' | 'warn' | 'danger')[]
  empty: string
}

export interface DetailSection {
  key: string
  title: string
  rows?: DetailRow[]
  table?: DetailTable
  /** 横条榜：§5.6 过滤原因分布、§5.5 评分分布直方图 */
  bars?: { label: string; count: number; tone?: 'warn' }[]
  /** 四维雷达（§5.2）：null 维画断开的弧，不是 0 */
  radar?: { label: string; value: number | null }[]
  /** 日内曲线（§5.1）：前端环形缓冲的采样点 */
  spark?: { points: (number | null)[]; caption: string }
  foot?: string
}

export interface DetailPanel {
  kind: 'account' | 'signal' | 'risk' | 'source' | 'model' | 'scan'
  sections: DetailSection[]
  /** §5.2 出口：只跳路由，本页没有任何确认/下单按钮 */
  exit?: { label: string; to: string }
}

/** 懒加载补充：MonitorView 取到后重算（全部只读 GET） */
export interface DetailExtra {
  /** 权益采样（前端 RingSeries 30 点，仅本次会话） */
  equityPoints?: (number | null)[]
  /** 近 7 日风控日报的急停事件（点开风控牌时按日读 /api/risk/report?date=） */
  killHistory?: { date: string; ok: boolean; events: RiskEvent[] }[] | null
  /** 证据链 N:123 的情报标题（GET /api/intel/items/{id}），取不到为 null */
  intel?: Record<string, { title: string; source: string; time: string } | null>
  /** S7：单只信号的研判正文摘录（GET /api/brain/reasoning?signal_id=），按 signal.id 键；取不到为 null */
  reasoning?: Record<string, { found: boolean; excerpt: string; latencyMs: number; model: string; createdAt: string } | null>
}

export interface RiskEvent {
  kind: string
  active: boolean
  mode: string
  source: string
  reason: string
  operator: string
  at: string
}

/* ---------- 缺口文案（接口名与 §7 表一致） ---------- */

export const GAP_PERF = '未接入：需要 GET /v1/strategy/performance'
export const GAP_REASONING = '研判正文未接入：需要 GET /v1/brain/reasoning?signal_id='
export const GAP_TOKEN = '令牌未接入：需要 GET /v1/brain/usage'
export const GAP_LATENCY_HIST = '未接入：需要 GET /v1/brain/latency-histogram'
export const GAP_SCORE_PNL = '未接入：需要 GET /v1/strategy/score-vs-pnl'
export const GAP_GPU = '未接入：需要 GET /v1/ops/gpu'
export const GAP_LADDER = '未接入：需要 GET /v1/market/ladder'
export const GAP_DAY_PNL = '未返回：trade Account 没有当日盈亏字段（需要 GET /v1/trade/account 补 day_pnl）'

/* ---------- 入口 ---------- */

/** 这张牌的深化类型；返回 null 表示该 id 没有六张深化牌，详情窗退回原有卡片网格 */
export function detailKindFor(input: MonitorInput, id: string): DetailPanel['kind'] | null {
  if (!id) return null
  if (id === 'book:center') return 'account'
  if (id === 'post:risk') return 'risk'
  if (id === 'post:scan' || id === 'post:miss') return 'scan'
  if (id === 'post:model' || id === 'model:large' || id === 'model:small' || id === 'model:token') return 'model'
  if (id === 'post:desk') return 'signal'
  if (id.startsWith('symbol:') && signalOf(input, id.slice('symbol:'.length))) return 'signal'
  if (id.startsWith('source:') && id !== 'source:quality' && sourceOf(input, id)) return 'source'
  return null
}

export function buildDetail(input: MonitorInput, id: string, now: number, extra: DetailExtra = {}): DetailPanel | null {
  const kind = detailKindFor(input, id)
  if (!kind) return null
  switch (kind) {
    case 'account':
      return { kind, sections: accountSections(input, extra) }
    case 'signal':
      return signalPanel(input, id, now, extra)
    case 'risk':
      return { kind, sections: riskSections(input, now, extra) }
    case 'source':
      return sourcePanel(input, id, now)
    case 'model':
      return modelPanel(input, id)
    case 'scan':
      return { kind, sections: scanSections(input) }
  }
}

/* ---------- 共享小件 ---------- */

type SourceItem = NonNullable<MonitorInput['sources']>['items'][number]

function signalOf(input: MonitorInput, symbol: string): MonitorSignal | undefined {
  return input.signals?.ok ? input.signals.items.find((item) => item.symbol === symbol) : undefined
}

function sourceOf(input: MonitorInput, id: string): SourceItem | undefined {
  const items = input.sources?.ok ? input.sources.items : []
  return items.find((item) => sourceNodeId(item.id, item.name) === id)
}

function pctParam(params: Record<string, string> | undefined, key: string): number | null {
  const raw = params?.[`risk.${key}`]
  if (raw == null || raw === '') return null
  const n = Number(raw)
  return Number.isFinite(n) ? n : null
}

/** §5.3：用量 >80% 转橙、=100% 转红 */
export function ringTone(pct: number): 'ok' | 'warn' | 'danger' {
  if (pct >= 100) return 'danger'
  if (pct >= 80) return 'warn'
  return 'ok'
}

function toneOf(value: number | null): DetailTone {
  if (value == null || value === 0) return 'flat'
  return value > 0 ? 'up' : 'down'
}

/** 证据链里的情报编号（N:123 → "123"），去重；懒加载标题用 */
export function intelIdsOf(evidence: string[]): string[] {
  const out: string[] = []
  for (const item of evidence) {
    const hit = /^N:(\d+)$/.exec(item.trim())
    if (hit && !out.includes(hit[1])) out.push(hit[1])
  }
  return out
}

/** 滑点 bp：买 = (成交均价−委托价)/委托价，卖反向——正为劣（§5.1） */
export function slippageBp(side: string, orderPrice: number | null, vwap: number | null): number | null {
  if (orderPrice == null || vwap == null || orderPrice <= 0) return null
  const buy = side.trim().toLowerCase()
  const diff = buy === 'sell' || buy === '卖' || buy === '卖出' ? orderPrice - vwap : vwap - orderPrice
  return (diff / orderPrice) * 10000
}

/** 委托的成交均价：fills 的 id 就是 clientOrderId（api/trade.ts 的映射），按它精确配对 */
export function fillVwap(orderId: string, fills: { id: string; price: number | null; qty: number | null }[]): number | null {
  let amountSum = 0
  let qtySum = 0
  for (const fill of fills) {
    if (fill.id !== orderId || fill.price == null || fill.qty == null) continue
    amountSum += fill.price * fill.qty
    qtySum += fill.qty
  }
  return qtySum > 0 ? amountSum / qtySum : null
}

/** 日内权益采样统计：最高点与最大回撤（30 点环形缓冲，仅本次会话） */
export function equityStats(points: (number | null)[]): { high: number | null; drawdownPct: number | null } {
  let high: number | null = null
  let maxDd: number | null = null
  for (const point of points) {
    if (point == null || point <= 0) continue
    if (high == null || point > high) high = point
    const dd = (high - point) / high
    if (maxDd == null || dd > maxDd) maxDd = dd
  }
  return { high, drawdownPct: maxDd }
}

/* ---------- §5.1 账户牌 ---------- */

function accountSections(input: MonitorInput, extra: DetailExtra): DetailSection[] {
  const account = input.account
  if (!account?.ok) {
    return [{ key: 'missing', title: '账户', rows: [{ key: 'm', label: '账户', value: '账户未返回', note: 'GET /api/trade/account 无应答' }] }]
  }
  const positions = account.positionsOk === false ? [] : account.positions
  const equity = account.equity

  const overview: DetailRow[] = [
    { key: 'book', label: '账户', value: account.book || '未返回', note: account.paperDays != null ? `模拟盘第 ${account.paperDays} 个交易日` : '' },
    { key: 'equity', label: '权益', value: amount(equity), note: '现金 + 持仓市值' },
    { key: 'cash', label: '现金', value: amount(account.cash), note: equity != null && account.cash != null ? `可用占比 ${ratioPct(account.cash / equity)}` : '' },
  ]
  if (account.dayPnl == null) {
    overview.push({ key: 'daypnl', label: '当日盈亏', value: GAP_DAY_PNL, tone: 'warn' })
  } else {
    overview.push({ key: 'daypnl', label: '当日盈亏', value: signed(account.dayPnl), tone: toneOf(account.dayPnl), note: equity ? `占权益 ${ratioPct(account.dayPnl / equity, 2)}` : '' })
  }
  overview.push({
    key: 'realized',
    label: '已实现盈亏',
    value: account.realized == null ? '未返回' : signed(account.realized),
    tone: account.realized == null ? 'flat' : toneOf(account.realized),
    note: '开户以来累计——trade 的 RollDay 不清零，不是「当日已实现」（§5.1 要求拆开说，避免拿浮动撑门面）',
  })
  overview.push({
    key: 'unrealized',
    label: '浮动盈亏',
    value: account.unrealized == null ? '未返回' : signed(account.unrealized),
    tone: account.unrealized == null ? 'flat' : toneOf(account.unrealized),
    note: '全部持仓的浮动合计（含往日建仓），不是「当日浮动」；当日口径需要 trade 补 day_realized/day_unrealized 字段',
  })
  if (account.openHalted) {
    overview.push({ key: 'halt', label: '开仓', value: '已停', tone: 'danger', note: account.haltReason || '对账不一致，停止开仓' })
  }

  /* 日内曲线与回撤：前端采样，刷新即失 */
  const stats = equityStats(extra.equityPoints ?? [])
  const sampled = (extra.equityPoints ?? []).some((point) => point != null)
  const curveRows: DetailRow[] = [
    {
      key: 'high',
      label: '日内最高权益',
      value: sampled && stats.high != null ? amount(stats.high) : '还没有采样点',
      note: '每 10 秒一点，最多 30 点',
    },
    {
      key: 'dd',
      label: '日内最大回撤',
      value: sampled && stats.drawdownPct != null ? ratioPct(stats.drawdownPct) : '还没有采样点',
      tone: stats.drawdownPct != null && stats.drawdownPct > 0.02 ? 'warn' : 'flat',
    },
    { key: 'bench', label: '基准（沪深300 同期）', value: '未接入：没有指数只读口', note: '需要 GET /v1/market/index 或并入行情域' },
  ]
  const curve: DetailSection = {
    key: 'curve',
    title: '日内曲线',
    rows: curveRows,
    spark: sampled ? { points: extra.equityPoints ?? [], caption: '权益采样（前端环形缓冲）' } : undefined,
    foot: '日内曲线仅本次会话：前端 30 点环形缓冲，不落库不上传，页面刷新即失（§9）。',
  }

  /* 持仓表：距止损/距止盈与终端带同式（last−stop)/last，<2% 转橙、≤0 转红 */
  const signals = input.signals?.ok ? input.signals.items : []
  const posRows: string[][] = []
  const posFlags: ('' | 'warn' | 'danger')[] = []
  for (const row of positions) {
    const value = row.lastPrice != null && row.quantity != null ? row.lastPrice * row.quantity : null
    const float = row.lastPrice != null && row.avgCost != null && row.quantity != null ? (row.lastPrice - row.avgCost) * row.quantity : null
    const floatPct = row.lastPrice != null && row.avgCost != null && row.avgCost > 0 ? (row.lastPrice - row.avgCost) / row.avgCost : null
    const weight = value != null && equity != null && equity > 0 ? value / equity : null
    const signal = signals.find((item) => item.symbol === row.symbol)
    let stopText = '止损价未对齐'
    let takeText = '止盈价未对齐'
    let flag: '' | 'warn' | 'danger' = ''
    if (row.lastPrice != null && row.lastPrice > 0) {
      if (signal?.stop != null) {
        const distance = (row.lastPrice - signal.stop) / row.lastPrice
        if (distance <= 0) {
          stopText = '已触发止损'
          flag = 'danger'
        } else {
          stopText = ratioPct(distance, 1)
          if (distance < 0.02) flag = 'warn'
        }
      }
      if (signal?.take != null) {
        takeText = ratioPct((signal.take - row.lastPrice) / row.lastPrice, 1)
      }
    }
    posRows.push([
      row.symbol,
      intText(row.quantity),
      amount(row.avgCost),
      amount(row.lastPrice),
      float == null ? '未返回' : signed(float),
      floatPct == null ? '未返回' : ratioPct(floatPct, 1),
      weight == null ? '未返回' : ratioPct(weight, 1),
      signal?.holdingDays != null ? `${signal.holdingDays} 天` : '未接入：持仓表没有建仓日期',
      stopText,
      takeText,
      GAP_LADDER,
    ])
    posFlags.push(flag)
  }
  const posTable: DetailSection = {
    key: 'positions',
    title: '持仓',
    table: {
      key: 'pos',
      columns: ['代码', '数量', '成本', '现价', '浮盈', '浮盈%', '仓位占比', '持有天数', '距止损', '距止盈', '今日高/低'],
      rows: posRows,
      flags: posFlags,
      empty: account.positionsOk === false ? '持仓未返回：GET /api/trade/positions 无应答' : '没有持仓',
    },
    foot: '距止损/距止盈按对齐的信号价计算（与终端带同式）；距止损 <2% 整行转橙、已跌破转红。红涨绿跌只用于浮盈列。',
  }

  /* 委托表：成交均价与滑点靠 fills 精确配对（fill.id 即 clientOrderId） */
  const fills = input.fills?.ok ? input.fills.items : []
  const orderRows: string[][] = []
  if (input.orders?.ok) {
    for (const order of input.orders.items) {
      const vwap = fillVwap(order.id, fills)
      const bp = slippageBp(order.side, order.price, vwap)
      orderRows.push([
        order.symbol,
        order.side || '—',
        order.status || '—',
        amount(order.price),
        intText(order.filled ?? null),
        vwap == null ? '未成' : amount(vwap),
        bp == null ? '未成' : `${bp.toFixed(1)} bp`,
      ])
    }
  }
  const orderSection: DetailSection = {
    key: 'orders',
    title: '委托',
    table: input.orders?.ok
      ? { key: 'ord', columns: ['代码', '方向', '状态', '委托价', '成交量', '成交均价', '滑点'], rows: orderRows, empty: '没有委托' }
      : undefined,
    rows: input.orders?.ok ? undefined : [{ key: 'ord', label: '委托', value: '委托未返回', note: 'GET /api/trade/orders 无应答' }],
    foot: '滑点 = (成交均价−委托价)/委托价×10000，买入正为劣、卖出正也为劣（卖单取反后同口径）。没成交的委托没有滑点。',
  }

  /* 成交：最近 20 条 */
  const fillRows = fills.slice(-20).reverse().map((fill) => [fill.time || '—', fill.symbol, fill.side || '—', amount(fill.price), intText(fill.qty)])
  const fillSection: DetailSection = {
    key: 'fills',
    title: '成交',
    table: input.fills?.ok
      ? { key: 'fill', columns: ['时间', '代码', '方向', '价', '量'], rows: fillRows, empty: '没有成交' }
      : undefined,
    rows: input.fills?.ok ? undefined : [{ key: 'fill', label: '成交', value: '成交未返回', note: 'GET /api/trade/fills 无应答' }],
    foot: '最近 20 条。',
  }

  const perf = input.strategyP1?.ok ? input.strategyP1.performance : null
  const perfRows: DetailRow[] = perf
    ? perf.sample > 0
      ? [
          { key: 'win', label: '近 20 笔胜率', value: pctText(perf.winRate * 100, 1), note: `${perf.sample} 笔已出后验` },
          { key: 'pl', label: '盈亏比', value: perf.plRatio > 0 ? perf.plRatio.toFixed(2) : '样本不足（缺盈利或缺亏损笔）' },
          { key: 'dd', label: '最大回撤', value: ratioPct(perf.maxDrawdown, 2), note: '按后验收益逐笔累计的峰谷差' },
          { key: 'avg', label: '平均实际收益', value: ratioPct(perf.avgReturn, 2) },
          {
            key: 'attr',
            label: '分维归因',
            value:
              perf.attributions.length > 0
                ? perf.attributions.map((a) => `${attributionLabel(a.attribution)} ×${a.count}`).join('，')
                : '还没有归因记录',
          },
        ]
      : [{ key: 'perf', label: '近 20 笔绩效', value: '样本不足：还没有已出后验的信号', tone: 'warn' }]
    : [{ key: 'perf', label: '近 20 笔胜率 / 盈亏比 / 最大回撤', value: GAP_PERF, tone: 'warn' }]

  return [
    { key: 'overview', title: '总览', rows: overview },
    curve,
    posTable,
    orderSection,
    fillSection,
    {
      key: 'perf',
      title: '绩效',
      rows: perfRows,
      foot: perf ? '口径：strategy_feedback 已出后验的近 20 笔（§5.1）。连亏天数与夏普后端没有，不编。' : '绩效未接入：不用当日数据冒充（§5.1）。',
    },
  ]
}

/* ---------- §5.2 信号牌 ---------- */

function signalPanel(input: MonitorInput, id: string, now: number, extra: DetailExtra): DetailPanel {
  const signals = input.signals?.ok ? input.signals.items : null
  if (!signals) {
    return { kind: 'signal', sections: [{ key: 'missing', title: '信号', rows: [{ key: 'm', label: '信号', value: '信号未返回', note: 'GET /api/strategy/signals 无应答' }] }] }
  }

  const symbol = id.startsWith('symbol:') ? id.slice('symbol:'.length) : ''
  const focus = symbol ? signals.find((item) => item.symbol === symbol) : undefined

  /* 信号列表（§5.2 第一块）：post:desk 给待确认，symbol 钻取也给全表对照 */
  const listRows = (id === 'post:desk' ? signals.filter((item) => ['approved', 'risk_checking', 'pending'].includes(item.status)) : signals).map((item) => {
    const votes = dimVotes(item.dims)
    const dimText = votes.map((vote) => `${vote.label.replace(/（.*）/, '')} ${vote.score == null ? '未评' : vote.score}`).join(' · ')
    const expired = isValidExpired(item.validUntil, now)
    return [
      item.symbol,
      item.name || '名称未返回',
      item.degraded ? `规则分 ${item.ruleScore ?? '未返回'}` : item.score == null ? '未返回' : String(item.score),
      dimText,
      signalStatusLabel(item.status),
      item.time || '—',
      !item.validUntil ? '未返回' : expired ? '已过期' : validRemainText(item.validUntil, now),
    ]
  })
  const listSection: DetailSection = {
    key: 'list',
    title: id === 'post:desk' ? '待确认信号' : '信号列表',
    table: { key: 'sig', columns: ['代码', '名称', '综合分', '四维分', '状态机位置', '生成时间', '有效期剩余'], rows: listRows, empty: id === 'post:desk' ? '没有待确认的信号' : '没有信号' },
    foot: id === 'post:desk' ? '点画布上的个股牌看单只钻取。' : '',
  }

  if (!focus) {
    return { kind: 'signal', sections: [listSection], exit: { label: '回到交易台确认', to: '/panel/trade' } }
  }

  /* 单只钻取：三价 + 依据、仓位 + 依据、置信度（§5.2） */
  const maxSingle = pctParam(input.risk?.params, 'max_single_pct')
  const stopCap = pctParam(input.risk?.params, 'stop_pct')
  const risk = ticketRisk({ positionPct: focus.positionPct ?? null, evidence: focus.evidence, reason: focus.reason ?? '' }, maxSingle)
  const atrText = focus.atr != null ? `ATR ${amount(focus.atr)}` : 'ATR 未返回'
  const drillRows: DetailRow[] = [
    {
      key: 'entry',
      label: '入场价',
      value: focus.entry == null ? '未返回' : amount(focus.entry),
      note: focus.entryLow != null && focus.entryHigh != null ? `依据：入场带 ${amount(focus.entryLow)}–${amount(focus.entryHigh)}，${atrText}` : `依据：${atrText}，入场带未返回`,
    },
    {
      key: 'stop',
      label: '止损价',
      value: focus.stop == null ? '未返回' : amount(focus.stop),
      note:
        focus.entry != null && focus.stop != null
          ? `依据：距入场 ${ratioPct((focus.entry - focus.stop) / focus.entry, 1)}（risk.stop_pct 上限 ${stopCap == null ? '未返回' : pctText(stopCap * 100)}）`
          : '',
    },
    {
      key: 'take',
      label: '止盈价',
      value: focus.take == null ? '未返回' : amount(focus.take),
      note: focus.entry != null && focus.stop != null && focus.take != null && focus.entry > focus.stop ? `依据：盈亏比 ${((focus.take - focus.entry) / (focus.entry - focus.stop)).toFixed(2)}` : '',
    },
    {
      key: 'pos',
      label: '建议仓位',
      value: focus.positionPct == null ? '未返回' : ratioPct(focus.positionPct),
      note: risk.rule ? `依据：${risk.rule} 夹到 ${risk.allowedPct == null ? '以风控实检为准' : ratioPct(risk.allowedPct)}；${risk.note}` : `依据：${risk.note}`,
    },
    { key: 'conf', label: '置信度', value: focus.score == null ? '未返回' : `${focus.score} 分`, note: focus.highValue ? '≥85 高价值（脉冲升级对象）' : focus.degraded ? '模型降级，只有规则分' : '' },
  ]
  const drill: DetailSection = { key: 'drill', title: `${focus.symbol} 单只钻取`, rows: drillRows }

  /* 四维雷达：缺的维 null → 断开的弧（NtRadar S5 起不再按 0 画） */
  const votes = dimVotes(focus.dims)
  const radarSection: DetailSection = {
    key: 'radar',
    title: '四维',
    radar: votes.map((vote) => ({ label: vote.label, value: vote.score })),
    rows: votes
      .filter((vote) => vote.score == null)
      .map((vote) => ({ key: `dim:${vote.key}`, label: vote.label, value: '未评', tone: 'warn' as const, note: '这一维模型没跑到（降级或超时），雷达上画成断开的弧，不按 0 画' })),
  }

  /* 证据链：F 因子逐条带值、N 情报编号配标题（懒加载，取不到写「未返回」） */
  const evidenceRows: string[][] = []
  const intel = extra.intel ?? {}
  for (const item of focus.evidence) {
    const text = item.trim()
    if (text.startsWith('F:')) {
      evidenceRows.push(['技术面', text.slice(2), '因子快照', ''])
    } else if (/^N:\d+$/.test(text)) {
      const nid = text.slice(2)
      const got = intel[nid]
      evidenceRows.push(['消息面', `N:${nid}`, got ? got.title : got === null ? '标题未返回' : '标题读取中', got ? `${got.source} ${got.time}`.trim() : `GET /api/intel/items/${nid}`])
    } else if (/^R\d{2}/.test(text)) {
      evidenceRows.push(['风控', text, '规则命中', ''])
    } else {
      evidenceRows.push(['其他', text, '', ''])
    }
  }
  const capital = input.factors?.ok && input.factors.symbol === focus.symbol ? input.factors.items : null
  if (capital) {
    for (const factor of capital.slice(0, 6)) {
      evidenceRows.push(['资金面', factor.name, factor.value == null ? '未返回' : String(factor.value), '主力净流入等因子（仅焦点股拉取）'])
    }
  } else if (input.factors && input.factors.symbol === focus.symbol && !input.factors.ok) {
    evidenceRows.push(['资金面', '主力净流入额', '未返回', 'GET /api/market/factors 无应答'])
  } else {
    evidenceRows.push(['资金面', '主力净流入额', '未拉取', '资金因子只拉焦点股'])
  }
  const overseas = input.sectors?.ok ? input.sectors.rows.find((row) => row.overseas != null) : undefined
  evidenceRows.push(['外围', overseas ? overseas.name : '关联外盘标的', overseas ? `外围 ${pctText(overseas.overseas)}` : '未返回', overseas ? '板块联动口径' : ''])
  const evidence: DetailSection = {
    key: 'evidence',
    title: '证据链',
    table: { key: 'ev', columns: ['维度', '编号/因子', '内容', '来源 · 时间'], rows: evidenceRows, empty: '这条信号没有带证据' },
  }

  /* 规则分对比：差值 >15 标注分歧 */
  const compareRows: DetailRow[] = []
  if (focus.aiScore != null && focus.ruleScore != null) {
    const diff = Math.abs(focus.aiScore - focus.ruleScore)
    compareRows.push({
      key: 'cmp',
      label: '模型分 vs 规则分',
      value: `${focus.aiScore} vs ${focus.ruleScore}`,
      tone: diff > 15 ? 'warn' : 'flat',
      note: diff > 15 ? `模型与规则分歧（差 ${diff} 分 > 15）` : '差值在 15 分以内',
    })
  } else {
    compareRows.push({
      key: 'cmp',
      label: '模型分 vs 规则分',
      value: focus.degraded ? '模型降级，只有规则分' : '未返回',
      note: `模型分 ${focus.aiScore == null ? '未返回' : focus.aiScore} · 规则分 ${focus.ruleScore == null ? '未返回' : focus.ruleScore}`,
    })
  }

  /* S7：研判正文摘录；extra.reasoning 按 signal.id 给，没有则仍写「未接入」 */
  const reasoning = focus ? extra.reasoning?.[focus.id] : undefined
  const reasoningRows: DetailRow[] = reasoning
    ? reasoning.found
      ? [
          { key: 'reason', label: '研判正文（首段）', value: reasoning.excerpt || '空', note: `${reasoning.model || '模型未返回'}，耗时 ${reasoning.latencyMs} ms，${reasoning.createdAt || ''}` },
        ]
      : [{ key: 'reason', label: '研判正文', value: '该信号没有挂到成功的模型调用', note: 'trace_id 为空或 llm_call_log 无 ok 行' }]
    : [{ key: 'reason', label: '研判正文', value: GAP_REASONING, tone: 'warn' }]

  return {
    kind: 'signal',
    sections: [
      listSection,
      drill,
      radarSection,
      evidence,
      { key: 'reason', title: '推理摘录', rows: reasoningRows, foot: reasoning ? undefined : '接口未通前绝不编造研判内容（§5.2）。' },
      { key: 'compare', title: '规则分对比', rows: compareRows },
      { key: 'history', title: '同类历史', rows: [{ key: 'hist', label: '同策略模板近 20 次（胜率/平均盈亏/最大单笔亏损）', value: '未接入：绩效接口（GET /v1/strategy/performance）是全策略口径，按模板拆分的行要等后端加 strategy 过滤', tone: 'warn' }] },
    ],
    exit: { label: '回到交易台确认', to: '/panel/trade' },
  }
}

/* ---------- §5.3 风控牌 ---------- */

interface RuleDef {
  code: string
  name: string
  desc: string
  effect: string
}

/** R01–R15 中文口径：名字与 pkg/risk/check.go 的链一致，处置方式按 shrink/passNote 的语义 */
export const RULES: RuleDef[] = [
  { code: 'R01', name: '授权', desc: '模式与准入允许这笔单；急停时只放行人工卖出', effect: '阻断' },
  { code: 'R02', name: '行情新鲜', desc: '报价延迟 ≤ quote_max_delay_ms，账户快照 ≤ account_max_age_sec', effect: '阻断' },
  { code: 'R03', name: '可交易', desc: '非停牌/退市，ST 按 allow_st_buy，上市满 min_list_days，不在黑名单', effect: '阻断' },
  { code: 'R04', name: '涨跌停与价格笼子', desc: '委托价在当日涨跌停与交易所价格笼子内', effect: '阻断' },
  { code: 'R05', name: '手数', desc: '100 股整数倍，按板块取整；放不下一手即拦', effect: '阻断' },
  { code: 'R06', name: '可卖数量', desc: '卖出不超过持仓可用', effect: '阻断' },
  { code: 'R07', name: '单票仓位', desc: '单票市值 ≤ 权益 × max_single_pct', effect: '缩量放行' },
  { code: 'R08', name: '总仓', desc: '持仓合计 ≤ 权益 × max_total_pct', effect: '缩量放行' },
  { code: 'R09', name: '板块集中', desc: '单板块 ≤ 权益 × max_sector_pct；订单没有板块信息时跳过并记录', effect: '缩量放行 / 仅记录' },
  { code: 'R10', name: '当日开仓笔数', desc: '当日买入笔数 ≤ max_daily_buys', effect: '阻断' },
  { code: 'R11', name: '流动性', desc: '成交额 ≥ min_turnover，参与率 ≤ max_participation', effect: '缩量放行；低于下限阻断' },
  { code: 'R12', name: '资金', desc: '委托金额 ≤ 可用资金（扣在途）', effect: '缩量放行' },
  { code: 'R13', name: '禁止自成交', desc: '同账户同代码反向挂单不撮合', effect: '阻断' },
  { code: 'R14', name: '速度上限', desc: '每秒/每日申报笔数低于程序化交易认定线（300/s、2 万/日）', effect: '阻断' },
  { code: 'R15', name: '熔断', desc: '账户当日亏损锁存（daily_loss_pct）或市场熔断时不开仓', effect: '阻断' },
]

function riskSections(input: MonitorInput, now: number, extra: DetailExtra): DetailSection[] {
  const risk = input.risk
  if (!risk?.ok) {
    return [{ key: 'missing', title: '风控', rows: [{ key: 'm', label: '风控', value: '风控未返回', note: 'GET /api/risk/state 无应答' }] }]
  }

  const state: DetailRow[] = [
    { key: 'mode', label: '模式', value: risk.mode || '未返回', note: 'SIM 模拟盘 / LIVE 实盘（切换在交易台，本页只读）' },
    {
      key: 'kill',
      label: '急停状态',
      value: risk.killActive ? '已触发' : '未触发',
      tone: risk.killActive ? 'danger' : 'flat',
      note: risk.killActive ? `原因：${risk.killReason || '未返回'}` : '',
    },
  ]
  if (risk.killActive) {
    state.push({ key: 'killat', label: '急停触发时间', value: risk.killAt || '未返回', note: risk.killSource ? `来源：${risk.killSource}` : '' })
  }
  state.push({
    key: 'breaker',
    label: '市场熔断',
    value: risk.marketBreaker || '无',
    note: risk.breakers && Object.keys(risk.breakers).length > 0 ? `账户熔断：${Object.entries(risk.breakers).map(([k, v]) => `${k} ${v}`).join('；')}` : '',
  })
  state.push({ key: 'quote', label: '最近行情时刻', value: risk.lastQuoteAt || '未返回', note: risk.lastQuoteAt ? `${ageText(ageOf(risk.lastQuoteAt, now))}前读到` : '' })

  /* 限额仪表：n/N 环形，>80% 橙、=100% 红 */
  const account = input.account?.ok ? input.account : null
  const equity = account?.equity ?? null
  const maxBuys = pctParam(risk.params, 'max_daily_buys')
  const maxSingle = pctParam(risk.params, 'max_single_pct')
  const maxTotal = pctParam(risk.params, 'max_total_pct')
  const maxLoss = pctParam(risk.params, 'daily_loss_pct')
  const positions = account && account.positionsOk !== false ? account.positions : []
  const totalValue = positions.reduce((sum, row) => sum + (row.lastPrice != null && row.quantity != null ? row.lastPrice * row.quantity : 0), 0)
  const singleMax = positions.reduce((top, row) => {
    const value = row.lastPrice != null && row.quantity != null ? row.lastPrice * row.quantity : 0
    return Math.max(top, equity != null && equity > 0 ? value / equity : 0)
  }, 0)
  const totalPct = equity != null && equity > 0 ? totalValue / equity : null

  const limitRows: DetailRow[] = [
    limitRow('buys', '当日买入', risk.buysToday, maxBuys, (n, cap) => `${intText(n)} / ${intText(cap)} 笔`, 'risk.max_daily_buys'),
    limitRow(
      'single',
      '单票最大仓位',
      singleMax * 100,
      maxSingle != null ? maxSingle * 100 : null,
      (n, cap) => `${n.toFixed(1)}% / ${cap.toFixed(1)}%`,
      'risk.max_single_pct',
      positions.length === 0 ? '没有持仓' : '',
    ),
    limitRow('total', '总仓位', totalPct != null ? totalPct * 100 : null, maxTotal != null ? maxTotal * 100 : null, (n, cap) => `${n.toFixed(1)}% / ${cap.toFixed(1)}%`, 'risk.max_total_pct'),
  ]
  if (account?.dayPnl == null || maxLoss == null) {
    limitRows.push({
      key: 'loss',
      label: '单日最大亏损',
      value: account?.dayPnl == null ? GAP_DAY_PNL : '参数未返回：risk.daily_loss_pct',
      tone: 'warn',
      note: maxLoss != null ? `上限 ${pctText(maxLoss * 100)}` : '',
    })
  } else {
    const used = Math.max(0, -account.dayPnl) / (equity || 1)
    const cap = maxLoss
    const pct = Math.min(100, (used / cap) * 100)
    limitRows.push({
      key: 'loss',
      label: '单日最大亏损',
      value: `${ratioPct(used)} / ${pctText(cap * 100)}`,
      note: '达到上限即锁仓，今日不再开仓（R15）',
      ring: { pct, tone: ringTone(pct) },
    })
  }
  limitRows.push({
    key: 'streak',
    label: '连续亏损熔断计数',
    value: '后端无此参数',
    note: '熔断 = 日内亏损线按账户锁存（R15）+ 市场熔断；没有独立的连亏计数器，不编 k/K',
  })
  /* S7：引擎口径限额表（GET /api/risk/limits）——比上面前端估算多速度上限与板块集中，且含在途买单 */
  const engineLimits = input.riskLimits?.ok ? input.riskLimits.items : null
  const engineSection: DetailSection = {
    key: 'engine-limits',
    title: '引擎限额（含在途）',
    table: engineLimits
      ? {
          key: 'eng',
          columns: ['限额', '用量', '上限', '占比', '口径'],
          rows: engineLimits.map((item) => [
            item.name,
            item.used < 0 ? '不可得' : fmtLimitValue(item.used, item.unit),
            item.limit < 0 ? '不适用' : fmtLimitValue(item.limit, item.unit),
            item.ratio < 0 ? '—' : pctText(item.ratio * 100, 0),
            item.note,
          ]),
          empty: '限额接口没有返回条目',
        }
      : undefined,
    rows: engineLimits ? undefined : [{ key: 'eng', label: '引擎限额', value: '未接入：需要 GET /v1/risk/limits', tone: 'warn' }],
    foot: engineLimits ? '用量为风控引擎内存口径：持仓市值 + 在途买单 + 未落地预约（R08），速度为每秒/当日计数（R14）。' : undefined,
  }
  const limitsSection: DetailSection = { key: 'limits', title: '限额仪表', rows: limitRows, foot: '环形 >80% 转橙、=100% 转红（§5.3）。' }

  /* R01–R15 逐条：今日命中次数来自日报 rejects，只有计数没有明细 */
  const report = input.riskReport?.ok ? input.riskReport : null
  const rejectMap = new Map((report?.rejects ?? []).map((item) => [item.rule, item.count]))
  const ruleRows = RULES.map((rule) => {
    const hit = report ? (rejectMap.has(rule.code) ? intText(rejectMap.get(rule.code)) : '今日未命中') : '未返回'
    return [rule.code, `${rule.name}：${rule.desc}`, hit, rule.effect]
  })
  const ruleSection: DetailSection = {
    key: 'rules',
    title: '规则明细（R01–R15）',
    table: { key: 'rule', columns: ['规则', '说明', '今日命中', '处置'], rows: ruleRows, empty: '' },
    foot: '最近命中时间与最近被拦的票未接入：风控日报（DailyReportReply）只给逐规则计数，没有逐条明细。',
  }

  /* 急停历史：今日事件 + 懒加载的近 7 日 */
  const killRows: string[][] = []
  const todayEvents = report?.events ?? []
  for (const event of todayEvents) {
    killRows.push([report?.date || '今日', clock(event.at) || event.at || '—', event.active ? '触发' : '恢复', event.reason || '未返回', event.source || event.operator || '—'])
  }
  const history = extra.killHistory
  if (history) {
    for (const day of history) {
      if (!day.ok) {
        killRows.push([day.date, '—', '未返回', 'GET /api/risk/report?date= 无应答', ''])
        continue
      }
      for (const event of day.events) {
        killRows.push([day.date, clock(event.at) || event.at || '—', event.active ? '触发' : '恢复', event.reason || '未返回', event.source || event.operator || '—'])
      }
    }
  }
  const killSection: DetailSection = {
    key: 'kills',
    title: '急停历史',
    table: { key: 'kill', columns: ['日期', '时间', '状态', '原因', '来源'], rows: killRows, empty: history == null ? '今日没有急停事件；近 7 日历史读取中' : '近 7 日没有急停事件' },
    foot: history == null ? '近 7 日按日读取（GET /api/risk/report?date=），读到后补进来。' : '',
  }

  /* 准入 */
  const admit = input.admission
  const admitRows: DetailRow[] = []
  if (!admit?.ok) {
    admitRows.push({ key: 'admit', label: '准入', value: '准入未返回', note: 'GET /api/backtest/admission 无应答' })
  } else {
    const left = admit.tradingDays != null && admit.requiredDays != null ? Math.max(0, admit.requiredDays - admit.tradingDays) : null
    admitRows.push({
      key: 'progress',
      label: '模拟盘进度',
      value: admit.tradingDays == null || admit.requiredDays == null ? '未返回' : `${admit.tradingDays} / ${admit.requiredDays} 个交易日`,
      note: left == null ? '' : left === 0 ? '天数已满，看分项' : `还差 ${left} 个交易日`,
    })
    for (const item of admit.items) {
      admitRows.push({ key: `admit:${item.label}`, label: item.label, value: item.status || '—', note: item.detail })
    }
    admitRows.push({ key: 'ready', label: '结论', value: admit.ready ? '通过，可以半自动实盘' : '未通过', tone: admit.ready ? 'up' : 'flat' })
  }

  return [
    { key: 'state', title: '状态', rows: state },
    limitsSection,
    engineSection,
    ruleSection,
    killSection,
    { key: 'admit', title: '准入', rows: admitRows },
  ].map((section, index, all) => (index === all.length - 1 ? { ...section, foot: [section.foot, '本页只读，急停在交易台（§5.3）。'].filter(Boolean).join(' ') } : section))
}

function limitRow(key: string, label: string, used: number | null, cap: number | null, text: (n: number, cap: number) => string, param: string, extraNote = ''): DetailRow {
  if (used == null) return { key, label, value: '用量未返回', tone: 'warn', note: extraNote }
  if (cap == null || cap <= 0) return { key, label, value: '上限未返回', tone: 'warn', note: `参数 ${param} 未返回`, }
  const pct = Math.min(100, (used / cap) * 100)
  return { key, label, value: text(used, cap), note: extraNote, ring: { pct, tone: ringTone(pct) } }
}

/** 引擎限额条目的用量/上限按单位格式化：元→压缩金额，%→比率，笔→整数。 */
function fmtLimitValue(value: number, unit: string): string {
  if (unit === '元') return compactAmount(value)
  if (unit === '%') return ratioPct(value, 2)
  if (unit === '笔每秒') return `${value.toFixed(0)} 笔/s`
  return intText(value)
}

function ageOf(text: string, now: number): number {
  const stamp = parseStamp(text)
  return stamp == null ? 0 : Math.max(0, (now - stamp) / 1000)
}

/* ---------- §5.4 数据源牌 ---------- */

function sourcePanel(input: MonitorInput, id: string, now: number): DetailPanel {
  const item = sourceOf(input, id)
  if (!item) {
    return { kind: 'source', sections: [{ key: 'missing', title: '数据源', rows: [{ key: 'm', label: '数据源', value: '源未返回', note: 'GET /api/datahub/sources 无应答' }] }] }
  }
  const okRate = item.successRate != null ? (item.successRate <= 1 ? item.successRate * 100 : item.successRate) : null
  const failRate = okRate != null ? 100 - okRate : null

  const state: DetailRow[] = [
    { key: 'status', label: '当前状态', value: item.status || '未返回', note: [item.enabled === false ? '已停用' : '启用中', item.active ? '在用' : '', item.official ? '官方源' : '', item.priority != null ? `优先级 ${item.priority}` : ''].filter(Boolean).join(' · ') },
    { key: 'lastok', label: '最近成功时间', value: item.lastAt || '未返回', note: item.lastAt ? `${ageText(ageOf(item.lastAt, now))}前` : '' },
    { key: 'lastfail', label: '最近失败时间', value: item.lastFailureAt || '没有失败记录', note: item.lastFailureAt ? `${ageText(ageOf(item.lastFailureAt, now))}前` : '' },
    { key: 'streak', label: '连续失败', value: item.consecutiveFailures == null ? '未返回' : `${item.consecutiveFailures} 次`, tone: (item.consecutiveFailures ?? 0) > 0 ? 'warn' : 'flat' },
  ]

  const throughput: DetailRow[] = [
    { key: 'succ', label: '成功数', value: intText(item.successCount ?? null) },
    { key: 'fail', label: '失败数', value: intText(item.failureCount ?? null) },
    { key: 'failrate', label: '失败率', value: failRate == null ? '未返回' : pctText(failRate, 1), tone: failRate != null && failRate > 20 ? 'warn' : 'flat' },
    { key: 'avg', label: '平均延迟', value: item.avgLatencyMs == null ? '未返回' : `${Math.round(item.avgLatencyMs)} ms` },
    { key: 'last', label: '最近延迟', value: item.latencyMs == null ? '未返回' : `${Math.round(item.latencyMs)} ms` },
    { key: 'p95', label: 'P95 延迟', value: '未接入', note: 'SourceStatus 只给 last_latency_ms 与 avg_latency_ms，没有分位桶' },
  ]

  const errorRows: DetailRow[] = [
    {
      key: 'err',
      label: '最近错误原文',
      value: item.lastError ? clipText(item.lastError, 120) : '没有错误记录',
      note: item.lastError && item.lastError.length > 120 ? `原文 ${item.lastError.length} 字，截断 120 字（§5.4）` : '',
    },
  ]

  const quota: DetailRow[] =
    item.quotaLimit == null
      ? [{ key: 'quota', label: '配额', value: '未返回', note: 'SourceStatus 没有配额字段' }]
      : item.quotaLimit <= 0
        ? [{ key: 'quota', label: '配额', value: '无限流', note: 'quota_limit = 0（§5.4：无此概念的源写「无限流」）' }]
        : [{ key: 'quota', label: '配额', value: `${intText(item.quotaUsed ?? null)} / ${intText(item.quotaLimit)}`, note: item.quotaUsed != null && item.quotaUsed / item.quotaLimit > 0.8 ? '用量超过 80%' : '' }]

  /* 数据质量：该源覆盖域的逐条问题，缺数/过期按检查项名字分桶 */
  const domain = item.kind || ''
  const issues = (input.quality?.ok ? input.quality.items : []).filter((issue) => !domain || issue.domain === domain)
  const missingHits = issues.filter((issue) => /missing|gap|缺/.test(issue.checkName)).reduce((sum, issue) => sum + (issue.hits ?? 0), 0)
  const staleHits = issues.filter((issue) => /stale|expired|过期/.test(issue.checkName)).reduce((sum, issue) => sum + (issue.hits ?? 0), 0)
  const qualityRows = issues.map((issue) => [
    issue.checkName || '—',
    issue.symbol || '—',
    issue.tradeDate || '—',
    issue.severity || '—',
    issue.hits == null ? '未返回' : String(issue.hits),
    issue.lastSeen || '—',
    clipText(issue.message || '', 60),
  ])
  const qualityFlags = issues.map((issue) => (issue.severity === 'high' ? 'warn' as const : ''))
  const qualitySection: DetailSection = {
    key: 'quality',
    title: '数据质量',
    rows: input.quality?.ok
      ? [
          { key: 'miss', label: '缺数条数', value: issues.length === 0 ? '没有问题记录' : `${missingHits} 条`, note: domain ? `域 ${domain} 的逐条问题按检查项分桶` : '' },
          { key: 'stale', label: '过期条数', value: issues.length === 0 ? '没有问题记录' : `${staleHits} 条` },
          { key: 'cal', label: '交易日是否对齐', value: '未接入', note: '质量问题只给逐条 trade_date，没有全市场交易日历比对' },
        ]
      : [{ key: 'q', label: '质量', value: '质量未返回', note: 'GET /api/datahub/quality 无应答' }],
    table: input.quality?.ok && issues.length > 0 ? { key: 'qi', columns: ['检查项', '代码', '交易日', '严重度', '条数', '最近出现', '说明'], rows: qualityRows, flags: qualityFlags, empty: '' } : undefined,
  }

  return {
    kind: 'source',
    sections: [
      { key: 'state', title: '状态', rows: state },
      { key: 'throughput', title: '吞吐', rows: throughput },
      { key: 'error', title: '错误样本', rows: errorRows },
      { key: 'quota', title: '配额', rows: quota },
      qualitySection,
      { key: 'config', title: '配置', rows: [{ key: 'cfg', label: '配置表单', value: '见下方表单', note: '本页唯一的写操作：只写配置项，不启停服务（§5.4）' }] },
    ],
  }
}

function clipText(text: string, max: number): string {
  const trimmed = text.trim()
  return trimmed.length <= max ? trimmed : `${trimmed.slice(0, max)}…`
}

/* ---------- §5.5 模型牌 ---------- */

function modelPanel(input: MonitorInput, id: string): DetailPanel {
  const tiers = input.brainStats?.ok ? input.brainStats.tiers : null
  const focusTier = id === 'model:small' ? 'small' : id === 'model:large' ? 'large' : ''
  const shown = tiers ? tiers.filter((tier) => !focusTier || tier.tier === focusTier) : null

  const tierRows: string[][] = (shown ?? []).map((tier) => {
    const calls = tier.calls ?? null
    const failures = tier.failures ?? null
    const failRate = calls != null && calls > 0 && failures != null ? (failures / calls) * 100 : null
    return [
      tier.tier || '—',
      tier.model || '未配置',
      tier.configured === false ? '未配置' : tier.circuitOpen ? '熔断' : '在线',
      calls == null ? '未返回' : String(calls),
      failures == null ? '未返回' : String(failures),
      failRate == null ? '未返回' : pctText(failRate, 1),
      tier.p50Ms == null ? '未返回' : `${Math.round(tier.p50Ms)} ms`,
      tier.p95Ms == null ? '未返回' : `${Math.round(tier.p95Ms)} ms`,
      intText(tier.inFlight ?? null),
      intText(tier.queued ?? null),
      tier.kvCacheUsage == null ? '未返回' : `${tier.kvCacheUsage}%`,
    ]
  })
  const tierSection: DetailSection = {
    key: 'tiers',
    title: '分档',
    table: tiers
      ? { key: 'tier', columns: ['档位', '模型名', '状态', '调用数', '失败数', '失败率', 'P50', 'P95', '在途', '排队', 'KV 缓存'], rows: tierRows, empty: focusTier ? `没有 ${focusTier} 档的统计` : '没有分档统计' }
      : undefined,
    rows: tiers ? undefined : [{ key: 't', label: '分档', value: '统计未返回', note: 'GET /api/brain/stats 无应答' }],
  }

  /* 评分自检：分布直方图按当前信号列表分桶；散点/相关系数/归因是缺口 */
  const signals = input.signals?.ok ? input.signals.items : null
  const scored = (signals ?? []).map((item) => item.score).filter((score): score is number => score != null)
  const buckets = scoreHistogram(scored)
  const gpu = gpuMemory(input)
  const selfSection: DetailSection = {
    key: 'selfcheck',
    title: '评分自检',
    bars: signals ? buckets.map((bucket) => ({ label: bucket.label, count: bucket.count, tone: bucket.label.startsWith('8') || bucket.label.startsWith('9') ? 'warn' as const : undefined })) : undefined,
    rows: [
      ...(signals
        ? [{ key: 'note', label: '分布口径', value: scored.length === 0 ? '今日没有信号评分' : `按当前信号列表 ${scored.length} 条分桶`, note: '不是全市场评分的全量（全量在扫描执行完的 ScanReply 里，只读拿不到）' }]
        : [{ key: 'note', label: '分布', value: '信号未返回', tone: 'warn' as const }]),
      ...scatterRows(input),
    ],
  }

  /* S7：延迟直方图（真实 8 桶）与令牌用量（按档 + 合计）；拉不到仍写「未接入」 */
  const brainP1 = input.brainP1?.ok ? input.brainP1 : null
  const latency = brainP1?.latency ?? null
  const usage = brainP1?.usage ?? null

  const latencyRows: DetailRow[] = latency
    ? [
        { key: 'win', label: '窗口', value: `近 ${latency.windowMinutes} 分钟，${latency.total} 次成功调用` },
        { key: 'p50', label: 'P50', value: `${Math.round(latency.p50Ms)} ms` },
        { key: 'p95', label: 'P95', value: `${Math.round(latency.p95Ms)} ms` },
      ]
    : [{ key: 'hist', label: '近 1 小时 8 桶（桶宽按 P95 自适应）', value: GAP_LATENCY_HIST, tone: 'warn' }]
  const latencySection: DetailSection = {
    key: 'latency',
    title: '延迟直方图',
    bars: latency && latency.total > 0
      ? latency.buckets.map((b) => ({ label: `≤${Math.round(b.leMs)}ms`, count: b.count }))
      : undefined,
    rows: latencyRows,
  }

  const tokenRows: DetailRow[] = usage
    ? usage.tiers.map((t) => ({
        key: `u-${t.tier}`,
        label: `${t.tier}${t.model ? `（${t.model}）` : ''}`,
        value: `${t.promptTokens} / ${t.completionTokens} / ${t.totalTokens}`,
        note: `prompt / completion / 合计，窗口 ${usage.window}，调用 ${t.calls} 次失败 ${t.failures} 次`,
      }))
    : []
  const tokenSection: DetailSection = {
    key: 'token',
    title: '令牌',
    rows: usage
      ? [
          ...tokenRows,
          {
            key: 'u-total',
            label: '合计',
            value: `${usage.total.promptTokens} / ${usage.total.completionTokens} / ${usage.total.totalTokens}`,
            note: `窗口起点 ${usage.since || '未返回'}`,
          },
        ]
      : [{ key: 'token', label: 'prompt / completion / 合计', value: GAP_TOKEN, tone: 'warn' }],
    foot: usage ? undefined : '接口未做前固定写「令牌未接入」：不写 0，也不用调用次数折算金额（§5.5）。',
  }

  return {
    kind: 'model',
    sections: [
      tierSection,
      latencySection,
      { key: 'failures', title: '失败原因 Top5', rows: [{ key: 'top', label: '失败原因分组', value: '未接入', note: 'BrainStats 只有失败计数，没有原因分组（需要 brain 补 failure_reason 统计）' }] },
      tokenSection,
      {
        key: 'throughput',
        title: '吞吐',
        rows: [
          usage && usage.total.calls > 0
            ? {
                key: 'tps',
                label: 'tokens/s（输出）',
                value: tokensPerSecond(usage),
                note: `按窗口 ${usage.window} 平均`,
              }
            : { key: 'tps', label: 'tokens/s（输入/输出分开）', value: GAP_TOKEN, tone: 'warn', note: '需要令牌计数与时间窗' },
          { key: 'gpu', label: 'GPU 利用率 / 温度 / 功耗', value: GAP_GPU, tone: 'warn' },
          { key: 'vram', label: '显存', value: gpu ? gpu.text : '未返回', note: gpu ? `占用 ${gpu.pct}%（ops 资源节点文本解析）` : 'ops 资源节点没有显存信息' },
        ],
      },
      { key: 'route', title: '路由说明', rows: [{ key: 'route', label: '固定路由', value: '技术/资金走小模型，消息/外围/综合走大模型', note: '大模型只出评分和理由，不直接下单（决策 3）' }] },
      selfSection,
    ],
  }
}

/** 窗口内平均输出 tokens/s；没有 usage 或窗口为 0 时返回 null */
function tokensPerSecond(usage: { window: string; since: string; total: { completionTokens: number } }): string {
  const since = Date.parse(usage.since)
  if (!Number.isFinite(since)) return '未返回'
  const seconds = (Date.now() - since) / 1000
  if (seconds <= 0) return '未返回'
  return `${(usage.total.completionTokens / seconds).toFixed(1)} tok/s`
}

/** 评分分布 0–100 十桶 */
export function scoreHistogram(scores: number[]): { label: string; count: number }[] {
  const buckets = Array.from({ length: 10 }, (_, index) => ({ label: `${index * 10}–${index * 10 + 10}`, count: 0 }))
  for (const score of scores) {
    if (!Number.isFinite(score)) continue
    const index = Math.min(9, Math.max(0, Math.floor(score / 10)))
    buckets[index].count += 1
  }
  return buckets
}

const ATTRIBUTION_LABEL: Record<string, string> = {
  indicator_failed: '指标失效',
  news_misread: '消息误判',
  unfilled: '未成交',
  emotion_mismatch: '情绪错配',
  missed: '漏选',
  未归因: '未归因',
}

export function attributionLabel(code: string): string {
  return ATTRIBUTION_LABEL[code] || code || '未归因'
}

/** 评分-收益散点 + 相关系数（§5.5 评分自检段）。拉不到写「未接入」，样本不足明说。 */
function scatterRows(input: MonitorInput): DetailRow[] {
  const p1 = input.strategyP1?.ok ? input.strategyP1 : null
  const scatter = p1?.scatter ?? null
  const perf = p1?.performance ?? null
  const rows: DetailRow[] = []
  if (!scatter) {
    rows.push({ key: 'scatter', label: '评分 vs 实际收益散点 + 相关系数（盘后）', value: GAP_SCORE_PNL, tone: 'warn' })
  } else if (scatter.sample < 2) {
    rows.push({ key: 'scatter', label: '评分 vs 实际收益相关系数', value: '样本不足：挂得上信号的已后验笔数少于 2', tone: 'warn' })
  } else {
    rows.push({
      key: 'scatter',
      label: '评分 vs 实际收益相关系数',
      value: scatter.pearson.toFixed(2),
      note: `近 ${scatter.sample} 笔（Pearson；|r|>0.5 算有区分度）`,
      tone: Math.abs(scatter.pearson) >= 0.5 ? 'up' : 'flat',
    })
    // 分桶均值：80+ 与 <70 两档的实际收益对比，比散点图本身更好读
    const high = scatter.points.filter((p) => p.score >= 80)
    const low = scatter.points.filter((p) => p.score < 70)
    if (high.length > 0 && low.length > 0) {
      const avg = (list: typeof high) => list.reduce((sum, p) => sum + p.actualReturn, 0) / list.length
      rows.push({
        key: 'bands',
        label: '高分组（≥80） vs 低分组（<70）平均后验',
        value: `${ratioPct(avg(high), 2)} / ${ratioPct(avg(low), 2)}`,
        note: `${high.length} 笔 / ${low.length} 笔`,
      })
    }
  }
  if (!perf) {
    rows.push({ key: 'attr', label: '分维贡献归因', value: `${GAP_PERF}（分维归因）`, tone: 'warn' })
  } else if (perf.attributions.length === 0) {
    rows.push({ key: 'attr', label: '分维贡献归因', value: '还没有归因记录（M07 归因写入后才有）', tone: 'warn' })
  } else {
    rows.push({
      key: 'attr',
      label: '分维贡献归因',
      value: perf.attributions.map((a) => `${attributionLabel(a.attribution)} ×${a.count}`).join('，'),
      note: `近 ${perf.sample} 笔已后验`,
    })
  }
  return rows
}

/* ---------- §5.6 扫描牌 ---------- */

function scanSections(input: MonitorInput): DetailSection[] {
  const funnel = buildFunnel(input, missLabel)

  /* 六层 + 转化率 */
  const layerRows: DetailRow[] = funnel.layers.map((layer, index) => {
    const rate = funnelRate(layer.count, funnel.layers[index - 1]?.count ?? null)
    return {
      key: layer.key,
      label: layer.name,
      value: funnelCountText(layer.count),
      note: rate ? `转化率 ${rate}` : index > 0 ? '转化率未返回（前一层没有数字）' : '全市场数字只在扫描执行完的 ScanReply 里，只读拿不到',
    }
  })
  const layersSection: DetailSection = {
    key: 'layers',
    title: '六层漏斗',
    rows: layerRows,
    foot: funnel.note || '数字口径与画布光带同源（buildFunnel），绝不触发 POST /api/strategy/scan。',
  }

  /* 各层过滤原因分布：每层一个横条榜，按条数降序最多 6 项 */
  const reasonSections: DetailSection[] = funnel.leaks
    .filter((leak) => leak.reasons.length > 0)
    .map((leak) => ({
      key: `leak:${leak.at}`,
      title: `${funnel.layers[leak.at - 1]?.name ?? ''} → ${funnel.layers[leak.at]?.name ?? ''} 过滤原因`,
      bars: leak.reasons.slice(0, 6),
      foot: '按条数降序，最多 6 项（§5.6）。',
    }))

  /* 漏选清单 ≤8 */
  const candidates = input.candidates?.ok ? input.candidates.items : null
  const missed = (candidates ?? []).filter((item) => item.missReason)
  const missRows = missed.slice(0, 8).map((item) => [
    item.symbol,
    item.name || '名称未返回',
    strategyLabel(item.strategy),
    missLabel(item.missReason),
    item.finalScore == null ? '未返回' : String(item.finalScore),
  ])
  const missSection: DetailSection = {
    key: 'miss',
    title: '漏选清单',
    table: candidates
      ? { key: 'miss', columns: ['代码', '名称', '模板', '漏选原因', '最终分'], rows: missRows, empty: missed.length === 0 ? '没有漏选' : '' }
      : undefined,
    rows: candidates ? undefined : [{ key: 'm', label: '漏选', value: '候选未返回', note: 'GET /api/strategy/candidates 无应答' }],
    foot: missed.length > 8 ? `共 ${missed.length} 条，这里取前 8 条。` : '',
  }

  /* 盘后漏选复盘：只做事实陈述，不做「本该赚多少」的假设收益 */
  const limits = input.limits?.ok ? input.limits.rows : null
  const missedSymbols = new Set(missed.map((item) => item.symbol))
  const limitHits = limits ? limits.filter((row) => missedSymbols.has(row.symbol)) : []
  const retRows = missed.filter((item) => item.hasT1 && item.retT1 != null)
  const bestT1 = retRows.reduce<{ symbol: string; ret: number } | null>((top, item) => {
    if (item.retT1 == null) return top
    return !top || item.retT1 > top.ret ? { symbol: item.symbol, ret: item.retT1 } : top
  }, null)
  const reviewRows: DetailRow[] = []
  if (candidates) {
    reviewRows.push({
      key: 'limit',
      label: '漏选名单里今日涨停',
      value: limits ? `${limitHits.length} 只` : '涨停接口未返回',
      note: limitHits.length > 0 ? limitHits.map((row) => row.symbol).join('、') : '',
    })
    reviewRows.push({
      key: 'best',
      label: '漏选后 T+1 最高涨幅',
      value: retRows.length === 0 ? '还没有 T+1 行情' : bestT1 ? `${bestT1.symbol} ${ratioPct(bestT1.ret, 1)}` : '未返回',
      note: retRows.length > 0 ? `按 ${retRows.length} 只已有 T+1 行情的漏选股（候选 ret_t1 原值）` : '候选的 T+1/T+3/T+5 是该漏选股此后的实际涨跌，不是策略收益',
    })
  } else {
    reviewRows.push({ key: 'review', label: '复盘', value: '候选未返回' })
  }
  const reviewSection: DetailSection = {
    key: 'review',
    title: '盘后漏选复盘',
    rows: reviewRows,
    foot: '只做事实陈述，不做「本该赚多少」的假设收益——策略没入场，没有成交价（§5.6）。',
  }

  return [layersSection, ...reasonSections, missSection, reviewSection]
}
