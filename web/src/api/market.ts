import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { Bar, LimitBoard, MarketIndicator, SectorRow, Sentiment, SentimentView } from './types/market'

export type { Bar, LimitBoard, MarketIndicator, SectorRow, Sentiment, SentimentView }

export async function getBars(symbol: string, freq: string): Promise<Bar[]> {
  const data = await request('/api/market/bars', {
    query: { symbol, freq, limit: freq === '1m' ? 240 : 180, adjust: 'NONE' },
  })
  return asList(data, 'bars').map((raw) => ({
    time: pick<string>(raw, 'time', 'barTime', 'bar_time') || '',
    open: num(pick(raw, 'open')),
    high: num(pick(raw, 'high')),
    low: num(pick(raw, 'low')),
    close: num(pick(raw, 'close')),
    volume: num(pick(raw, 'volume')),
    amount: num(pick(raw, 'amount')),
  }))
}

export async function getIndicators(symbol: string): Promise<MarketIndicator[]> {
  const data = await request('/api/market/indicators', {
    query: { symbol, adjust: 'NONE' },
  })
  return asList(data, 'points').map((raw) => {
    const values = raw.values && typeof raw.values === 'object' ? raw.values : raw
    return {
      time: pick<string>(raw, 'time') || '',
      ma5: num(pick(values, 'ma5')),
      ma10: num(pick(values, 'ma10')),
      ma20: num(pick(values, 'ma20')),
      dif: num(pick(values, 'dif')),
      dea: num(pick(values, 'dea')),
      macd: num(pick(values, 'macd')),
      rsi6: num(pick(values, 'rsi6')),
    }
  })
}

export interface FactorPoint {
  symbol: string
  kind: string
  stale: boolean
  values: { name: string; value: number | null }[]
}

/** 因子截面。kind=capital 时只取资金因子。 */
export async function getFactors(symbol: string, kind = 'capital'): Promise<FactorPoint[]> {
  const data = await request('/api/market/factors', { query: { symbols: symbol, kind } })
  return asList(data, 'rows').map((raw) => {
    const values = raw.values && typeof raw.values === 'object' ? (raw.values as Record<string, unknown>) : {}
    return {
      symbol: pick<string>(raw, 'symbol') || symbol,
      kind: pick<string>(raw, 'kind') || kind,
      stale: !!raw.stale,
      values: Object.entries(values).map(([name, value]) => ({ name, value: num(value) })),
    }
  })
}

export async function getSentiment(): Promise<SentimentView> {
  const raw = await request('/api/market/sentiment')
  return {
    phase: pick<string>(raw, 'phase') || '',
    positionScale: num(pick(raw, 'positionScale', 'position_scale')),
    up: num(pick(raw, 'upCount', 'up_count')),
    down: num(pick(raw, 'downCount', 'down_count')),
    brokenRate: num(pick(raw, 'brokenRate', 'broken_rate')),
    stale: !!(raw && typeof raw === 'object' && (raw as Record<string, unknown>).stale),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

export async function listSectors(top = 8): Promise<SectorRow[]> {
  const data = await request('/api/market/sectors', { query: { top } })
  return asList(data, 'sectors').map((raw) => ({
    code: pick<string>(raw, 'sectorCode', 'sector_code') || '',
    name: pick<string>(raw, 'name') || pick<string>(raw, 'sectorCode', 'sector_code') || '',
    rank: num(pick(raw, 'rank')),
    heat: num(pick(raw, 'heat')),
    avgPct: num(pick(raw, 'avgPct', 'avg_pct')),
    overseas: num(pick(raw, 'overseasImpulse', 'overseas_impulse')),
    leader: pick<string>(raw, 'leaderSymbol', 'leader_symbol') || '',
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }))
}

/** 情绪快照全量（M02 §6）：五阶段 + 计数 + 系数 + 风险等级。 */
export async function getSentimentDetail(): Promise<Sentiment> {
  const data = await request('/api/market/sentiment')
  const raw = (data && typeof data === 'object' ? data : {}) as Record<string, unknown>
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
    upCount: num(pick(raw, 'upCount', 'up_count')),
    downCount: num(pick(raw, 'downCount', 'down_count')),
    brokenCount: num(pick(raw, 'brokenCount', 'broken_count')),
    brokenRate: num(pick(raw, 'brokenRate', 'broken_rate')),
    maxHeight: num(pick(raw, 'maxHeight', 'max_height')),
    profitEffect: num(pick(raw, 'profitEffect', 'profit_effect')),
    advance: num(pick(raw, 'advance')),
    decline: num(pick(raw, 'decline')),
    flat: num(pick(raw, 'flat')),
    amount: num(pick(raw, 'amount')),
    phase: pick<string>(raw, 'phase') || '',
    riskLevel: num(pick(raw, 'riskLevel', 'risk_level')),
    scoreCoef: num(pick(raw, 'scoreCoef', 'score_coef')),
    positionScale: num(pick(raw, 'positionScale', 'position_scale')),
    stale: !!raw.stale,
  }
}

/** 涨跌停列表（M02 §5）：direction = UP / DOWN。 */
export async function listLimits(direction: 'UP' | 'DOWN' = 'UP'): Promise<LimitBoard[]> {
  const data = await request('/api/market/limits', { query: { direction } })
  return asList(data, 'boards', 'limits', 'items').map((raw) => ({
    symbol: pick<string>(raw, 'symbol') || '',
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    direction: pick<string>(raw, 'direction') || direction,
    status: pick<string>(raw, 'status') || '',
    limitPrice: num(pick(raw, 'limitPrice', 'limit_price')),
    firstSealAt: pick<string>(raw, 'firstSealAt', 'first_seal_at') || '',
    lastSealAt: pick<string>(raw, 'lastSealAt', 'last_seal_at') || '',
    openCount: num(pick(raw, 'openCount', 'open_count')),
    sealAmount: num(pick(raw, 'sealAmount', 'seal_amount')),
    consecutive: num(pick(raw, 'consecutive')),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }))
}

/* ---------- S7 P1：集合竞价截面与连板梯队（只读） ---------- */

export interface AuctionRow {
  symbol: string
  auctionPct: number
  auctionAmount: number
  auctionVolRatio: number
  auctionTurnover: number
  auctionLimitUp: boolean
  auctionUnmatched: number
}

export interface AuctionSummary {
  tradeDate: string
  found: boolean
  totalAmount: number
  limitUpCount: number
  limitUps: AuctionRow[]
  topRush: AuctionRow[]
  asOf: string
}

function normalizeAuctionRow(raw: RawRecord): AuctionRow {
  return {
    symbol: pick<string>(raw, 'symbol') || '',
    auctionPct: num(pick(raw, 'auctionPct', 'auction_pct')) ?? 0,
    auctionAmount: num(pick(raw, 'auctionAmount', 'auction_amount')) ?? 0,
    auctionVolRatio: num(pick(raw, 'auctionVolRatio', 'auction_vol_ratio')) ?? 0,
    auctionTurnover: num(pick(raw, 'auctionTurnover', 'auction_turnover')) ?? 0,
    auctionLimitUp: !!(raw.auctionLimitUp ?? raw.auction_limit_up),
    auctionUnmatched: num(pick(raw, 'auctionUnmatched', 'auction_unmatched')) ?? 0,
  }
}

/** 集合竞价截面（M02，09:25 落库）。found=false 表示当日还没有竞价因子。 */
export async function getAuction(top = 10): Promise<AuctionSummary> {
  const data = await request('/api/market/auction', { query: { top } })
  const raw = (data ?? {}) as RawRecord
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    totalAmount: num(pick(raw, 'totalAmount', 'total_amount')) ?? 0,
    limitUpCount: num(pick(raw, 'limitUpCount', 'limit_up_count')) ?? 0,
    limitUps: asList<RawRecord>(raw, 'limitUps', 'limit_ups').map(normalizeAuctionRow),
    topRush: asList<RawRecord>(raw, 'topRush', 'top_rush').map(normalizeAuctionRow),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

export interface LadderStep {
  fromHeight: number
  sample: number
  succeeded: number
  rate: number
}

export interface LadderDay {
  tradeDate: string
  steps: LadderStep[]
}

export interface LadderSummary {
  days: LadderDay[]
  total: LadderStep[]
}

function normalizeLadderStep(raw: RawRecord): LadderStep {
  return {
    fromHeight: num(pick(raw, 'fromHeight', 'from_height')) ?? 0,
    sample: num(pick(raw, 'sample')) ?? 0,
    succeeded: num(pick(raw, 'succeeded')) ?? 0,
    rate: num(pick(raw, 'rate')) ?? 0,
  }
}

/** 连板梯队晋级率（M02 跨日对比）。sample=0 的档由界面写「无样本」。 */
export async function getLadder(days = 5): Promise<LadderSummary> {
  const data = await request('/api/market/ladder', { query: { days } })
  const raw = (data ?? {}) as RawRecord
  return {
    days: asList<RawRecord>(raw, 'days').map((d) => ({
      tradeDate: pick<string>(d, 'tradeDate', 'trade_date') || '',
      steps: asList<RawRecord>(d, 'steps').map(normalizeLadderStep),
    })),
    total: asList<RawRecord>(raw, 'total').map(normalizeLadderStep),
  }
}

/* ---------- P2：K 线缺口 / 昨涨停今溢价 / 20 日均线占比（只读） ---------- */

export interface PremiumRow {
  symbol: string
  name: string
  tradeDate: string
  baseDate: string
  consecutive: number
  baseClose: number
  open: number
  close: number
  openPremium: number
  closePremium: number
  highPremium: number
  limitAgain: boolean
  hasClose: boolean
  source: string
}

export interface PremiumDay {
  tradeDate: string
  baseDate: string
  sample: number
  limitAgain: number
  openAvg: number
  closeAvg: number
  closeWin: number
  highAvg: number
  hasClose: boolean
}

export interface PremiumSummary {
  tradeDate: string
  found: boolean
  live: boolean
  summary: PremiumDay | null
  days: PremiumDay[]
  rows: PremiumRow[]
  asOf: string
}

function normalizePremiumDay(raw: RawRecord): PremiumDay {
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    baseDate: pick<string>(raw, 'baseDate', 'base_date') || '',
    sample: num(pick(raw, 'sample')) ?? 0,
    limitAgain: num(pick(raw, 'limitAgain', 'limit_again')) ?? 0,
    openAvg: num(pick(raw, 'openAvg', 'open_avg')) ?? 0,
    closeAvg: num(pick(raw, 'closeAvg', 'close_avg')) ?? 0,
    closeWin: num(pick(raw, 'closeWin', 'close_win')) ?? 0,
    highAvg: num(pick(raw, 'highAvg', 'high_avg')) ?? 0,
    hasClose: !!raw.hasClose,
  }
}

/** 昨日涨停今日溢价（P2 快照表；live=true 表示快照未落、实时兜底）。溢价率为分数（0.0323=3.23%）。 */
export async function getLimitPremium(days = 5, top = 50): Promise<PremiumSummary> {
  const data = await request('/api/market/limit-premium', { query: { days, top } })
  const raw = (data ?? {}) as RawRecord
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    live: !!raw.live,
    summary: raw.summary ? normalizePremiumDay(raw.summary as RawRecord) : null,
    days: asList<RawRecord>(raw, 'days').map(normalizePremiumDay),
    rows: asList<RawRecord>(raw, 'rows').map((r) => ({
      symbol: pick<string>(r, 'symbol') || '',
      name: pick<string>(r, 'name') || '',
      tradeDate: pick<string>(r, 'tradeDate', 'trade_date') || '',
      baseDate: pick<string>(r, 'baseDate', 'base_date') || '',
      consecutive: num(pick(r, 'consecutive')) ?? 0,
      baseClose: num(pick(r, 'baseClose', 'base_close')) ?? 0,
      open: num(pick(r, 'open')) ?? 0,
      close: num(pick(r, 'close')) ?? 0,
      openPremium: num(pick(r, 'openPremium', 'open_premium')) ?? 0,
      closePremium: num(pick(r, 'closePremium', 'close_premium')) ?? 0,
      highPremium: num(pick(r, 'highPremium', 'high_premium')) ?? 0,
      limitAgain: !!r.limitAgain,
      hasClose: !!r.hasClose,
      source: pick<string>(r, 'source') || '',
    })),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

export interface BreadthDay {
  tradeDate: string
  window: number
  above: number
  below: number
  flat: number
  total: number
  ratio: number
  live: boolean
  asOf: string
}

export interface BreadthSummary {
  tradeDate: string
  found: boolean
  latest: BreadthDay | null
  days: BreadthDay[]
}

/** 20 日均线上占比（市场广度）。ratio 为分数。 */
export async function getBreadth(days = 10): Promise<BreadthSummary> {
  const data = await request('/api/market/breadth', { query: { days } })
  const raw = (data ?? {}) as RawRecord
  const norm = (r: RawRecord): BreadthDay => ({
    tradeDate: pick<string>(r, 'tradeDate', 'trade_date') || '',
    window: num(pick(r, 'window')) ?? 20,
    above: num(pick(r, 'above')) ?? 0,
    below: num(pick(r, 'below')) ?? 0,
    flat: num(pick(r, 'flat')) ?? 0,
    total: num(pick(r, 'total')) ?? 0,
    ratio: num(pick(r, 'ratio')) ?? 0,
    live: !!r.live,
    asOf: pick<string>(r, 'asOf', 'as_of') || '',
  })
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    latest: raw.latest ? norm(raw.latest as RawRecord) : null,
    days: asList<RawRecord>(raw, 'days').map(norm),
  }
}

export interface KlineGapDay {
  tradeDate: string
  universe: number
  missing: number
  present: number
  sample: string[]
  asOf: string
}

export interface KlineGapsSummary {
  tradeDate: string
  found: boolean
  universe: number
  days: KlineGapDay[]
  missingTotal: number
  asOf: string
}

/** K 线连续性检查（交易日历开市但无 1d 收盘行的缺口日）。 */
export async function getKlineGaps(days = 5): Promise<KlineGapsSummary> {
  const data = await request('/api/market/kline-gaps', { query: { days } })
  const raw = (data ?? {}) as RawRecord
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    universe: num(pick(raw, 'universe')) ?? 0,
    days: asList<RawRecord>(raw, 'days').map((d) => ({
      tradeDate: pick<string>(d, 'tradeDate', 'trade_date') || '',
      universe: num(pick(d, 'universe')) ?? 0,
      missing: num(pick(d, 'missing')) ?? 0,
      present: num(pick(d, 'present')) ?? 0,
      sample: asList<string>(d, 'sample'),
      asOf: pick<string>(d, 'asOf', 'as_of') || '',
    })),
    missingTotal: num(pick(raw, 'missingTotal', 'missing_total')) ?? 0,
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

/* ---------- 视频落地 P0：单券快照（GetQuote，只读） ---------- */

export interface QuoteLevel {
  price: number
  volume: number
}

/** 单券快照：数值 0 与空串一律按「未知」处理（proto 零值不猜），source/stale/found 是口径依据。 */
export interface QuoteView {
  symbol: string
  name: string
  market: string
  tradeStatus: string
  last: number
  chg: number
  pctChg: number
  open: number
  high: number
  low: number
  prevClose: number
  volume: number
  amount: number
  turnoverRate: number
  amplitude: number
  bid: QuoteLevel[]
  ask: QuoteLevel[]
  peTtm: number
  pb: number
  circMv: number
  totalMv: number
  limitUp: number
  limitDown: number
  noLimit: boolean
  /** snap=盘中快照；1d=最近日线回退；空=无数据。 */
  source: string
  asOf: string
  stale: boolean
  found: boolean
}

function quoteLevels(list: QuoteLevel[]): QuoteLevel[] {
  return list.filter((l) => l.price > 0)
}

export async function getQuote(symbol: string): Promise<QuoteView> {
  const data = await request('/api/market/quote', { query: { symbol } })
  const raw = (data ?? {}) as RawRecord
  return {
    symbol: pick<string>(raw, 'symbol') || symbol,
    name: pick<string>(raw, 'name') || '',
    market: pick<string>(raw, 'market') || '',
    tradeStatus: pick<string>(raw, 'tradeStatus', 'trade_status') || '',
    last: num(pick(raw, 'last')) ?? 0,
    chg: num(pick(raw, 'chg')) ?? 0,
    pctChg: num(pick(raw, 'pctChg', 'pct_chg')) ?? 0,
    open: num(pick(raw, 'open')) ?? 0,
    high: num(pick(raw, 'high')) ?? 0,
    low: num(pick(raw, 'low')) ?? 0,
    prevClose: num(pick(raw, 'prevClose', 'prev_close')) ?? 0,
    volume: num(pick(raw, 'volume')) ?? 0,
    amount: num(pick(raw, 'amount')) ?? 0,
    turnoverRate: num(pick(raw, 'turnoverRate', 'turnover_rate')) ?? 0,
    amplitude: num(pick(raw, 'amplitude')) ?? 0,
    bid: quoteLevels(asList<RawRecord>(raw, 'bid').map((l) => ({
      price: num(pick(l, 'price')) ?? 0,
      volume: num(pick(l, 'volume')) ?? 0,
    }))),
    ask: quoteLevels(asList<RawRecord>(raw, 'ask').map((l) => ({
      price: num(pick(l, 'price')) ?? 0,
      volume: num(pick(l, 'volume')) ?? 0,
    }))),
    peTtm: num(pick(raw, 'peTtm', 'pe_ttm')) ?? 0,
    pb: num(pick(raw, 'pb')) ?? 0,
    circMv: num(pick(raw, 'circMv', 'circ_mv')) ?? 0,
    totalMv: num(pick(raw, 'totalMv', 'total_mv')) ?? 0,
    limitUp: num(pick(raw, 'limitUpPrice', 'limit_up_price')) ?? 0,
    limitDown: num(pick(raw, 'limitDownPrice', 'limit_down_price')) ?? 0,
    noLimit: !!(raw.noLimit ?? raw.no_limit),
    source: pick<string>(raw, 'source') || '',
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
    stale: !!raw.stale,
    found: !!raw.found,
  }
}
