import { describe, expect, it, vi } from 'vitest'
import { getAdmission, grantLive, parseAdmission, revokeLive } from '../admission'

const m = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../http', () => ({ request: m.request }))

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

describe('准入信封（W3c：总闸/通道独立取位）', () => {
  it('信封 liveEnabled/brokerConnected 取自回执信封，缺链按 false（fail-closed）；内部报告 live_enabled 合同不松', async () => {
    m.request.mockResolvedValue({ report_json: JSON.stringify(admissionFixture()), live_enabled: true, broker_connected: true })
    const env = await getAdmission()
    expect(env.liveEnabled).toBe(true)
    expect(env.brokerConnected).toBe(true)
    expect(env.report.live_enabled).toBe(false)
    m.request.mockResolvedValue({ reportJson: JSON.stringify(admissionFixture()) })
    const missing = await getAdmission()
    expect(missing.liveEnabled).toBe(false)
    expect(missing.brokerConnected).toBe(false)
  })

  it('grant/revoke 走审计端点带 operator（LIVE 总闸开关不经配置页）；回执即翻闸后信封直接回填', async () => {
    m.request.mockResolvedValue({ report_json: JSON.stringify(admissionFixture()), live_enabled: true, broker_connected: true })
    const granted = await grantLive('owner-a')
    expect(m.request).toHaveBeenLastCalledWith('/api/trade/admission/live/grant', { method: 'POST', json: { operator: 'owner-a' } })
    expect(granted.liveEnabled).toBe(true)
    expect(granted.report.run_evidence_ready).toBe(false)
    m.request.mockResolvedValue({ report_json: JSON.stringify(admissionFixture()), live_enabled: false, broker_connected: false })
    const revoked = await revokeLive('owner-a')
    expect(m.request).toHaveBeenLastCalledWith('/api/trade/admission/live/revoke', { method: 'POST', json: { operator: 'owner-a' } })
    expect(revoked.liveEnabled).toBe(false)
    expect(revoked.brokerConnected).toBe(false)
  })
})
