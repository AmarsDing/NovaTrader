import { request } from './http'
import { pick } from '@/utils/format'
export interface AllocationRow {
  symbol: string
  target_weight: number
  current_quantity: number
  available_to_sell: number
  frozen_sell: number
  t1_locked: number
  reference_price: number | null
  source_time: string | null
  target_quantity: number | null
  delta_quantity: number | null
  buy_quantity: number
  sell_quantity: number
  estimated_fee: number | null
  projected_weight: number | null
  status: string
  reasons: string[]
}
export interface AllocationPreview {
  version: 1
  scope: 'portfolio_shadow_preview'
  state: 'complete' | 'blocked'
  allocation_ready: boolean
  can_execute: false
  risk_checked: false
  formed_at: string
  account_type: 'SIM'
  account_id: 'default'
  account_snapshot_id: string
  account_generation: string
  account_state_hash: string
  config_revision: number
  research: { run_id: string; manifest_id: string; manifest_hash: string; result_hash: string; fixture_type: string }
  summary: {
    fresh_equity: number | null
    available_cash: number
    frozen_cash: number
    required_cash: number | null
    estimated_fees: number | null
    buy_cost: number | null
    sell_proceeds: number | null
    projected_cash: number | null
    buy_funding_gap: number | null
    residual_weight: number | null
    turnover: number | null
    constraints: { code: string; status: string; message: string }[]
  }
  rows: AllocationRow[]
  unknowns: string[]
  execution_requirements: string[]
}
function strings(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((s) => typeof s === 'string')
}
function n(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}
function integer(v: unknown): boolean {
  return Number.isSafeInteger(v) && (v as number) >= 0
}
function nullable(v: unknown): boolean {
  return v === null || n(v)
}
export function parseAllocation(raw: unknown, runId: string): AllocationPreview {
  const json = pick<string>(raw, 'previewJson', 'preview_json')
  if (typeof json !== 'string') throw new Error('组合调整预览未返回')
  const r = JSON.parse(json) as AllocationPreview
  if (
    !r ||
    r.version !== 1 ||
    r.scope !== 'portfolio_shadow_preview' ||
    r.account_type !== 'SIM' ||
    r.account_id !== 'default' ||
    r.can_execute !== false ||
    r.risk_checked !== false ||
    !['complete', 'blocked'].includes(r.state) ||
    typeof r.allocation_ready !== 'boolean' ||
    r.allocation_ready !== (r.state === 'complete') ||
    r.research?.run_id !== runId ||
    typeof r.account_generation !== 'string' ||
    !/^\d+$/.test(r.account_generation) ||
    !Number.isFinite(Date.parse(r.formed_at)) ||
    !integer(r.config_revision) ||
    !Array.isArray(r.rows) ||
    r.rows.length > 200 ||
    !strings(r.unknowns) ||
    !strings(r.execution_requirements) ||
    !r.summary ||
    !Array.isArray(r.summary.constraints)
  )
    throw new Error('组合预览身份、范围或权限合同无效')
  const seen = new Set<string>()
  for (const row of r.rows) {
    if (
      !row ||
      !/^\d{6}\.(SH|SZ|BJ)$/.test(row.symbol) ||
      seen.has(row.symbol) ||
      !n(row.target_weight) ||
      row.target_weight < 0 ||
      row.target_weight > 1 ||
      ![
        row.current_quantity,
        row.available_to_sell,
        row.frozen_sell,
        row.t1_locked,
        row.buy_quantity,
        row.sell_quantity,
      ].every(integer) ||
      ![row.reference_price, row.estimated_fee, row.projected_weight].every(nullable) ||
      !(row.target_quantity === null || integer(row.target_quantity)) ||
      !(row.delta_quantity === null || Number.isSafeInteger(row.delta_quantity)) ||
      !['ready', 'blocked', 'unknown'].includes(row.status) ||
      !strings(row.reasons)
    )
      throw new Error('组合预览行缺少可核对数量或报价')
    seen.add(row.symbol)
  }
  if (
    ![
      r.summary.fresh_equity,
      r.summary.required_cash,
      r.summary.estimated_fees,
      r.summary.buy_cost,
      r.summary.sell_proceeds,
      r.summary.projected_cash,
      r.summary.buy_funding_gap,
      r.summary.residual_weight,
      r.summary.turnover,
    ].every(nullable) ||
    !n(r.summary.available_cash) ||
    !n(r.summary.frozen_cash) ||
    r.summary.constraints.some(
      (c) => !c || typeof c.message !== 'string' || !['passed', 'blocked', 'unknown'].includes(c.status),
    )
  )
    throw new Error('组合预览预算或约束证据缺失')
  if (
    r.allocation_ready &&
    (r.rows.some((row) => row.status !== 'ready' || row.reference_price == null || row.target_quantity == null) ||
      r.summary.fresh_equity == null ||
      r.summary.constraints.some((c) => c.status !== 'passed'))
  )
    throw new Error('组合预览完成标志与实际约束不一致')
  return r
}
export async function previewPortfolio(runId: string): Promise<AllocationPreview> {
  return parseAllocation(
    await request('/api/trade/portfolio/preview', { method: 'POST', json: { research_run_id: runId, account: 'SIM' } }),
    runId,
  )
}
