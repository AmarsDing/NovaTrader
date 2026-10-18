import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { listResearchJobs, createResearchJob, cancelResearchJob } from '@/api/research'
import ResearchJobs from '../ResearchJobs.vue'
vi.mock('@/api/research', () => ({ listResearchJobs: vi.fn(), createResearchJob: vi.fn(), cancelResearchJob: vi.fn() }))
vi.mock('@/ws/hub', () => ({ subscribe: () => () => {}, onPush: () => () => {}, onResync: () => () => {} }))
it('distinguishes missing jobs from an empty successful response and never creates on mount', async () => {
  setActivePinia(createPinia())
  vi.mocked(listResearchJobs).mockRejectedValueOnce(new Error('服务未返回')).mockResolvedValue([])
  const w = mount(ResearchJobs)
  try {
    await flushPromises()
    expect(w.text()).toContain('研究任务尚未核验')
    expect(w.text()).not.toContain('当前没有研究任务')
    await w.find('button').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('当前没有研究任务')
    expect(createResearchJob).not.toHaveBeenCalled()
    expect(cancelResearchJob).not.toHaveBeenCalled()
  } finally {
    w.unmount()
  }
})
