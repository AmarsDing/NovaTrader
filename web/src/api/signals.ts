import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { SignalItem } from './types/strategy'

export type { SignalItem }

export async function listSignals(): Promise<SignalItem[]> {
  const data = await request('/api/strategy/signals', { query: { limit: 50 } })
  return asList(data, 'items', 'signals').map(normalizeSignal)
}

function normalizeSignal(raw: RawRecord): SignalItem {
  const finalScore = num(pick(raw, 'finalScore', 'final_score'))
  return {
    id: pick<string>(raw, 'id') || '',
    symbol: pick<string>(raw, 'symbol') || '',
    name: pick<string>(raw, 'name') || '',
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
    highValue: !!(raw.highValue || raw.high_value) || (finalScore ?? 0) >= 85,
    evidence: Array.isArray(raw.evidence) ? raw.evidence.map(String) : [],
    reason: pick<string>(raw, 'reason') || '',
    status: pick<string>(raw, 'status') || '',
    dims: raw.dims && typeof raw.dims === 'object' ? (raw.dims as Record<string, unknown>) : {},
    time: pick<string>(raw, 'signalTime', 'signal_time') || '',
  }
}
