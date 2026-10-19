import { describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { buildSyntheticDepth } from '../depth-synth'
import NtDepthBoard from '../NtDepthBoard.vue'

const wrappers: VueWrapper[] = []

describe('buildSyntheticDepth：最近价阶梯合成', () => {
  it('常规模块：上下各五档，按 tick 累进，不伪造挂单量', () => {
    const depth = buildSyntheticDepth(10.35, { symbol: '600519.SH', preClose: 10 })
    expect(depth).not.toBeNull()
    expect(depth?.asks.map((l) => l.price)).toEqual([10.36, 10.37, 10.38, 10.39, 10.4])
    expect(depth?.bids.map((l) => l.price)).toEqual([10.34, 10.33, 10.32, 10.31, 10.3])
    expect(depth?.asks.every((l) => l.volume === null)).toBe(true)
    expect(depth?.bids.every((l) => l.volume === null)).toBe(true)
  })

  it('临近涨停封顶：只出到封板价（含封板价本身）', () => {
    // preClose 9.20 → 主板涨停 10.12
    const depth = buildSyntheticDepth(10.09, { symbol: '600519.SH', preClose: 9.2 })
    expect(depth?.asks.map((l) => l.price)).toEqual([10.1, 10.11, 10.12])
    // 封板价 12.00 档（20% 创业板）允许排到封板位
    const growth = buildSyntheticDepth(11.98, { symbol: '300750.SZ', preClose: 10 })
    expect(growth?.asks.map((l) => l.price)).toEqual([11.99, 12])
  })

  it('封涨停时卖侧为空、买侧照出；封跌停镜像', () => {
    const up = buildSyntheticDepth(11, { symbol: '600519.SH', preClose: 10 })
    expect(up?.asks).toEqual([])
    expect(up?.bids.map((l) => l.price)).toEqual([10.99, 10.98, 10.97, 10.96, 10.95])
    const down = buildSyntheticDepth(9, { symbol: '600519.SH', preClose: 10 })
    expect(down?.bids).toEqual([])
    expect(down?.asks.map((l) => l.price)).toEqual([9.01, 9.02, 9.03, 9.04, 9.05])
  })

  it('缺昨收不做封顶；自定义 tick；无效最近价返回 null', () => {
    const free = buildSyntheticDepth(5.11, { symbol: '' })
    expect(free?.asks.map((l) => l.price)).toEqual([5.12, 5.13, 5.14, 5.15, 5.16])
    const ticked = buildSyntheticDepth(10, { tick: 0.05 })
    expect(ticked?.asks.map((l) => l.price)).toEqual([10.05, 10.1, 10.15, 10.2, 10.25])
    expect(buildSyntheticDepth(null, {})).toBeNull()
    expect(buildSyntheticDepth(0, {})).toBeNull()
    expect(buildSyntheticDepth(Number.NaN, {})).toBeNull()
  })
})

describe('NtDepthBoard 合成兜底渲染', () => {
  it('双边无档且有最近价 → 合成阶梯 + 「合成·待数据」标注，量柱不渲染', () => {
    const wrapper = mount(NtDepthBoard, {
      props: { lastPrice: 10.35, symbol: '600519.SH', preClose: 10 },
    })
    wrappers.push(wrapper)
    expect(wrapper.find('.nt-depth__tag').exists()).toBe(true)
    expect(wrapper.find('.nt-depth__tag').text()).toBe('合成·待数据')
    expect(wrapper.find('.nt-depth__last').text()).toBe('10.35')
    const rows = wrapper.findAll('.nt-depth__row')
    expect(rows).toHaveLength(10)
    expect(rows.every((row) => row.classes().includes('nt-depth__row--syn'))).toBe(true)
    // 卖一侧 10.36、买一侧 10.34（卖盘倒序渲染，首行是卖五 10.40）
    expect(rows[0].find('.nt-depth__price').text()).toBe('10.40')
    expect(rows[4].find('.nt-depth__price').text()).toBe('10.36')
    expect(rows[5].find('.nt-depth__price').text()).toBe('10.34')
    expect(wrapper.findAll('.nt-depth__bar')).toHaveLength(0)
    expect(wrapper.text()).toContain('—') // 量位占位
  })

  it('封涨停：卖侧空档，只画买侧且不越过封板价', () => {
    const wrapper = mount(NtDepthBoard, {
      props: { lastPrice: 11, symbol: '600519.SH', preClose: 10 },
    })
    wrappers.push(wrapper)
    expect(wrapper.text()).not.toContain('11.01')
    expect(wrapper.text()).toContain('10.99')
    expect(wrapper.findAll('.nt-depth__row')).toHaveLength(5)
  })

  it('已有真实档位 → 完全不合成（不混真/合），量柱照常', () => {
    const wrapper = mount(NtDepthBoard, {
      props: {
        lastPrice: 10.35,
        preClose: 10,
        symbol: '600519.SH',
        asks: [{ price: 10.36, volume: 120 }],
        bids: [{ price: 10.34, volume: 80 }],
      },
    })
    wrappers.push(wrapper)
    expect(wrapper.find('.nt-depth__tag').exists()).toBe(false)
    expect(wrapper.findAll('.nt-depth__bar')).toHaveLength(2)
    expect(wrapper.findAll('.nt-depth__vol')[0].text()).toBe('120') // 卖侧渲染在上方，首行即卖一量
  })

  it('既无档也无最近价 → 纯空态，无标注无档位', () => {
    const wrapper = mount(NtDepthBoard)
    wrappers.push(wrapper)
    expect(wrapper.find('.nt-depth__mid').text()).toBe('—')
    expect(wrapper.findAll('.nt-depth__row')).toHaveLength(0)
    expect(wrapper.find('.nt-depth__tag').exists()).toBe(false)
  })
})
