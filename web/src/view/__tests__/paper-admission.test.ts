import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { getAdmission, grantLive, parseAdmission, revokeLive, type AdmissionEnvelope } from '@/api/admission'
import { getOpsReadiness } from '@/api/ops'
import { useAuthStore } from '@/stores/auth'
import PaperAdmission from '../PaperAdmission.vue'

vi.mock('@/api/admission', async (original) => ({
  ...(await original<typeof import('@/api/admission')>()),
  getAdmission: vi.fn(),
  grantLive: vi.fn(),
  revokeLive: vi.fn(),
}))
vi.mock('@/api/ops', () => ({ getOpsReadiness: vi.fn() }))

afterEach(() => {
  vi.restoreAllMocks()
  // Teleport 到 body 的模态不随 wrapper 断言清理，兜底扫尾防串案。
  document.body.querySelectorAll('.nt-modal').forEach((n) => n.remove())
})

function fixture() {
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
        reasons: { missing_session_slots: 1 },
      },
    ],
    reasons: ['证据不足'],
    other_required_evidence: ['通道验收'],
    policy: 'server-clock-v1',
  }
}
/** W3c：getAdmission 改回执信封（报告 + 总闸/通道旗标）。 */
function envelope(patch?: Record<string, unknown>, flags?: { liveEnabled?: boolean; brokerConnected?: boolean }): AdmissionEnvelope {
  return {
    report: parseAdmission({ report_json: JSON.stringify({ ...fixture(), ...patch }) }),
    liveEnabled: flags?.liveEnabled ?? false,
    brokerConnected: flags?.brokerConnected ?? false,
  }
}
function loginOwner() {
  const auth = useAuthStore()
  auth.user = { id: '1', name: 'owner-a', displayName: 'owner-a', role: 'owner', local: true }
}
const readyEnvelope = () => envelope({ consecutive_days: 20, run_evidence_ready: true, days: [] })
const modalButtons = () => Array.from(document.body.querySelectorAll<HTMLButtonElement>('.nt-modal button'))

it('展示真实缺失原因，刷新失败清除旧天数并把总闸牌翻回关闭态', async () => {
  setActivePinia(createPinia())
  vi.mocked(getAdmission).mockResolvedValue(envelope(undefined, { liveEnabled: false }))
  const w = mount(PaperAdmission)
  try {
    await flushPromises()
    expect(w.text()).toContain('0 / 20')
    expect(w.text()).toContain('盘中采样缺失')
    expect(w.text()).toContain('实盘执行权限：未开放')
    vi.mocked(getAdmission).mockRejectedValue(new Error('离线'))
    await w.find('button').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('连续合格天数未知')
    expect(w.text()).not.toContain('0 / 20')
    // 证据读不到 → 授权按钮不再示人（不留过期「已开」门板）。
    expect(w.findAll('button').some((b) => b.text().includes('实盘授权'))).toBe(false)
  } finally {
    w.unmount()
  }
})

it('W3c 授权模态：证据未达标锁提交，达标后走审计端点授权并刷新证据', async () => {
  setActivePinia(createPinia())
  loginOwner()
  vi.mocked(getOpsReadiness).mockResolvedValue({
    asOf: '',
    ready: true,
    gates: [{ gate: 'risk_engine', state: 'ok' }],
    blockers: [],
  })
  vi.mocked(getAdmission).mockResolvedValue(envelope())
  const w = mount(PaperAdmission)
  try {
    await flushPromises()
    await w
      .findAll('button')
      .find((b) => b.text().includes('实盘授权'))!
      .trigger('click')
    await flushPromises()
    expect(document.body.textContent ?? '').toContain('申请实盘授权')
    expect(getOpsReadiness).toHaveBeenCalled()
    const submit = modalButtons().find((b) => b.textContent?.includes('提交实盘授权申请'))!
    expect(submit.disabled).toBe(true)
    expect(grantLive).not.toHaveBeenCalled()

    // 证据达标 → 模态仍开着，按钮解锁 → 提交走审计端点并回捞新证据。
    vi.mocked(getAdmission).mockResolvedValue(readyEnvelope())
    await w.find('button').trigger('click')
    await flushPromises()
    const open = modalButtons().find((b) => b.textContent?.includes('提交实盘授权申请'))
    expect(open?.disabled).toBe(false)
    vi.mocked(grantLive).mockResolvedValue(envelope({ consecutive_days: 20, run_evidence_ready: true, days: [] }, { liveEnabled: true, brokerConnected: true }))
    open!.click()
    await flushPromises()
    expect(grantLive).toHaveBeenCalledWith('owner-a')
    expect(revokeLive).not.toHaveBeenCalled()
    /* 回执即翻闸后真信：不再补 GET（初载+手动核验=2），界面直接翻「已开」。 */
    expect(vi.mocked(getAdmission).mock.calls.length).toBe(2)
    expect(w.text()).toContain('实盘执行权限：已开')
  } finally {
    w.unmount()
  }
})

it('W3c 已开态：模态改收回确认（不再摆前置清单），确认走收回审计端点', async () => {
  setActivePinia(createPinia())
  loginOwner()
  vi.mocked(getOpsReadiness).mockResolvedValue({ asOf: '', ready: true, gates: [], blockers: [] })
  vi.mocked(getAdmission).mockResolvedValue(envelope({ consecutive_days: 20, run_evidence_ready: true, days: [] }, { liveEnabled: true, brokerConnected: true }))
  const w = mount(PaperAdmission)
  try {
    await flushPromises()
    expect(w.text()).toContain('实盘执行权限：已开')
    await w
      .findAll('button')
      .find((b) => b.text().includes('已开'))!
      .trigger('click')
    await flushPromises()
    expect(document.body.textContent ?? '').toContain('收回走同一审计开关关闭：新实盘委托即刻 fail-closed')
    expect(document.body.querySelector('.nt-preflight')).toBeNull()
    vi.mocked(revokeLive).mockResolvedValue(envelope({ consecutive_days: 20, run_evidence_ready: true, days: [] }, { liveEnabled: false, brokerConnected: true }))
    modalButtons().find((b) => b.textContent?.includes('收回实盘授权'))!.click()
    await flushPromises()
    expect(revokeLive).toHaveBeenCalledWith('owner-a')
    expect(grantLive).not.toHaveBeenCalled()
    expect(w.text()).toContain('实盘执行权限：未开放')
  } finally {
    w.unmount()
  }
})

it('查看角色不出现授权通路（按钮禁用且无提交通路）', async () => {
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.user = { id: '2', name: 'viewer-a', displayName: 'viewer-a', role: 'viewer', local: true }
  vi.mocked(getOpsReadiness).mockResolvedValue({ asOf: '', ready: true, gates: [], blockers: [] })
  vi.mocked(getAdmission).mockResolvedValue(readyEnvelope())
  const w = mount(PaperAdmission)
  try {
    await flushPromises()
    const grant = w.findAll('button').find((b) => b.text().includes('实盘授权'))
    expect(grant?.attributes('disabled')).toBeDefined()
    await grant!.trigger('click')
    await flushPromises()
    expect(document.body.querySelector('.nt-modal')).toBeNull()
    expect(grantLive).not.toHaveBeenCalled()
  } finally {
    w.unmount()
  }
})
