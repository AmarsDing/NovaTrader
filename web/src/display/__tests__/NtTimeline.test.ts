/**
 * NtTimeline 渲染合同（W4c 通知二维分级补位）：kindBadge 徽注优先于裸 tag；
 * 无 kindBadge 时 tag 走旧路（.nt-tl__tag 裸文本，旧消费面零回归）；两者皆无则都不渲染。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtTimeline, { type TimelineEntry } from '../NtTimeline.vue'
import NtBadge from '@/ui/NtBadge.vue'

function itemsOf(entry: Partial<TimelineEntry>): TimelineEntry[] {
  return [{ id: 1, time: '09:31', title: '样例', ...entry }]
}

describe('NtTimeline（W4c 类别徽注）', () => {
  it('kindBadge 渲染 NtBadge 且抑制裸 tag（徽注优先，双轨不并显）', () => {
    const w = mount(NtTimeline, {
      props: { items: itemsOf({ kindBadge: { text: '风控', tone: 'warn' }, tag: 'risk' }) },
    })
    const badge = w.findComponent(NtBadge)
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('风控')
    expect(badge.props('tone')).toBe('warn')
    expect(w.find('.nt-tl__tag').exists()).toBe(false)
  })

  it('无 kindBadge 时 tag 走旧路（裸文本，brain-monitor/evolution 等旧消费面零回归）', () => {
    const w = mount(NtTimeline, { props: { items: itemsOf({ tag: '信号' }) } })
    expect(w.findComponent(NtBadge).exists()).toBe(false)
    expect(w.find('.nt-tl__tag').text()).toBe('信号')
  })

  it('kindBadge/tag 皆无：两者都不渲染（占位不缺角）', () => {
    const w = mount(NtTimeline, { props: { items: itemsOf({}) } })
    expect(w.findComponent(NtBadge).exists()).toBe(false)
    expect(w.find('.nt-tl__tag').exists()).toBe(false)
  })
})
