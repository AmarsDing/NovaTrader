import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'

/* ---------- BE-10 三大指数快照（上证/深成/创指，实时快照优先、缺当日回退 1d 收盘） ---------- */

export interface IndexQuoteView {
  code: string
  name: string
  last: number
  chg: number
  pctChg: number
  amplitude: number
  asOf: string // RFC3339，缺失为空串
  source: string // snap / 1d / ''
  stale: boolean
  found: boolean
}

export interface IndexSnapshot {
  asOf: string
  indices: IndexQuoteView[]
}

export async function getIndexSnapshot(): Promise<IndexSnapshot> {
  const data = await request('/api/market/index-snapshot')
  const raw = (data ?? {}) as RawRecord
  return {
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
    indices: asList<RawRecord>(raw, 'indices').map((item) => ({
      code: pick<string>(item, 'code') || '',
      name: pick<string>(item, 'name') || '',
      last: num(pick(item, 'last')) ?? 0,
      chg: num(pick(item, 'chg')) ?? 0,
      pctChg: num(pick(item, 'pctChg', 'pct_chg')) ?? 0,
      amplitude: num(pick(item, 'amplitude')) ?? 0,
      asOf: pick<string>(item, 'asOf', 'as_of') || '',
      source: pick<string>(item, 'source') || '',
      stale: !!item.stale,
      found: !!item.found,
    })),
  }
}
