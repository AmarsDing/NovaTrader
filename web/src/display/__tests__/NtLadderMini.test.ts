/**
 * 涨停梯队精简卡契约：封板率条宽/文案、梯队色档（≥5 板 up / ≥3 板 accent / 其余 plain）、
 * 条宽相对最高档（最小 6%）、晋级率行、封炸双零显「今日暂无涨停个股」、model=null 显 missing。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtLadderMini, { type LadderMiniModel } from '@/display/NtLadderMini.vue'

const MODEL: LadderMiniModel = {
  sealed: 4,
  broken: 1,
  sealRate: 0.8,
  tiers: [
    { height: 5, count: 1 },
    { height: 3, count: 1 },
    { height: 1, count: 2 },
  ],
  steps: [
    { label: '1 进 2', rate: 0.45, sample: 20 },
    { label: '3 进 4+', rate: 0.4, sample: 5 },
  ],
}

describe('NtLadderMini', () => {
  it('封板率条 + 封/炸计数', () => {
    const wrapper = mount(NtLadderMini, { props: { model: MODEL } })
    expect(wrapper.find('.lmini__sealbar i').attributes('style')).toContain('width: 80%')
    expect(wrapper.text()).toContain('封板率 80%')
    expect(wrapper.text()).toContain('封 4 / 炸 1')
  })

  it('梯队色档与相对条宽（max=2 → 1只=50%，2只=100%）', () => {
    const wrapper = mount(NtLadderMini, { props: { model: MODEL } })
    const rows = wrapper.findAll('.lmini__tiers li')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.find('.lmini__h').classes()).toContain('up')
    expect(rows[1]!.find('.lmini__h').classes()).toContain('accent')
    expect(rows[2]!.find('.lmini__h').classes()).toContain('plain')
    expect(rows[0]!.find('.lmini__track i').attributes('style')).toContain('width: 50%')
    expect(rows[2]!.find('.lmini__track i').attributes('style')).toContain('width: 100%')
  })

  it('晋级率行拼接；model=null 显 missing；封炸双零显提示', () => {
    const wrapper = mount(NtLadderMini, { props: { model: MODEL } })
    expect(wrapper.find('.lmini__steps').text()).toContain('1 进 2 45%')
    expect(wrapper.find('.lmini__steps').text()).toContain('3 进 4+ 40%')

    const missing = mount(NtLadderMini, { props: { model: null } })
    expect(missing.text()).toContain('涨停名单未返回')

    const none = mount(NtLadderMini, { props: { model: { sealed: 0, broken: 0, sealRate: null, tiers: [], steps: [] } } })
    expect(none.text()).toContain('今日暂无涨停个股')
    expect(none.text()).toContain('封板率 —')
  })
})
