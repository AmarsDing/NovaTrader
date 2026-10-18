import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { getAdmission, parseAdmission } from '@/api/admission'
import PaperAdmission from '../PaperAdmission.vue'
vi.mock('@/api/admission', async (original) => ({
  ...(await original<typeof import('@/api/admission')>()),
  getAdmission: vi.fn(),
}))
afterEach(() => vi.restoreAllMocks())
it('展示真实缺失原因，刷新失败清除旧天数，不提供授权按钮', async () => {
  const fixture = {
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
        reasons: { missing_session_slots: 1 },
      },
    ],
    reasons: ['证据不足'],
    other_required_evidence: ['通道验收'],
    policy: 'server-clock-v1',
  }
  vi.mocked(getAdmission).mockResolvedValue(parseAdmission({ report_json: JSON.stringify(fixture) }))
  const w = mount(PaperAdmission)
  try {
    await flushPromises()
    expect(w.text()).toContain('0 / 20')
    expect(w.text()).toContain('盘中采样缺失')
    expect(w.text()).toContain('实盘执行权限：未开放')
    expect(w.findAll('button')).toHaveLength(1)
    vi.mocked(getAdmission).mockRejectedValue(new Error('离线'))
    await w.find('button').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('连续合格天数未知')
    expect(w.text()).not.toContain('0 / 20')
  } finally {
    w.unmount()
  }
})
