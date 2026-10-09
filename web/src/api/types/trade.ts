/** M09 交易执行。经 admin 代理 /api/trade/*。SIM 模拟 / LIVE 实盘严格分开。 */

export type Book = 'SIM' | 'LIVE'

export interface AccountSnapshot {
  account: string
  cash: number | null
  equity: number | null
  realized: number | null
  unrealized: number | null
  dayPnl: number | null
  paperDays: number | null
  openHalted: boolean
  haltReason: string
}

export interface Position {
  symbol: string
  quantity: number | null
  available: number | null
  avgCost: number | null
  lastPrice: number | null
}

export type OrderSide = 'buy' | 'sell' | string

export interface Order {
  id: string
  account: string
  symbol: string
  side: OrderSide
  status: string
  price: number | null
  volume: number | null
  filled: number | null
  reason: string
}

export interface Fill {
  id: string
  symbol: string
  side: OrderSide
  qty: number | null
  price: number | null
  amount: number | null
  time: string
}

export interface OrderDraft {
  clientOrderId: string
  account: Book | string
  symbol: string
  side: OrderSide
  price: number
  volume: number
  source: string
  operator: string
}
