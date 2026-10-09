import { request } from '@/api/http'
import { num, pick, type RawRecord } from '@/utils/format'

export interface RiskStateView {
  mode: string
  killActive: boolean
  killSource: string
  killReason: string
  killAt: string
  params: Record<string, unknown>
  buysToday: number | null
  marketBreaker: string
}

export interface KillBody {
  reason?: string
  source?: string
  operator?: string
  [key: string]: unknown
}

export interface ModeBody {
  mode: string
  operator?: string
  [key: string]: unknown
}

export interface ParamBody {
  key: string
  value: unknown
  operator?: string
  [key: string]: unknown
}

export async function getRiskState(): Promise<RiskStateView> {
  const raw = await request('/api/risk/state')
  const record = (raw && typeof raw === 'object' ? raw : {}) as RawRecord
  const kill = (record.killSwitch || record.kill_switch || {}) as RawRecord
  return {
    mode: pick<string>(record, 'mode') || '',
    killActive: !!kill.active,
    killSource: pick<string>(kill, 'source') || '',
    killReason: pick<string>(kill, 'reason') || '',
    killAt: pick<string>(kill, 'at') || '',
    params: record.params && typeof record.params === 'object' ? (record.params as RawRecord) : {},
    buysToday: num(pick(record, 'buysToday', 'buys_today')),
    marketBreaker: pick<string>(record, 'marketBreaker', 'market_breaker') || '',
  }
}

export function triggerKillSwitch(body: KillBody): Promise<unknown> {
  return request('/api/risk/killswitch', { method: 'POST', json: body })
}

export function resetKillSwitch(body: KillBody): Promise<unknown> {
  return request('/api/risk/killswitch/reset', { method: 'POST', json: body })
}

export function setMode(body: ModeBody): Promise<unknown> {
  return request('/api/risk/mode', { method: 'POST', json: body })
}

export function updateParam(body: ParamBody): Promise<unknown> {
  return request('/api/risk/params', { method: 'POST', json: body })
}
