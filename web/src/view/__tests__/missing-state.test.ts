import { createPinia } from 'pinia'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { request } from '@/api/http'
import OverviewView from '../OverviewView.vue'
import LimitLadder from '@/panels/market/limit-ladder.panel.vue'
import NtButton from '@/ui/NtButton.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtLadderTiers from '@/display/NtLadderTiers.vue'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
vi.mock('@/ws/hub', () => ({ isLive: () => false, onPush: () => () => {}, onResync: () => () => {}, subscribe: () => () => {} }))

it('决策台区分失败与成功空集合，刷新失败后不继续声称没有机会', async () => {
  let offline = true
  vi.mocked(request).mockImplementation(async <T>(): Promise<T> => {
    if (offline) throw new Error('服务离线')
    return { items: [] } as T
  })
  const wrapper = shallowMount(OverviewView, { global: { plugins: [createPinia()] } })
  try {
    await flushPromises()
    expect(wrapper.text()).toContain('信号未返回，待执行数量未知')
    expect(wrapper.text()).toContain('候选未返回，漏选数量未知')
    expect(wrapper.text()).not.toContain('没有待执行信号')
    offline = false
    wrapper.findComponent(NtButton).vm.$emit('click')
    await flushPromises()
    expect(wrapper.text()).toContain('没有待执行信号')
    expect(wrapper.text()).toContain('候选 0 · 入选 0')
    offline = true
    wrapper.findComponent(NtButton).vm.$emit('click')
    await flushPromises()
    expect(wrapper.text()).toContain('信号未返回，待执行数量未知')
    expect(wrapper.text()).not.toContain('没有待执行信号')
  } finally { wrapper.unmount() }
})

it('涨停梯队首次失败不填0，成功空列表显示0，再次失败恢复未知', async () => {
  let offline = true
  vi.mocked(request).mockImplementation(async <T>(): Promise<T> => {
    if (offline) throw new Error('服务离线')
    return { items: [] } as T
  })
  const wrapper = shallowMount(LimitLadder)
  const stat = (label: string) => wrapper.findAllComponents(NtStatCard).find((card) => card.props('label') === label)!
  try {
    await flushPromises()
    expect(stat('涨停').props('value')).toBe('—')
    expect(stat('炸板率').props('value')).toBe('—')
    expect(wrapper.findComponent(NtLadderTiers).props('empty')).toContain('未返回')
    offline = false
    wrapper.findComponent(NtButton).vm.$emit('click')
    await flushPromises()
    expect(stat('涨停').props('value')).toBe('0')
    expect(wrapper.findComponent(NtLadderTiers).props('empty')).toBe('今日无涨停梯队')
    offline = true
    wrapper.findComponent(NtButton).vm.$emit('click')
    await flushPromises()
    expect(stat('涨停').props('value')).toBe('—')
    expect(stat('连板高度').props('value')).toBe('—')
    expect(wrapper.text()).toContain('跌停名单未返回')
  } finally { wrapper.unmount() }
})
