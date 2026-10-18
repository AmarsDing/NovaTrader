import { describe, expect, it } from 'vitest'
import { parseAdmission } from '../admission'

export function admissionFixture() {
  return {
    version: 1,
    account_type: 'SIM',
    account_id: 'default',
    evaluated_at: '2026-10-16T08:00:00Z',
    required_days: 20,
    consecutive_days: 0,
    latest_closed_trading_day: '2026-10-16',
    run_evidence_ready: false,
    live_enabled: false,
    days: [
      {
        trading_day: '2026-10-16',
        status: 'incomplete',
        required_slots: 480,
        observed_slots: 479,
        healthy_slots: 478,
        missing_slots: 1,
        unhealthy_slots: 1,
        samples: 479,
        closing_proof: false,
        reasons: { missing_session_slots: 1, configuration_not_loaded: 1 },
      },
    ],
    reasons: ['证据不足'],
    other_required_evidence: ['通道验收'],
    policy: 'server-clock-v1',
  }
}
describe('连续运行证据合同', () => {
  it('保留缺失槽和闭市未知，不把累计天数当权限', () => {
    const r = parseAdmission({ reportJson: JSON.stringify(admissionFixture()), consecutiveDays: 90, liveEnabled: true })
    expect(r.consecutive_days).toBe(0)
    expect(r.days[0]?.missing_slots).toBe(1)
    expect(r.live_enabled).toBe(false)
  })
  it('拒绝假达标、短于20日、字段缺失和矛盾采样', () => {
    for (const patch of [
      { run_evidence_ready: true },
      { required_days: 3 },
      { live_enabled: true },
      { days: [{ ...admissionFixture().days[0], status: 'qualified' }] },
    ]) {
      expect(() => parseAdmission({ report_json: JSON.stringify({ ...admissionFixture(), ...patch }) })).toThrow()
    }
    expect(() => parseAdmission({})).toThrow()
  })
})
