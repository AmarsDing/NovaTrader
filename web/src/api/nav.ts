import { request } from './http'
import { pick } from '@/utils/format'
export interface NavPoint {
  date: string
  role: 'initial' | 'closing'
  snapshot_id: string
  generation: string
  as_of: string | null
  equity: number | null
  valuation_complete: boolean
  cash_flow_known: boolean
  external_flow: number | null
  reasons: string[]
}
export interface NavReport {
  version: 1
  scope: 'account_nav_export'
  account_type: 'SIM'
  account_id: 'default'
  frequency: '1d'
  points: NavPoint[]
  expected_dates: string[]
  initial_date: string
  missing_dates: string[]
  reasons: string[]
  cash_flow_policy: string
  calendar_version: string
  manifest_id: string
  manifest_hash: string
  research_as_of: string
}
export interface NavExport {
  report: NavReport
  inputJson: string
  inputSha256: string
  inputReady: boolean
  ledgerHash: string
}
export interface NavDraft {
  account: 'SIM'
  start_date: string
  end_date: string
  frequency: '1d'
  as_of?: string
  manifest_id?: string
  benchmark_json?: string
}
function strings(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((s) => typeof s === 'string')
}
function numberOrNull(v: unknown): boolean {
  return v === null || (typeof v === 'number' && Number.isFinite(v))
}
export function parseNavExport(raw: unknown): NavExport {
  const json = pick<string>(raw, 'reportJson', 'report_json')
  if (typeof json !== 'string') throw new Error('账本净值报告未返回')
  const r = JSON.parse(json) as NavReport
  if (
    !r ||
    r.version !== 1 ||
    r.scope !== 'account_nav_export' ||
    r.account_type !== 'SIM' ||
    r.account_id !== 'default' ||
    r.frequency !== '1d' ||
    !Array.isArray(r.points) ||
    r.points.length > 400 ||
    !strings(r.expected_dates) ||
    !strings(r.missing_dates) ||
    !strings(r.reasons)
  )
    throw new Error('净值报告合同或账户身份无效')
  for (const p of r.points) {
    if (
      !p ||
      !/^\d{4}-\d{2}-\d{2}$/.test(p.date) ||
      !['initial', 'closing'].includes(p.role) ||
      !numberOrNull(p.equity) ||
      !numberOrNull(p.external_flow) ||
      typeof p.generation !== 'string' ||
      !/^\d*$/.test(p.generation) ||
      typeof p.valuation_complete !== 'boolean' ||
      typeof p.cash_flow_known !== 'boolean' ||
      !strings(p.reasons)
    )
      throw new Error('净值点的身份、估值或现金流状态未核验')
  }
  const ready = pick(raw, 'inputReady', 'input_ready'),
    inputJson = pick<string>(raw, 'inputJson', 'input_json') || '',
    inputSha256 = pick<string>(raw, 'inputSha256', 'input_sha256') || ''
  if (
    typeof ready !== 'boolean' ||
    (ready &&
      (!inputJson ||
        !/^[a-f0-9]{64}$/.test(inputSha256) ||
        r.missing_dates.length ||
        r.reasons.length ||
        r.points.some((p) => !p.valuation_complete || !p.cash_flow_known || p.equity == null)))
  )
    throw new Error('研究输入就绪标志与证据不一致')
  if (!ready && inputJson) throw new Error('证据不完整的净值不能形成研究输入')
  return {
    report: r,
    inputReady: ready,
    inputJson,
    inputSha256,
    ledgerHash: pick<string>(raw, 'ledgerHash', 'ledger_hash') || '',
  }
}
export async function exportNav(draft: NavDraft): Promise<NavExport> {
  return parseNavExport(await request('/api/trade/nav/export', { method: 'POST', json: draft }))
}
