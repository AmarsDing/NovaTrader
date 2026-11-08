/**
 * 盘中情绪路径 SFC 契约：points 空显 NtEmpty 保卡位（不画空图装没事）；
 * 有点位 → option 非空进 NtEChart；partial=true 附补集注记。
 * echarts/core 按 theme-chart.test.ts 同法 mock（jsdom 无 canvas）。
 */
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { EmotionPathPoint } from '@/api/market'
import NtEmotionPath from '@/viz/NtEmotionPath.vue'
import NtEChart from '@/viz/NtEChart.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'

const chart = vi.hoisted(() => ({ setOption: vi.fn(), clear: vi.fn(), resize: vi.fn(), dispose: vi.fn() }))
vi.mock('echarts/core', () => ({ use: vi.fn(), init: vi.fn(() => chart) }))

beforeEach(() => {
  // jsdom 无 ResizeObserver（与 theme-chart.test.ts 同法打桩）。
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
})

function point(time: string, auction: boolean): EmotionPathPoint {
  return {
    time,
    auction,
    up: 12,
    down: 2,
    broken: 1,
    brokenRate: 0.05,
    maxHeight: 3,
    profitEffect: 0,
    advance: 0,
    decline: 0,
    amount: 0,
    phase: 'WARM',
    scoreCoef: 1,
    stale: false,
  }
}

const POINTS = [point('2026-11-08T09:25:00+08:00', true), point('2026-11-08T09:35:00+08:00', false)]

describe('NtEmotionPath', () => {
  it('points 空 → NtEmpty 保卡位', () => {
    const wrapper = mount(NtEmotionPath, { props: { points: [] } })
    expect(wrapper.text()).toContain('非交易时段暂无情绪路径')
    expect(wrapper.findComponent(NtEChart).exists()).toBe(false)
  })

  it('有点位 → NtEChart 收到非空 option；partial 附注记', () => {
    const wrapper = mount(NtEmotionPath, { props: { points: POINTS, partial: true } })
    const echart = wrapper.getComponent(NtEChart)
    expect(echart.props('option')).toBeTruthy()
    expect(wrapper.text()).toContain('当日截面补集中')
  })

  it('ready=false 显 missing（不把取数失败说成非交易时段）；loading 且未就绪显骨架', () => {
    const missing = mount(NtEmotionPath, { props: { points: [], ready: false } })
    expect(missing.text()).toContain('情绪路径未返回')
    expect(missing.text()).not.toContain('非交易时段')

    const loading = mount(NtEmotionPath, { props: { points: [], ready: false, loading: true } })
    expect(loading.findComponent(NtSkeleton).exists()).toBe(true)
    expect(loading.findComponent(NtEChart).exists()).toBe(false)
    expect(loading.text()).not.toContain('情绪路径未返回')
  })


  it('非 partial 不附注记', () => {
    const wrapper = mount(NtEmotionPath, { props: { points: POINTS } })
    expect(wrapper.text()).not.toContain('当日截面补集中')
  })
})
