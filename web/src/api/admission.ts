import { request } from './http'
import { pick } from '@/utils/format'

export interface AdmissionDay {
  trading_day: string
  status: 'qualified' | 'incomplete' | 'running'
  required_slots: number
  observed_slots: number
  healthy_slots: number
  missing_slots: number
  unhealthy_slots: number
  samples: number
  closing_proof: boolean
  reasons: Record<string, number>
}
export interface AdmissionReport {
  version: 1
  account_type: 'SIM'
  account_id: 'default'
  evaluated_at: string
  required_days: number
  consecutive_days: number
  latest_closed_trading_day: string
  run_evidence_ready: boolean
  live_enabled: false
  days: AdmissionDay[]
  reasons: string[]
  other_required_evidence: string[]
  policy: string
}
function count(v: unknown): v is number {
  return Number.isSafeInteger(v) && (v as number) >= 0
}
function strings(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((s) => typeof s === 'string')
}
export function parseAdmission(raw: unknown): AdmissionReport {
  const json = pick<string>(raw, 'reportJson', 'report_json')
  if (typeof json !== 'string') throw new Error('连续运行证据尚未返回')
  const r = JSON.parse(json) as AdmissionReport
  if (
    !r ||
    r.version !== 1 ||
    r.account_type !== 'SIM' ||
    r.account_id !== 'default' ||
    r.live_enabled !== false ||
    !Number.isFinite(Date.parse(r.evaluated_at)) ||
    !count(r.required_days) ||
    r.required_days < 20 ||
    !count(r.consecutive_days) ||
    typeof r.run_evidence_ready !== 'boolean' ||
    r.run_evidence_ready !== r.consecutive_days >= r.required_days ||
    !/^\d{4}-\d{2}-\d{2}$/.test(r.latest_closed_trading_day) ||
    !strings(r.reasons) ||
    !strings(r.other_required_evidence) ||
    !Array.isArray(r.days) ||
    r.days.length > 121
  ) {
    throw new Error('连续运行证据合同不完整，不能据此判断准入')
  }
  for (const d of r.days) {
    if (
      !d ||
      !/^\d{4}-\d{2}-\d{2}$/.test(d.trading_day) ||
      !['qualified', 'incomplete', 'running'].includes(d.status) ||
      ![d.required_slots, d.observed_slots, d.healthy_slots, d.missing_slots, d.unhealthy_slots, d.samples].every(
        count,
      ) ||
      d.observed_slots + d.missing_slots !== d.required_slots ||
      d.healthy_slots + d.unhealthy_slots !== d.observed_slots ||
      typeof d.closing_proof !== 'boolean' ||
      !d.reasons ||
      typeof d.reasons !== 'object' ||
      Array.isArray(d.reasons) ||
      !Object.values(d.reasons).every(count) ||
      (d.status === 'qualified' && (!d.closing_proof || d.missing_slots || d.unhealthy_slots))
    ) {
      throw new Error('交易日采样证据不完整')
    }
  }
  return r
}
export async function getAdmission(): Promise<AdmissionReport> {
  return parseAdmission(await request('/api/trade/admission', { query: { account: 'SIM', limit: 30 } }))
}
