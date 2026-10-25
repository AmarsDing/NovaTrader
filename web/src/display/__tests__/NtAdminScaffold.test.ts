/**
 * NtAdminScaffold 布局件（W4a；UI 宪法 R-UI-44/45）合同测试：
 * - 三槽结构：filter 条在 body 之前（文档序），drawer 在主体右侧且仅在 drawerWidth>0 时渲染槽位；
 * - 抽屉开合：drawerWidth=0 不渲开关不渲抽屉（面板未声明档位）；>0 时开关 emit update:drawerOpen 反值；
 * - 阶梯宽度：面板只声明档位（320/480），宽度由件内落到像素，面板代码零裸值；
 * - 纯布局：无数据请求、无 store/router 依赖，空槽不渲骨架占位。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtAdminScaffold from '../NtAdminScaffold.vue'

const slots = {
  filter: '<span data-t="f">筛选条</span>',
  default: '<div data-t="b">主体表</div>',
  drawer: '<div data-t="d">抽屉详情</div>',
}

describe('NtAdminScaffold 三槽结构', () => {
  it('filter 条在 body 之前（文档序），三槽都渲染', () => {
    const wrapper = mount(NtAdminScaffold, { slots, props: { drawerWidth: 320, drawerOpen: true } })
    const filter = wrapper.find('[data-region="filter"]')
    const body = wrapper.find('[data-region="body"]')
    expect(filter.exists()).toBe(true)
    expect(body.exists()).toBe(true)
    expect(filter.element.compareDocumentPosition(body.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // 抽屉在主体之后（右侧）。
    const drawer = wrapper.find('[data-region="drawer"]')
    expect(drawer.exists()).toBe(true)
    expect(body.element.compareDocumentPosition(drawer.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('无 filter 槽时不渲筛选骨架（空槽不占位）', () => {
    const wrapper = mount(NtAdminScaffold, { slots: { default: '<div>主体</div>' } })
    expect(wrapper.find('[data-region="filter"]').exists()).toBe(false)
    expect(wrapper.find('[data-region="body"]').exists()).toBe(true)
  })
})

describe('NtAdminScaffold 抽屉开合', () => {
  it('drawerWidth=0：不渲开关、不渲抽屉', () => {
    const wrapper = mount(NtAdminScaffold, { slots })
    expect(wrapper.find('.nt-admin__toggle').exists()).toBe(false)
    expect(wrapper.find('[data-region="drawer"]').exists()).toBe(false)
  })

  it('drawerWidth>0 + 开：抽屉渲出且带阶梯像素宽；开关 aria-expanded=true', () => {
    const wrapper = mount(NtAdminScaffold, { slots, props: { drawerWidth: 480, drawerOpen: true } })
    const drawer = wrapper.find('[data-region="drawer"]')
    expect(drawer.attributes('style')).toContain('width: 480px')
    expect(wrapper.find('.nt-admin__toggle').attributes('aria-expanded')).toBe('true')
  })

  it('开关点击 emit update:drawerOpen 反值（关→开）', async () => {
    const wrapper = mount(NtAdminScaffold, { slots, props: { drawerWidth: 320, drawerOpen: false } })
    await wrapper.find('.nt-admin__toggle').trigger('click')
    expect(wrapper.emitted('update:drawerOpen')).toEqual([[true]])
  })
})

describe('NtAdminScaffold 纯布局', () => {
  it('无标题不渲标题槽；action 槽透传', () => {
    const wrapper = mount(NtAdminScaffold, {
      slots: { actions: '<button data-t="a">导出</button>', default: '<div>主体</div>' },
    })
    expect(wrapper.find('.nt-admin__title').exists()).toBe(false)
    expect(wrapper.find('[data-t="a"]').exists()).toBe(true)
  })
})
