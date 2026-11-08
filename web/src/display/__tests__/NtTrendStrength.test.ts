/**
 * 趋势强度契约：MA20 上占比大数 + 站上/跌破计数 + 实时徽注；
 * sparkline 时序按交易日升序、当日 live 截面不在 days 内则补尾；
 * found=false / latest=null → NtEmpty 保卡位；ready=false（整源未取到）→ missing，
 * 禁把取数失败说成「任务未产出」。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { BreadthSummary } from '@/api/market'
import NtTrendStrength from '@/display/NtTrendStrength.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import NtSparkline from '@/viz/NtSparkline.vue'

function day(tradeDate: string, ratio: number) {
  return { tradeDate, window: 20, above: 1800, below: 1500, flat: 200, total: 3500, ratio, live: false, asOf: '' }
}

function breadth(patch: Partial<BreadthSummary> = {}): BreadthSummary {
  return {
    tradeDate: '2026-11-08',
    found: true,
    latest: { ...day('2026-11-08', 0.514), live: true },
    days: [day('2026-11-07', 0.5), day('2026-11-06', 0.48)],
    ...patch,
  }
}

describe('NtTrendStrength', () => {
  it('主读数 + 站上/跌破 + 实时徽注', () => {
    const wrapper = mount(NtTrendStrength, { props: { breadth: breadth() } })
    expect(wrapper.text()).toContain('MA20 上占比')
    expect(wrapper.text()).toContain('51.4%')
    expect(wrapper.text()).toContain('站上 1800')
    expect(wrapper.text()).toContain('跌破 1500')
    expect(wrapper.text()).toContain('实时')
  })

  it('sparkline：days 升序 + 当日 live 截面补尾；已在 days 内不重复', () => {
    const wrapper = mount(NtTrendStrength, { props: { breadth: breadth() } })
    expect(wrapper.getComponent(NtSparkline).props('values')).toEqual([0.48, 0.5, 0.514])

    const dup = mount(NtTrendStrength, {
      props: { breadth: breadth({ days: [day('2026-11-06', 0.48), day('2026-11-08', 0.51)] }) },
    })
    expect(dup.getComponent(NtSparkline).props('values')).toEqual([0.48, 0.51])
  })

  it('found=false 或 latest=null → 空态保卡位', () => {
    const notFound = mount(NtTrendStrength, { props: { breadth: breadth({ found: false, latest: null }) } })
    expect(notFound.text()).toContain('还没有广度数据')
    const noLatest = mount(NtTrendStrength, { props: { breadth: breadth({ latest: null }) } })
    expect(noLatest.text()).toContain('还没有广度数据')
    const nullBreadth = mount(NtTrendStrength, { props: { breadth: null } })
    expect(nullBreadth.text()).toContain('还没有广度数据')
  })

  it('ready=false → missing（不把取数失败说成任务未产出）；loading 且未就绪显骨架', () => {
    const missing = mount(NtTrendStrength, { props: { breadth: null, ready: false } })
    expect(missing.text()).toContain('广度快照未返回')
    expect(missing.text()).not.toContain('还没有广度数据')

    const loading = mount(NtTrendStrength, { props: { breadth: null, ready: false, loading: true } })
    expect(loading.findComponent(NtSkeleton).exists()).toBe(true)
    expect(loading.text()).not.toContain('广度快照未返回')
  })
})
