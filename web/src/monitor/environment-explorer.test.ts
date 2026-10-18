import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { getEnvironment } from '@/api/environment'
import EnvironmentExplorer from './EnvironmentExplorer.vue'
vi.mock('@/api/environment', () => ({ getEnvironment: vi.fn() }))
afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
})
it('关闭时不探测，打开只读，失败清旧值，关闭后停止轮询', async () => {
  vi.useFakeTimers()
  vi.mocked(getEnvironment).mockResolvedValue({
    sampledAt: '',
    cacheExpiresAt: '',
    cached: false,
    note: '',
    unsupported: [],
    services: [
      {
        service: 'risk',
        state: 'unsupported',
        checkedAt: '',
        probeDurationMs: null,
        note: 'consumer_observation_endpoint_not_registered',
        observation: null,
      },
    ],
  })
  const w = mount(EnvironmentExplorer, { global: { stubs: { Teleport: true } } })
  try {
    expect(getEnvironment).not.toHaveBeenCalled()
    await w.find('button').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('最终风控')
    expect(w.text()).toContain('未接操作指标')
    expect(w.find('table').exists()).toBe(false)
    vi.mocked(getEnvironment).mockRejectedValue(new Error('离线'))
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.text()).toContain('数量未知')
    expect(w.text()).not.toContain('最终风控')
    await w.find('[role="dialog"] header button').trigger('click')
    const calls = vi.mocked(getEnvironment).mock.calls.length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(getEnvironment).toHaveBeenCalledTimes(calls)
  } finally {
    w.unmount()
  }
})
