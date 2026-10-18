import { request } from './http'

export interface ConditionalPlan {
  version: number
  plan_id: string
  revision: number
  snapshot_id: string
  signal_id: number
  account_type: string
  account_id: string
  symbol: string
  side: string
  price_origin: string
  entry_low: number
  entry_high: number
  limit_price: number
  stop_loss: number
  take_profit: number
  max_notional: number
  valid_from: string
  valid_until: string
  execution_day: string
  summary: string
  invalidation: string[]
}
export interface PlanRecord {
  plan: ConditionalPlan
  snapshot: {
    version: number
    snapshot_id: string
    config_revision: number
    strategy_revision: number
    account_generation: number
    formed_at: string
    source_time: string
    evidence: string[]
    counter_evidence: string[]
    unknowns: string[]
    reason: string
  }
  state: string
  mode: string
  operator: string
  reason: string
}
export interface ExecutionIntent {
  business_key: string
  client_order_id: string
  request_hash: string
  book: string
  plan_id: string
  plan_revision: number
  state: string
  reason: string
}
function obj(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('计划合同未返回')
  return value as Record<string, unknown>
}
export function parsePlan(value: unknown): PlanRecord {
  const r = obj(value),
    p = obj(r.plan),
    s = obj(r.snapshot)
  if (
    p.version !== 1 ||
    s.version !== 1 ||
    typeof p.plan_id !== 'string' ||
    !p.plan_id ||
    !Number.isInteger(p.revision) ||
    Number(p.revision) < 1 ||
    p.snapshot_id !== s.snapshot_id ||
    typeof p.symbol !== 'string' ||
    !['buy', 'sell'].includes(String(p.side)) ||
    p.price_origin !== 'rules' ||
    !['SIM', 'LIVE'].includes(String(p.account_type)) ||
    p.account_id !== 'default' ||
    !Number.isFinite(Date.parse(String(p.valid_until))) ||
    typeof r.state !== 'string'
  )
    throw new Error('计划版本或身份无效')
  for (const key of ['entry_low', 'entry_high', 'limit_price', 'stop_loss', 'take_profit', 'max_notional']) {
    if (typeof p[key] !== 'number' || !Number.isFinite(p[key]) || Number(p[key]) < 0)
      throw new Error('计划价格或预算缺失')
  }
  for (const key of ['evidence', 'counter_evidence', 'unknowns']) {
    if (!Array.isArray(s[key]) || !(s[key] as unknown[]).every((v) => typeof v === 'string'))
      throw new Error('计划证据合同不完整')
  }
  if (!Array.isArray(p.invalidation)) throw new Error('计划失效条件缺失')
  return r as unknown as PlanRecord
}
export async function listPlans(account: string): Promise<PlanRecord[]> {
  const raw = obj(await request('/api/trade/plans', { query: { account } }))
  if (!Array.isArray(raw.plans)) throw new Error('计划列表未返回')
  return raw.plans.map(parsePlan)
}
export async function createPlan(signalId: number, account: string, old?: PlanRecord): Promise<PlanRecord> {
  const raw = obj(
    await request('/api/trade/plans', {
      method: 'POST',
      json: {
        signal_id: signalId,
        account,
        plan_id: old?.plan.plan_id ?? '',
        base_revision: old?.plan.revision ?? 0,
      },
    }),
  )
  return parsePlan(raw.record)
}
export async function authorizePlan(r: PlanRecord, mode: 'sim_auto' | 'sim_confirm' | 'revoke', operator: string) {
  const raw = obj(
    await request(`/api/trade/plans/${encodeURIComponent(r.plan.plan_id)}/authorize`, {
      method: 'POST',
      json: {
        revision: r.plan.revision,
        mode,
        operator,
      },
    }),
  )
  return parsePlan(raw.record)
}
export function executePlan(r: PlanRecord, operator: string) {
  return request(`/api/trade/plans/${encodeURIComponent(r.plan.plan_id)}/execute`, {
    method: 'POST',
    json: { revision: r.plan.revision, operator },
  })
}
export async function listIntents(account: string): Promise<ExecutionIntent[]> {
  const raw = obj(await request('/api/trade/intents', { query: { account } }))
  if (!Array.isArray(raw.intents)) throw new Error('执行意图列表未返回')
  return raw.intents.map((v) => {
    const r = obj(v)
    if (typeof r.client_order_id !== 'string' || typeof r.state !== 'string' || typeof r.business_key !== 'string')
      throw new Error('执行意图身份不完整')
    return r as unknown as ExecutionIntent
  })
}
