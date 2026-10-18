import type { Book } from '@/api/types/trade'

/** 仅承载信号身份与展示依据；不是已通过风控的订单。 */
export interface SignalDraftContext {
  signalId: string
  strategyVersionId: string
  book: Book | null
  symbol: string
  side: string
  validUntil: string
}

export function normalizeBook(value: unknown): Book | null {
  const text = String(value ?? '').trim().toUpperCase()
  if (text === 'SIM' || text === 'PAPER') return 'SIM'
  if (text === 'LIVE') return 'LIVE'
  return null
}

export function draftPrice(value: unknown): string {
  if (typeof value !== 'number' && typeof value !== 'string') return ''
  if (typeof value === 'string' && !value.trim()) return ''
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? String(parsed) : ''
}

export function signalContextOf(params: Record<string, unknown>): SignalDraftContext | null {
  if (!Object.prototype.hasOwnProperty.call(params, 'signalId')) return null
  return {
    signalId: String(params.signalId ?? '').trim(),
    strategyVersionId: String(params.strategyVersionId ?? '').trim(),
    book: normalizeBook(params.book),
    symbol: String(params.symbol ?? '').trim().toUpperCase(),
    side: String(params.side ?? ''),
    validUntil: String(params.validUntil ?? ''),
  }
}

export function signalDraftBlock(
  context: SignalDraftContext | null,
  draft: { account: string; symbol: string; side: string },
  now: number,
): string {
  if (!context) return ''
  if (!context.signalId) return '信号编号缺失，请回到决策台重新选择'
  if (!context.strategyVersionId || context.strategyVersionId === '0') return '信号策略版本未归档，暂不能按此计划提交'
  if (!context.book) return '信号账户未返回，不能默认转为模拟盘'
  if (context.book !== normalizeBook(draft.account)) return '当前账户与信号不一致，请回到原账户处理'
  if (context.symbol !== draft.symbol.trim().toUpperCase() || context.side !== draft.side) return '表单已不属于这条信号，请重新选择'
  const expires = Date.parse(context.validUntil)
  if (!Number.isFinite(expires)) return '信号有效期未返回，暂不能提交'
  if (now >= expires) return '信号已过期，请回到决策台查看最新计划'
  return ''
}

export function pendingTradeKey(operator: string): string {
  return `nt.trade.pending.v1.${encodeURIComponent(operator)}`
}

/** 写入发生在POST之前，刷新/关面板后仍能按原编号核对。 */
export function readPendingTrade(storage: Pick<Storage, 'getItem'>, key: string): import('@/api/types/trade').OrderDraft | null {
  const raw = storage.getItem(key)
  if (!raw) return null
  const value = JSON.parse(raw) as Record<string, unknown>
  if (!value || typeof value !== 'object' || typeof value.clientOrderId !== 'string' || !value.clientOrderId || !normalizeBook(value.account) || typeof value.symbol !== 'string' || !['buy', 'sell'].includes(String(value.side)) || !draftPrice(value.price) || !Number.isInteger(value.volume) || Number(value.volume) <= 0 || typeof value.operator !== 'string') {
    throw new Error('待核对委托记录不完整，请核对原委托后处理')
  }
  return value as unknown as import('@/api/types/trade').OrderDraft
}
