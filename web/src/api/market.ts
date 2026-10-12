import { request } from '@/api/http'
import { asList, num, pick } from '@/utils/format'
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
