import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, it, vi } from 'vitest'
import { previewPortfolio, type AllocationPreview } from '@/api/allocation'
import { allocationFixture } from '@/api/__tests__/research-fixtures'
import PortfolioPreview from '../PortfolioPreview.vue'
vi.mock('@/api/allocation', () => ({ previewPortfolio: vi.fn() }))
// P-08 批次：预览表代码位接 SymbolName（setup 内 useSecurityStore），需激活 pinia
beforeEach(() => setActivePinia(createPinia()))
it('按用户点击才核算，切研究任务清旧事实且丢弃迟到结果', async () => {
  vi.mocked(previewPortfolio).mockReset()
  let resolve: (p: AllocationPreview) => void = () => {}
  vi.mocked(previewPortfolio)
    .mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolve = r
        }),
    )
    .mockResolvedValue(allocationFixture('run-2'))
  const w = mount(PortfolioPreview, { props: { runId: 'run-1' } })
  try {
    expect(previewPortfolio).not.toHaveBeenCalled()
    await w.find('button').trigger('click')
    await w.setProps({ runId: 'run-2' })
    await w.find('button').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('存在阻断或未知')
    expect(w.text()).toContain('真实报价缺失')
    expect(w.text()).not.toContain('立即下单')
    resolve({ ...allocationFixture(), rows: [] })
    await flushPromises()
    // P-08 批次：代码位接 SymbolName inline（「名称 裸六位」合同），为旧数据行渲染空态壳而非 600519.SH 全码
    expect(w.text()).toContain('600519')
  } finally {
    w.unmount()
  }
})
