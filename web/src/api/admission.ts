import { request } from './http'
import { pick, type RawRecord } from '@/utils/format'

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
  /** 报告内部快照恒 false（serve 端写死）：证据本身永不当权限凭证，总闸状态看信封 liveEnabled。 */
  live_enabled: false
  days: AdmissionDay[]
  reasons: string[]
  other_required_evidence: string[]
  policy: string
}
/** GetAdmission 回执信封（admission.proto 5/6 号位）：总闸与通道状态独立于证据快照。 */
export interface AdmissionEnvelope {
  report: AdmissionReport
  /** LIVE 总闸当前状态：GrantLive 审计留痕开启 / 缺链按 false（fail-closed，proto 头注的口径）。 */
  liveEnabled: boolean
  /** 文件单通道探测：掉线/探测失败回落 false（fail-closed）。 */
  brokerConnected: boolean
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
/** 证据 + 总闸/通道信封。旧报告内部 live_enabled 恒 false 合同不松；总闸只从信封取。 */
function toEnvelope(raw: unknown): AdmissionEnvelope {
  const r = (raw ?? {}) as RawRecord
  return {
    report: parseAdmission(r),
    liveEnabled: pick<boolean>(r, 'liveEnabled', 'live_enabled') === true,
    brokerConnected: pick<boolean>(r, 'brokerConnected', 'broker_connected') === true,
  }
}
export async function getAdmission(): Promise<AdmissionEnvelope> {
  return toEnvelope(await request('/api/trade/admission', { query: { account: 'SIM', limit: 30 } }))
}
/**
 * GrantLive 开实盘总闸的唯一路径：admin 网关 Owner 鉴权+审计（trade.grant_live）后转发。
 * 回执就是翻闸后的 AdmissionEvidenceReply（双闸旗标 + 重算报告，服务端 admissionReply 同构拼）——
 * 直接回填界面，不补一次 GET（服务端 RevokeLive 内部已重跑准入评估，再拉是纯冗余往返）。
 */
export async function grantLive(operator: string): Promise<AdmissionEnvelope> {
  return toEnvelope(await request('/api/trade/admission/live/grant', { method: 'POST', json: { operator } }))
}
/** RevokeLive 收权：同样审计留痕后立即关闭，新实盘委托 fail-closed；回执回口同 grant。 */
export async function revokeLive(operator: string): Promise<AdmissionEnvelope> {
  return toEnvelope(await request('/api/trade/admission/live/revoke', { method: 'POST', json: { operator } }))
}
