/** M02 行情与因子。经 admin 代理 /api/market/*。 */

export interface Bar {
  /** 原始时刻串；展示用 chartTime/format* 归一化。 */
  time: string
  open: number | null
  high: number | null
  low: number | null
  close: number | null
  volume: number | null
  amount?: number | null
  adjFactor?: number | null
}

export interface IndicatorPoint {
  time: string
  values: Record<string, number | null>
}

export type Adjust = 'NONE' | 'QFQ' | 'HFQ'

export interface Sentiment {
  tradeDate: string
  asOf: string
  upCount: number | null
  downCount: number | null
  brokenCount: number | null
  brokenRate: number | null
  maxHeight: number | null
  profitEffect: number | null
  advance: number | null
  decline: number | null
  flat: number | null
  amount: number | null
  /** ICE / RECOVER / WARM / HOT / FADE */
  phase: string
  scoreCoef: number | null
  positionScale: number | null
  stale: boolean
}

export interface LimitBoard {
  symbol: string
  tradeDate: string
  /** UP / DOWN */
  direction: string
  /** SEALED / BROKEN */
  status: string
  limitPrice: number | null
  firstSealAt: string
  lastSealAt: string
  openCount: number | null
  sealAmount: number | null
  consecutive: number | null
  asOf: string
}

export interface SectorHeat {
  sectorCode: string
  name: string
  rank: number | null
  heat: number | null
  avgPct: number | null
  upLimitCount: number | null
  advanceRatio: number | null
  amount: number | null
  memberCount: number | null
  leaderSymbol: string
  leaderReason: string
  overseasImpulse: number | null
  asOf: string
}

/* ---------- 以下为归一化后的展示态 DTO（组件只见这些） ---------- */

/** getIndicators 返回的扁平指标点。 */
export interface MarketIndicator {
  time: string
  ma5: number | null
  ma10: number | null
  ma20: number | null
  dif: number | null
  dea: number | null
  macd: number | null
  rsi6: number | null
}

/** getSentiment 归一化结果。 */
export interface SentimentView {
  phase: string
  positionScale: number | null
  up: number | null
  down: number | null
  brokenRate: number | null
  stale: boolean
  asOf: string
}

/** listSectors 归一化结果。 */
export interface SectorRow {
  code: string
  name: string
  rank: number | null
  heat: number | null
  avgPct: number | null
  overseas: number | null
  leader: string
  asOf: string
}

export interface FactorRow {
  symbol: string
  asOf: string
  /** close / intraday / auction / capital */
  kind: string
  values: Record<string, number | null>
  stale: boolean
}
