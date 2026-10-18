/**
 * position 主题账户快照推送解析（N1-02 实体增量）。
 * 后端 SubjectTradeAccount 载荷 = pkg/accountstate.Snapshot（完整账户态），
 * 信封带 entity_id（SIM:default / LIVE:default）与 entity_version（= generation）。
 * 只有完整、自洽、对得上当前账本的帧才返回增量；其余一律 null，
 * 由调用方回退 REST 权威刷新——提示帧、半个快照、别的账户的帧都不许当真值。
 */
import type { WsEventMetadata } from '@/ws/topics'
import type { Position } from './types/trade'

export interface AccountPushState {
  /** 归一化账本（SIM / LIVE）。 */
  book: string
  /** 实体版本 = 载荷 generation（后端实现里二者同源，不一致即不可信）。 */
  generation: number
  /** 账户态时刻（服务端 as_of 原样保留）。 */
  asOf: string
  equity: number
  /** 账本现金：与 REST Account.cash 同一口径（accountstate.Cash = 视图 Cash）。 */
  cash: number
  /** 浮动盈亏：Σ(市值 − 成本×数量)，与后端 AccountView 同口径推导。 */
  unrealized: number
  /** 当日盈亏：day_start 已知才给出（equity − 日初权益）；未知为 null，不算成零。 */
  dayPnl: number | null
  positions: Position[]
}

function finite(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

export function parseAccountPush(payload: unknown, metadata: WsEventMetadata | undefined): AccountPushState | null {
  if (!payload || typeof payload !== 'object') return null
  const body = payload as Record<string, unknown>
  if (body.version !== 1 || body.kind !== 'full') return null
  const book = typeof body.account_type === 'string' ? body.account_type.toUpperCase() : ''
  if (book !== 'SIM' && book !== 'LIVE') return null
  if (body.account_id !== 'default') return null
  const version = metadata?.entity_version
  if (typeof version !== 'number' || !Number.isSafeInteger(version) || version <= 0) return null
  if (!Number.isSafeInteger(body.generation) || body.generation !== version) return null
  const asOf = typeof body.as_of === 'string' ? body.as_of : ''
  const asOfMs = Date.parse(asOf)
  // 镜像后端 Fresh()：账户态不允许来自未来（容忍 5s 时钟偏差）。
  if (!asOf || !Number.isFinite(asOfMs) || asOfMs > Date.now() + 5000) return null
  const equity = finite(body.equity) && body.equity >= 0 ? body.equity : null
  const cash = finite(body.cash) && body.cash >= 0 ? body.cash : null
  if (equity == null || cash == null || !Array.isArray(body.holdings)) return null
  const positions: Position[] = []
  let unrealized = 0
  for (const row of body.holdings) {
    if (!row || typeof row !== 'object') return null
    const h = row as Record<string, unknown>
    const symbol = typeof h.symbol === 'string' ? h.symbol : ''
    if (!symbol) return null
    if (!finite(h.quantity) || h.quantity <= 0) return null
    if (!finite(h.available) || h.available < 0 || h.available > h.quantity) return null
    if (!finite(h.avg_cost) || h.avg_cost <= 0) return null
    if (!finite(h.market_value) || h.market_value <= 0) return null
    unrealized += h.market_value - h.avg_cost * h.quantity
    positions.push({
      symbol,
      quantity: h.quantity,
      available: h.available,
      avgCost: h.avg_cost,
      // 估值价 = 市值 / 数量：与后端用同一份估值市值，不引入第二来源。
      lastPrice: h.market_value / h.quantity,
    })
  }
  const dayStart = finite(body.day_start_equity) ? body.day_start_equity : 0
  const dayPnl = body.day_start_known === true && dayStart > 0 ? equity - dayStart : null
  return { book, generation: version, asOf, equity, cash, unrealized, dayPnl, positions }
}
