import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { authorizePlan, listIntents, listPlans, type PlanRecord } from '@/api/automation'
import { getOpsReadiness } from '@/api/ops'
import { useAccountStore } from '@/stores/account'
import { useAuthStore } from '@/stores/auth'
import ConditionalPlans from '../ConditionalPlans.vue'

vi.mock('@/api/automation', () => ({
  listPlans: vi.fn(),
  listIntents: vi.fn(),
  createPlan: vi.fn(),
  authorizePlan: vi.fn(),
  executePlan: vi.fn(),
}))
vi.mock('@/api/ops', () => ({ getOpsReadiness: vi.fn() }))
vi.mock('@/ws/hub', () => ({ onPush: () => () => {}, onResync: () => () => {} }))
afterEach(() => {
  vi.restoreAllMocks()
  document.body.querySelectorAll('.nt-modal').forEach((n) => n.remove())
})
it('never grants authorization on mount and distinguishes unread from empty', async () => {
  setActivePinia(createPinia())
  vi.mocked(listPlans).mockRejectedValue(new Error('离线'))
  vi.mocked(listIntents).mockResolvedValue([])
  const w = mount(ConditionalPlans)
  try {
    await flushPromises()
    expect(w.text()).toContain('尚未核验')
    expect(w.text()).not.toContain('当前没有持久计划')
    vi.mocked(listPlans).mockResolvedValue([])
    await w.find('button').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('当前没有持久计划')
    expect(authorizePlan).not.toHaveBeenCalled()
  } finally {
    w.unmount()
  }
})
it('clears old account immediately and discards a late previous response', async () => {
  setActivePinia(createPinia())
  const account = useAccountStore()
  let resolve: (v: never[]) => void = () => {}
  vi.mocked(listPlans)
    .mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolve = r
        }),
    )
    .mockResolvedValue([])
  vi.mocked(listIntents).mockResolvedValue([])
  const w = mount(ConditionalPlans)
  try {
    account.book = 'LIVE'
    await flushPromises()
    resolve([])
    await flushPromises()
    expect(w.text()).toContain('LIVE')
    expect(w.text()).not.toContain('SIM 条件自动')
    expect(authorizePlan).not.toHaveBeenCalled()
  } finally {
    w.unmount()
  }
})

/** W3c 启用模态（策略启用=计划授权承接，无独立启用 RPC）：draft 计划 v1 全字段合同行。 */
function planRow(): PlanRecord {
  return {
    plan: {
      version: 1,
      plan_id: 'p-1',
      revision: 1,
      snapshot_id: 's-1',
      signal_id: 7,
      account_type: 'SIM',
      account_id: 'default',
      symbol: '600000',
      side: 'buy',
      price_origin: 'rules',
      entry_low: 10,
      entry_high: 11,
      limit_price: 10.5,
      stop_loss: 9.5,
      take_profit: 12,
      max_notional: 10000,
      valid_from: '2026-10-16T00:00:00Z',
      valid_until: '2099-01-01T00:00:00Z',
      execution_day: '2026-10-16',
      summary: '测试计划',
      invalidation: ['破位'],
    },
    snapshot: {
      version: 1,
      snapshot_id: 's-1',
      config_revision: 1,
      strategy_revision: 1,
      account_generation: 1,
      formed_at: '2026-10-16T00:00:00Z',
      source_time: '2026-10-16T00:00:00Z',
      evidence: ['e'],
      counter_evidence: [],
      unknowns: [],
      reason: 'r',
    },
    state: 'draft',
    mode: 'sim_confirm',
    operator: '',
    reason: '',
  }
}
const modalButtons = () => Array.from(document.body.querySelectorAll<HTMLButtonElement>('.nt-modal button'))

it('W3c 授权前置检查：必堵未就位锁提交并逐条说明；必堵全就位后走原授权链（提示项不锁）', async () => {
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.user = { id: '1', name: 'owner-a', displayName: 'owner-a', role: 'owner', local: true }
  const account = useAccountStore()
  account.book = 'SIM'
  const row = planRow()
  vi.mocked(listPlans).mockResolvedValue([row])
  vi.mocked(listIntents).mockResolvedValue([])
  const w = mount(ConditionalPlans, { global: { stubs: { SymbolName: true } } })
  try {
    await flushPromises()
    const authBtn = w.findAll('button').find((b) => b.text().includes('授权 SIM 条件自动'))
    expect(authBtn).toBeTruthy()
    vi.mocked(getOpsReadiness).mockResolvedValue({
      asOf: '',
      ready: false,
      gates: [
        { gate: 'risk_engine', state: 'down', why: '引擎未上线', deeplink: '/ops#services' },
        { gate: 'risk_config', state: 'ok' },
        { gate: 'risk_killswitch', state: 'ok' },
        { gate: 'datahub_sources', state: 'down', why: '两源离线' },
        { gate: 'llm_config', state: 'ok' },
      ],
      blockers: [],
    })
    await authBtn!.trigger('click')
    await flushPromises()
    expect(document.body.textContent ?? '').toContain('计划授权前置检查')
    expect(document.body.textContent ?? '').toContain('还有 1 项未达')
    expect(document.body.textContent ?? '').toContain('风险引擎')
    expect(modalButtons().find((b) => b.textContent?.includes('确认授权'))?.disabled).toBe(true)
    expect(authorizePlan).not.toHaveBeenCalled()

    /* 必堵三闸全就位后解锁；advisory 数据源仍 down 但不锁（提示项语义）。 */
    vi.mocked(getOpsReadiness).mockResolvedValue({
      asOf: '',
      ready: true,
      gates: [
        { gate: 'risk_engine', state: 'ok' },
        { gate: 'risk_config', state: 'ok' },
        { gate: 'risk_killswitch', state: 'ok' },
        { gate: 'datahub_sources', state: 'down' },
        { gate: 'llm_config', state: 'ok' },
      ],
      blockers: [],
    })
    await authBtn!.trigger('click')
    await flushPromises()
    const submit = modalButtons().find((b) => b.textContent?.includes('确认授权'))
    expect(submit?.disabled).toBe(false)
    vi.mocked(authorizePlan).mockResolvedValue(row)
    submit!.click()
    await flushPromises()
    expect(authorizePlan).toHaveBeenCalledWith(row, 'sim_auto', 'owner-a')
  } finally {
    w.unmount()
  }
})

it('W3c 就绪口不可达不静默放行：必堵行全按未证实落锁（fail-closed）', async () => {
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.user = { id: '1', name: 'owner-a', displayName: 'owner-a', role: 'owner', local: true }
  const account = useAccountStore()
  account.book = 'SIM'
  vi.mocked(listPlans).mockResolvedValue([planRow()])
  vi.mocked(listIntents).mockResolvedValue([])
  vi.mocked(getOpsReadiness).mockRejectedValue(new Error('离线'))
  const w = mount(ConditionalPlans, { global: { stubs: { SymbolName: true } } })
  try {
    await flushPromises()
    await w
      .findAll('button')
      .find((b) => b.text().includes('授权 SIM 条件自动'))!
      .trigger('click')
    await flushPromises()
    expect(document.body.textContent ?? '').toContain('未证实')
    expect(modalButtons().find((b) => b.textContent?.includes('确认授权'))?.disabled).toBe(true)
    expect(authorizePlan).not.toHaveBeenCalled()
  } finally {
    w.unmount()
  }
})
