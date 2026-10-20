import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import { useSecurityStore } from '@/stores/security'
import type { SignalItem } from './types/strategy'

export type { SignalItem }

export async function listSignals(): Promise<SignalItem[]> {
  const data = await request('/api/strategy/signals', { query: { limit: 50 } })
  return asList(data, 'items', 'signals').map(normalizeSignal)
}

export async function getSignal(id: string): Promise<SignalItem> {
  const raw = await request(`/api/strategy/signals/${encodeURIComponent(id)}`)
  return normalizeSignal((raw && typeof raw === 'object' ? raw : {}) as RawRecord)
}

export function normalizeSignal(raw: RawRecord): SignalItem {
  const finalScore = num(pick(raw, 'finalScore', 'final_score'))
  const pnlPct = num(pick(raw, 'pnlPct', 'pnl_pct'))
  const pnlFlag = pick(raw, 'hasPnl', 'has_pnl')
  const executedPrice = num(pick(raw, 'executedPrice', 'executed_price'))
  const executedVolume = num(pick(raw, 'executedVolume', 'executed_volume'))
  // proto 未设置的 double 可能序列化为 0；旧接口的真实零收益必须显式声明。
  const hasPnl =
    typeof pnlFlag === 'boolean'
      ? pnlFlag
      : String(raw.status || '').toLowerCase() === 'closed' && pnlPct !== null && pnlPct !== 0
  const symbol = pick<string>(raw, 'symbol') || ''
  const name = pick<string>(raw, 'name') || ''
  // 附录 B-5 回填：信号行的 symbol/name 顺手进名称字典（put 判空码、null 名不覆盖旧名）。
  useSecurityStore().put([{ code: symbol, name: name || null }])
  return {
    id: String(pick(raw, 'id') ?? ''),
    executedPrice: executedPrice != null && executedPrice > 0 ? executedPrice : null,
    executedVolume: executedVolume != null && Number.isSafeInteger(executedVolume) && executedVolume >= 0 ? executedVolume : null,
    book: String(pick(raw, 'book') ?? ''),
    strategyVersionId: String(pick(raw, 'strategyVersionId', 'strategy_version_id') ?? ''),
    symbol,
    name,
    side: pick<string>(raw, 'side') || '',
    strategy: pick<string>(raw, 'strategy') || '',
    entry: num(pick(raw, 'entry')),
    entryLow: num(pick(raw, 'entryLow', 'entry_low')),
    entryHigh: num(pick(raw, 'entryHigh', 'entry_high')),
    stop: num(pick(raw, 'stopLoss', 'stop_loss')),
    take: num(pick(raw, 'takeProfit', 'take_profit')),
    score: finalScore,
    ruleScore: num(pick(raw, 'ruleScore', 'rule_score')),
    aiScore: num(pick(raw, 'aiScore', 'ai_score')),
    degraded: !!(raw.aiDegraded || raw.ai_degraded),
    // high_value 以后端扫描期落库为唯一权威（模型降级→HighValue=false，biz_test.go 钉死）；
    // 不得用 finalScore>=85 前端回退复活降级高分信号（P3-10c，原回退与降级口径冲突已删）。
    highValue: !!(raw.highValue || raw.high_value),
    evidence: Array.isArray(raw.evidence) ? raw.evidence.map(String) : [],
    reason: pick<string>(raw, 'reason') || '',
    status: pick<string>(raw, 'status') || '',
    dims: raw.dims && typeof raw.dims === 'object' ? (raw.dims as Record<string, unknown>) : {},
    time: pick<string>(raw, 'signalTime', 'signal_time') || '',
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    atr: num(pick(raw, 'atr')),
    positionPct: num(pick(raw, 'positionPct', 'position_pct')),
    validUntil: pick<string>(raw, 'validUntil', 'valid_until') || '',
    holdDaysMax: num(pick(raw, 'holdDaysMax', 'hold_days_max')),
    exitKind: pick<string>(raw, 'exitKind', 'exit_kind') || '',
    exitPrice: num(pick(raw, 'exitPrice', 'exit_price')),
    pnlPct: hasPnl ? pnlPct : null,
    hasPnl: hasPnl && pnlPct !== null,
    holdingDays: num(pick(raw, 'holdingDays', 'holding_days')),
    traceId: pick<string>(raw, 'traceId', 'trace_id') || '',
    closedAt: pick<string>(raw, 'closedAt', 'closed_at') || '',
  }
}
