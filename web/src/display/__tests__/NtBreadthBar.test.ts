/**
 * 涨跌广度三段条契约：分段宽按占比、aria-label 三档家数、任一计数 null 即 missing
 * （不拿缺段冒充 0 家）、loading 且缺数据显骨架。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtBreadthBar from '@/display/NtBreadthBar.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'

describe('NtBreadthBar', () => {
  it('三段宽按占比 + aria-label 报三档家数', () => {
    const wrapper = mount(NtBreadthBar, { props: { advance: 3100, flat: 210, decline: 1800 } })
    const bar = wrapper.find('.breadth__bar')
    expect(bar.attributes('aria-label')).toBe('涨 3100 家、平 210 家、跌 1800 家')
    expect(wrapper.find('.breadth__seg--up').attributes('style')).toContain('width: 60.7%')
    expect(wrapper.find('.breadth__seg--flat').attributes('style')).toContain('width: 4.1%')
    expect(wrapper.find('.breadth__seg--down').attributes('style')).toContain('width: 35.2%')
    const counts = wrapper.find('.breadth__counts')
    expect(counts.text()).toContain('3100')
    expect(counts.text()).toContain('210')
    expect(counts.text()).toContain('1800')
  })

  it('任一计数 null → missing 空态', () => {
    const wrapper = mount(NtBreadthBar, { props: { advance: null, flat: 210, decline: 1800 } })
    expect(wrapper.text()).toContain('涨跌家数未返回')
    expect(wrapper.find('.breadth__bar').exists()).toBe(false)
  })

  it('loading 且缺数据显骨架；loading 但有旧值不清屏', () => {
    const skeleton = mount(NtBreadthBar, { props: { advance: null, flat: null, decline: null, loading: true } })
    expect(skeleton.findComponent(NtSkeleton).exists()).toBe(true)
    const stale = mount(NtBreadthBar, { props: { advance: 1, flat: 1, decline: 1, loading: true } })
    expect(stale.find('.breadth__bar').exists()).toBe(true)
  })
})
