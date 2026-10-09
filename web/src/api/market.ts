import { request } from '@/api/http'
import { asList, num, pick } from '@/utils/format'
import type { Bar, MarketIndicator, SectorRow, SentimentView } from './types/market'

export type { Bar, MarketIndicator, SectorRow, SentimentView }

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
