/**
 * NtBlockerCard 组件（W3b；UI 宪法 R-UI-30/36）合同测试：
 * - ready=true：显「全部就绪」全绿徽，不渲 blocker 列表（slot 承接）；
 * - ready=false：按 blockers 逐条渲 中文闸门名/状态标签/severity 左边条色；
 * - 数据纪律：state=unknown 显「未证实」暗点、未登记 gate 代码原样显不编名、无 progress 不画条；
 * - 进度：done/total 渲成百分比条 + mono 计数；total 非正不画条；
 * - 点击「去处理」原样 emit navigate(deeplink)，display 件自己不碰 router。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtBlockerCard, { type BlockerItem } from '../NtBlockerCard.vue'

const base: BlockerItem = { gate: 'risk_engine', state: 'down', severity: 'danger', why: '风控引擎未就绪', deeplink: '/ops#services' }

describe('NtBlockerCard 就绪/未就绪两态', () => {
  it('ready=true：全绿徽 + 「全部就绪」，不渲列表', () => {
    const wrapper = mount(NtBlockerCard, { props: { ready: true, blockers: [] } })
    expect(wrapper.find('.nt-blocker__state.is-ok').text()).toBe('全部就绪')
    expect(wrapper.find('.nt-blocker__list').exists()).toBe(false)
    expect(wrapper.find('.nt-blocker__empty').exists()).toBe(false)
  })

  it('ready=false：头显待处理数，逐条渲 blocker', () => {
    const wrapper = mount(NtBlockerCard, {
      props: { ready: false, blockers: [base, { ...base, gate: 'sim_admission', state: 'warn', severity: 'warn' }] },
    })
    expect(wrapper.find('.nt-blocker__state.is-bad').text()).toBe('2 项待处理')
    expect(wrapper.findAll('.nt-blocker__row')).toHaveLength(2)
  })

  it('ready=false 但明细空：显「未返回明细」占位，不谎报数量', () => {
    const wrapper = mount(NtBlockerCard, { props: { ready: false, blockers: [] } })
    expect(wrapper.find('.nt-blocker__empty').text()).toContain('未返回明细')
    expect(wrapper.find('.nt-blocker__list').exists()).toBe(false)
  })
})

describe('NtBlockerCard 闸门行渲染', () => {
  it('已登记闸门映射中文名 + severity 定左边条色', () => {
    const wrapper = mount(NtBlockerCard, { props: { ready: false, blockers: [base] } })
    const row = wrapper.find('.nt-blocker__row')
    expect(row.find('.nt-blocker__gate').text()).toBe('风险引擎')
    expect(row.classes()).toContain('is-danger')
    expect(row.find('.nt-blocker__tag').text()).toBe('不可用')
    expect(row.find('.nt-blocker__why').text()).toBe('风控引擎未就绪')
  })

  it('warn 行 is-warn；unknown 显「未证实」且 data-state 穿透', () => {
    const wrapper = mount(NtBlockerCard, {
      props: { ready: false, blockers: [{ gate: 'llm_config', state: 'unknown', severity: 'warn', why: 'w' }] },
    })
    const row = wrapper.find('.nt-blocker__row')
    expect(row.classes()).toContain('is-warn')
    expect(row.attributes('data-state')).toBe('unknown')
    expect(row.find('.nt-blocker__tag').text()).toBe('未证实')
    expect(row.find('.nt-blocker__dot.is-unknown').exists()).toBe(true)
  })

  it('未登记 gate 代码原样显（不编名）', () => {
    const wrapper = mount(NtBlockerCard, {
      props: { ready: false, blockers: [{ gate: 'future_gate', state: 'warn', severity: 'warn' }] },
    })
    expect(wrapper.find('.nt-blocker__gate').text()).toBe('future_gate')
  })
})

describe('NtBlockerCard 进度条', () => {
  it('12/20 渲 60% 宽度条 + mono 计数', () => {
    const wrapper = mount(NtBlockerCard, {
      props: {
        ready: false,
        blockers: [{ gate: 'sim_admission', state: 'warn', severity: 'warn', progress: { done: 12, total: 20 } }],
      },
    })
    const bar = wrapper.find('.nt-blocker__bar > i')
    expect(bar.attributes('style')).toContain('width: 60%')
    expect(wrapper.find('.nt-blocker__count').text()).toBe('12/20')
  })

  it('total 非正不画条（缺数不伪造 0%）', () => {
    const wrapper = mount(NtBlockerCard, {
      props: {
        ready: false,
        blockers: [{ gate: 'sim_admission', state: 'warn', severity: 'warn', progress: { done: 0, total: 0 } }],
      },
    })
    expect(wrapper.find('.nt-blocker__progress').exists()).toBe(false)
  })
})

describe('NtBlockerCard 深链导航', () => {
  it('点「去处理」原样 emit navigate(deeplink)', async () => {
    const wrapper = mount(NtBlockerCard, { props: { ready: false, blockers: [base] } })
    await wrapper.find('.nt-blocker__go').trigger('click')
    expect(wrapper.emitted('navigate')).toEqual([['/ops#services']])
    expect(wrapper.find('.nt-blocker__go').attributes('data-deeplink')).toBe('/ops#services')
  })

  it('无 deeplink 不渲按钮', () => {
    const wrapper = mount(NtBlockerCard, {
      props: { ready: false, blockers: [{ gate: 'risk_config', state: 'warn', severity: 'warn' }] },
    })
    expect(wrapper.find('.nt-blocker__go').exists()).toBe(false)
  })
})
