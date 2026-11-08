import { describe, expect, it } from 'vitest'
import type { Bar } from '@/api/types/market'
import { classicPivots, computeKeyLevels, prevHighLow, roundLevels, roundStep, unfilledGaps } from '../market-levels'

function bar(over: Partial<Bar>): Bar {
  return {
    time: '2026-01-05',
    open: 10,
    high: 11,
    low: 9,
    close: 10,
    volume: 1000,
    amount: 10000,
    adjFactor: null,
    ...over,
  }
}

/** 生成 n 根递增收敛的合法 K 线（方便控制高/低/收）。 */
function series(specs: { h: number; l: number; c: number; o?: number; t?: string }[]): Bar[] {
  return specs.map((s, i) =>
    bar({
      time: s.t ?? `2026-01-${String(i + 1).padStart(2, '0')}`,
      open: s.o ?? s.c,
      high: s.h,
      low: s.l,
      close: s.c,
    }),
  )
}

describe('prevHighLow 前高前低', () => {
  it('长窗取最高高/最低低；短窗同价去重（长窗优先）', () => {
    const bars = series([
      { h: 12, l: 8, c: 10 },
      { h: 15, l: 9, c: 11 },
      { h: 13, l: 10, c: 12 },
    ])
    const levels = prevHighLow(bars, 3, 2)
    // 长窗：高 15 / 低 8；短窗（后 2 根）：高 15（同价去重）、低 9
    expect(levels).toEqual([
      { price: 15, title: '3日高', lineStyle: 'dashed' },
      { price: 8, title: '3日低', lineStyle: 'dashed' },
      { price: 9, title: '2日低', lineStyle: 'dotted' },
    ])
  })

  it('短窗高低与长窗重合时只出长窗两条', () => {
    const bars = series([
      { h: 14, l: 9, c: 10 },
      { h: 15, l: 8, c: 11 },
    ])
    expect(prevHighLow(bars, 2, 1).map((l) => l.price)).toEqual([15, 8])
  })

  it('缺腿 K 线跳过；全缺 → 空', () => {
    const bars = series([{ h: 12, l: 8, c: 10 }])
    bars.push(bar({ time: '2026-01-06', high: null }))
    expect(prevHighLow(bars, 10, 5)).toEqual([
      { price: 12, title: '10日高', lineStyle: 'dashed' },
      { price: 8, title: '10日低', lineStyle: 'dashed' },
    ])
    expect(prevHighLow([bar({ high: null })], 10, 5)).toEqual([])
  })
})

describe('classicPivots 经典枢轴', () => {
  it('P=(H+L+C)/3、R1=2P−L、S1=2P−H，取最后一根四价齐全 K 线', () => {
    const bars = series([
      { h: 10, l: 8, c: 9 },
      { h: 12, l: 9, c: 11 },
    ])
    const [p, r1, s1] = classicPivots(bars)
    expect(p.price).toBeCloseTo((12 + 9 + 11) / 3)
    expect(r1.price).toBeCloseTo(2 * p.price - 9)
    expect(s1.price).toBeCloseTo(2 * p.price - 12)
  })

  it('末根缺腿 → 向前找四价齐全根；全缺 → 空', () => {
    const bars = series([{ h: 12, l: 8, c: 10 }])
    bars.push(bar({ time: '2026-01-06', close: null }))
    expect(classicPivots(bars)[0].price).toBeCloseTo(10)
    expect(classicPivots([bar({ low: null })])).toEqual([])
  })
})

describe('unfilledGaps 缺口', () => {
  it('幅度超阈值记缺口；未回补取缺口守边（上跳守下沿=昨收）', () => {
    const bars = series([
      { h: 10.1, l: 9.9, c: 10 },
      // 上跳 2%：open 10.2 vs 昨收 10
      { o: 10.2, h: 10.5, l: 10.15, c: 10.4, t: '2026-01-02' },
      // 第三根不再跳空（open 显式 10.45，落在阈值内）
      { o: 10.45, h: 10.6, l: 10.2, c: 10.5, t: '2026-01-03' },
    ])
    const gaps = unfilledGaps(bars, 60, 0.005, 5)
    expect(gaps).toHaveLength(1)
    expect(gaps[0].price).toBe(10)
    expect(gaps[0].title).toBe('缺口 2026-01-02')
  })

  it('宽松回补判定：后续任一根价格区覆盖缺口区即剔除', () => {
    const bars = series([
      { h: 10.1, l: 9.9, c: 10 },
      { o: 10.2, h: 10.4, l: 10.15, c: 10.3, t: '2026-01-02' },
      // 价格区 [9.9, 10.35] 覆盖缺口区 [10, 10.2] → 回补
      { o: 10.3, h: 10.35, l: 9.9, c: 10.0, t: '2026-01-03' },
    ])
    expect(unfilledGaps(bars, 60, 0.005, 5)).toEqual([])
  })

  it('幅度不足阈值不记；数量封顶取最近', () => {
    const mk = (base: number, i: number): Bar[] => [
      bar({ time: `2026-01-${String(i).padStart(2, '0')}`, open: base, high: base * 1.005, low: base * 0.995, close: base }),
      bar({
        time: `2026-01-${String(i + 1).padStart(2, '0')}`,
        open: base * 1.02,
        high: base * 1.03,
        low: base * 1.015,
        close: base * 1.025,
      }),
    ]
    const bars = [
      ...mk(10, 1),
      ...mk(10.25, 3),
      ...mk(10.5, 5),
      ...mk(10.75, 7),
      ...mk(11.0, 9),
      ...mk(11.25, 11),
      bar({ time: '2026-01-13', open: 11.5, high: 11.6, low: 11.4, close: 11.5 }),
    ]
    const gaps = unfilledGaps(bars, 60, 0.005, 3)
    expect(gaps.length).toBeLessThanOrEqual(3)
    // 小幅跳空（<0.5%）不记
    const quiet = series([
      { h: 10.02, l: 9.98, c: 10 },
      { o: 10.02, h: 10.1, l: 10.0, c: 10.05 },
    ])
    expect(unfilledGaps(quiet, 60, 0.005, 5)).toEqual([])
  })

  it('下跳缺口：守上沿（昨收）；后续价格区覆盖缺口区即回补（评审 P2-3 方向锁）', () => {
    const bars = series([
      { h: 10.1, l: 9.9, c: 10 },
      // 下跳 2%：open 9.8 vs 昨收 10 → 缺口区 [9.8, 10]，守边=昨收 10
      { o: 9.8, h: 9.85, l: 9.6, c: 9.7, t: '2026-01-02' },
      { o: 9.72, h: 9.8, l: 9.5, c: 9.6, t: '2026-01-03' },
    ])
    const gaps = unfilledGaps(bars, 60, 0.005, 5)
    expect(gaps).toHaveLength(1)
    expect(gaps[0].price).toBe(10)
    expect(gaps[0].title).toBe('缺口 2026-01-02')
    // 回补：后续任一根 [low,high] 覆盖 [9.8, 10]
    const filled = series([
      { h: 10.1, l: 9.9, c: 10 },
      { o: 9.8, h: 9.85, l: 9.6, c: 9.7, t: '2026-01-02' },
      { o: 9.7, h: 10.05, l: 9.75, c: 9.9, t: '2026-01-03' },
    ])
    expect(unfilledGaps(filled, 60, 0.005, 5)).toEqual([])
  })
})

describe('roundStep / roundLevels 整数关口', () => {
  it('步长档：1/5/10/50/100 按价位自适应', () => {
    expect(roundStep(8)).toBe(1)
    expect(roundStep(20)).toBe(5)
    expect(roundStep(80)).toBe(10)
    expect(roundStep(200)).toBe(50)
    expect(roundStep(1700)).toBe(100)
  })

  it('±span 档（不含锚关口本身），价非正剔除', () => {
    const levels = roundLevels(series([{ h: 1720, l: 1680, c: 1702 }]), 3)
    expect(levels.map((l) => l.price)).toEqual([1400, 1500, 1600, 1800, 1900, 2000])
    expect(levels.every((l) => l.lineStyle === 'dotted')).toBe(true)
  })

  it('无有效收盘 → 空', () => {
    expect(roundLevels([bar({ close: null })])).toEqual([])
  })
})

describe('computeKeyLevels 跨组同价去重', () => {
  it('同价条目按 prevHL → pivots → gaps → round 优先级保留一次', () => {
    const bars = series([
      { h: 10, l: 8, c: 9, t: '2026-01-01' },
      { h: 10, l: 9.9, c: 10, t: '2026-01-02' },
    ])
    // prevHL 高=10、低=8；枢轴 P=(10+9.9+10)/3≈9.967，R1≈10.03，S1=2P−10≈9.93
    const groups = computeKeyLevels(bars, { gapLookback: 2, roundSpan: 1 })
    const all = [...groups.prevHL, ...groups.pivots, ...groups.gaps, ...groups.round]
    const prices = all.map((l) => l.price)
    expect(new Set(prices).size).toBe(prices.length)
  })
})
