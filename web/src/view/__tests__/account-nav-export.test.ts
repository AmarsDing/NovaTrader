import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { exportNav } from '@/api/nav'
import { navFixture } from '@/api/__tests__/research-fixtures'
import AccountNavExport from '../AccountNavExport.vue'
vi.mock('@/api/nav', () => ({ exportNav: vi.fn() }))
it('读取不会创建研究，缺净值不能使用输入，修改查询即作废旧报告', async () => {
  vi.mocked(exportNav).mockResolvedValue(navFixture())
  const w = mount(AccountNavExport)
  try {
    expect(exportNav).not.toHaveBeenCalled()
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('缺少实际收盘快照')
    const use = w.findAll('button').find((b) => b.text().includes('填入研究草稿'))!
    expect(use.attributes('disabled')).toBeDefined()
    expect(w.emitted('use-input')).toBeUndefined()
    await w.find('input[type="date"]').setValue('2026-09-01')
    expect(w.text()).not.toContain('账本记录已读取')
  } finally {
    w.unmount()
  }
})
