/**
 * 指数卡墙契约：红涨绿跌 class、stale 徽注（source=1d→昨收 / 其余→旧）、
 * found=false 显 NtMissing（不拿 0 点位冒充）、空态 NtEmpty、首读骨架。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { IndexQuoteView } from '@/api/indexSnapshot'
import NtIndexCards from '@/display/NtIndexCards.vue'
import NtMissing from '@/ui/NtMissing.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'

function ix(patch: Partial<IndexQuoteView>): IndexQuoteView {
  return {
    code: '000001.SH',
    name: '上证指数',
    last: 3052.11,
    chg: 12.3,
    pctChg: 0.4,
    amplitude: 0.8,
    asOf: '',
    source: 'snap',
    stale: false,
    found: true,
    ...patch,
  }
}

describe('NtIndexCards', () => {
  it('正常卡：名称/点位/涨跌文本 + 红涨绿跌 class', () => {
    const wrapper = mount(NtIndexCards, {
      props: { indices: [ix({}), ix({ code: '399001.SZ', name: '深证成指', chg: -23.1, pctChg: -0.23 })] },
    })
    const cards = wrapper.findAll('.ix-card')
    expect(cards).toHaveLength(2)
    expect(cards[0]!.text()).toContain('上证指数')
    expect(cards[0]!.text()).toContain('3052.11')
    expect(cards[0]!.find('.ix-card__chg').classes()).toContain('up')
    expect(cards[0]!.text()).toContain('+0.40%')
    expect(cards[1]!.find('.ix-card__chg').classes()).toContain('down')
    expect(cards[1]!.text()).toContain('-0.23%')
  })

  it('stale 徽注：source=1d 显「昨收」，其余陈旧显「旧」', () => {
    const wrapper = mount(NtIndexCards, {
      props: { indices: [ix({ stale: true, source: '1d' }), ix({ code: 'X', stale: true, source: 'snap' })] },
    })
    const tags = wrapper.findAll('.ix-card__tag')
    expect(tags.map((t) => t.text())).toEqual(['昨收', '旧'])
  })

  it('found=false 显 NtMissing，不渲染点位', () => {
    const wrapper = mount(NtIndexCards, { props: { indices: [ix({ found: false })] } })
    expect(wrapper.findComponent(NtMissing).exists()).toBe(true)
    expect(wrapper.find('.ix-card__last').exists()).toBe(false)
  })

  it('空数组 + loading 显骨架 ×3；空数组非 loading 显 missing 空态', () => {
    const loading = mount(NtIndexCards, { props: { indices: [], loading: true } })
    expect(loading.findAllComponents(NtSkeleton)).toHaveLength(3)
    const empty = mount(NtIndexCards, { props: { indices: [] } })
    expect(empty.text()).toContain('指数快照未返回')
  })
})
