import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { authorizePlan, listIntents, listPlans } from '@/api/automation'
import { useAccountStore } from '@/stores/account'
import ConditionalPlans from '../ConditionalPlans.vue'

vi.mock('@/api/automation', () => ({
  listPlans: vi.fn(),
  listIntents: vi.fn(),
  createPlan: vi.fn(),
  authorizePlan: vi.fn(),
  executePlan: vi.fn(),
}))
vi.mock('@/ws/hub', () => ({ onPush: () => () => {}, onResync: () => () => {} }))
afterEach(() => vi.restoreAllMocks())
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
