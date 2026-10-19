import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { AccountSnapshot, Fill, Order, OrderDraft, Position } from './types/trade'

export type { AccountSnapshot, Fill, Order, OrderDraft, Position }

export async function getAccount(account: string): Promise<AccountSnapshot> {
  const raw = await request('/api/trade/account', { query: { account } })
  return {
    account: pick<string>(raw, 'account') || account,
    cash: num(pick(raw, 'cash')),
    equity: num(pick(raw, 'equity')),
    realized: num(pick(raw, 'realizedPnl', 'realized_pnl')),
    unrealized: num(pick(raw, 'unrealizedPnl', 'unrealized_pnl')),
    dayPnl: num(pick(raw, 'dayPnl', 'day_pnl')),
    paperDays: num(pick(raw, 'paperDays', 'paper_days')),
    openHalted: !!pick(raw, 'openHalted', 'open_halted'),
    haltReason: pick<string>(raw, 'haltReason', 'halt_reason') || '',
  }
}

export async function listPositions(account: string): Promise<Position[]> {
  const data = await request('/api/trade/positions', { query: { account } })
  return asList(data, 'positions').map((raw) => ({
    symbol: pick<string>(raw, 'symbol') || '',
    quantity: num(pick(raw, 'quantity')),
    available: num(pick(raw, 'available')),
    avgCost: num(pick(raw, 'avgCost', 'avg_cost')),
    lastPrice: num(pick(raw, 'lastPrice', 'last_price')),
  }))
}

export async function listOrders(account: string): Promise<Order[]> {
  const data = await request('/api/trade/orders', { query: { account } })
  return asList(data, 'orders', 'items').map(normalizeOrder)
}

export async function getOrder(clientOrderId: string): Promise<Order> {
  return normalizeOrder(await request(`/api/trade/orders/${encodeURIComponent(clientOrderId)}`))
}

export async function listFills(account: string): Promise<Fill[]> {
  const data = await request('/api/trade/fills', { query: { account } })
  return asList(data, 'fills', 'items').map((raw) => ({
    id: pick<string>(raw, 'clientOrderId', 'client_order_id') || '',
    symbol: pick<string>(raw, 'symbol') || '',
    side: pick<string>(raw, 'side') || '',
    qty: num(pick(raw, 'qty', 'quantity', 'filled')),
    price: num(pick(raw, 'price')),
    amount: num(pick(raw, 'amount')),
    time: pick<string>(raw, 'time', 'filledAt', 'filled_at') || '',
  }))
}

export interface ChannelStatus {
  channel: string
  status: string
  since: string
}

// G7 执行通道状态读面。服务端只回真实跟踪的通道：今日只有 sim；
// 空清单 = 服务端未跟踪任何通道（与「读取失败」一样按状态未知呈现）。
export async function getChannelStatus(): Promise<ChannelStatus[]> {
  const data = await request('/api/trade/channel/status')
  return asList(data, 'channels').map((raw) => ({
    channel: pick<string>(raw, 'channel') || '',
    status: pick<string>(raw, 'status') || '',
    since: pick<string>(raw, 'since') || '',
  }))
}

export async function placeOrder(body: OrderDraft): Promise<Order> {
  return normalizeOrder(await request('/api/trade/orders', { method: 'POST', json: body }))
}

export function cancelOrder(clientOrderId: string): Promise<unknown> {
  return request(`/api/trade/orders/${encodeURIComponent(clientOrderId)}/cancel`, { method: 'POST', json: {} })
}

export function confirmOrder(clientOrderId: string): Promise<unknown> {
  return request(`/api/trade/orders/${encodeURIComponent(clientOrderId)}/confirm`, { method: 'POST', json: {} })
}

function normalizeOrder(raw: RawRecord): Order {
  return {
    id: pick<string>(raw, 'clientOrderId', 'client_order_id') || '',
    account: pick<string>(raw, 'account') || '',
    symbol: pick<string>(raw, 'symbol') || '',
    side: pick<string>(raw, 'side') || '',
    status: pick<string>(raw, 'status') || '',
    price: num(pick(raw, 'price')),
    volume: num(pick(raw, 'volume')),
    filled: num(pick(raw, 'filled')),
    reason: pick<string>(raw, 'reason') || '',
    signalId: String(pick(raw, 'signalId', 'signal_id') ?? ''),
    strategyVersion: String(pick(raw, 'strategyVersion', 'strategy_version') ?? ''),
  }
}
