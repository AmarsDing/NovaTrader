/**
 * kline-transform（W6c；UI 宪法 R-UI-50/51）合同测试：
 * - adjustBar：qfq 价×factor / hfq 价÷factor / none 不动；非法 factor（0/负/NaN）原样；量不变；不改入参；
 * - adjustBars：逐根生效且保序；
 * - resampleBars：OHLC 标准聚合（首 open/末 close/max/min）、volume 求和、桶起点整点对齐、
 *   空输入→[]、无数据段不补零桶、跨 60 分钟桶不串桶；
 * - withCostLine：未持仓不显线、同价替换占位、成本线居首实线；
 * - aggregateMarkers（W6b）：≤maxCount 原样拷贝、密集分桶每桶每 side 各留最早一条、
 *   首末恒保留、maxCount<=0 视为 1、缺 side 不分层、不改入参。
 */
import { describe, expect, it } from 'vitest'
import { adjustBar, adjustBars, aggregateMarkers, resampleBars, withCostLine, type BarLike } from '../kline-transform'

const bar: BarLike = { time: 1_700_000_000, open: 10, high: 11, low: 9, close: 10.5, volume: 100 }

describe('adjustBar 复权', () => {
  it('qfq：价 × factor，量不变', () => {
    const out = adjustBar(bar, 0.9, 'qfq')
    expect(out.open).toBeCloseTo(9)
    expect(out.high).toBeCloseTo(9.9)
    expect(out.low).toBeCloseTo(8.1)
    expect(out.close).toBeCloseTo(9.45)
    expect(out.volume).toBe(100)
  })

  it('hfq：价 ÷ factor', () => {
    const out = adjustBar(bar, 0.5, 'hfq')
    expect(out.close).toBeCloseTo(21)
    expect(out.volume).toBe(100)
  })

  it('none / 非法 factor（0/负/NaN）原样返回（缺失不复权，不回填 0/1）', () => {
    expect(adjustBar(bar, 0.5, 'none')).toBe(bar)
    expect(adjustBar(bar, 0, 'qfq')).toBe(bar)
    expect(adjustBar(bar, -1, 'qfq')).toBe(bar)
    expect(adjustBar(bar, Number.NaN, 'hfq')).toBe(bar)
  })

  it('不改入参（纯函数）', () => {
    const src = { ...bar }
    adjustBar(src, 0.8, 'qfq')
    expect(src).toEqual(bar)
  })
})

describe('adjustBars', () => {
  it('逐根生效且保序', () => {
    const src = [bar, { ...bar, time: bar.time + 60, close: 20 }]
    const out = adjustBars(src, 0.5, 'qfq')
    expect(out).toHaveLength(2)
    expect(out[0].close).toBeCloseTo(5.25)
    expect(out[1].close).toBeCloseTo(10)
    expect(out[1].time).toBe(src[1].time)
  })
})

describe('resampleBars 周期聚合', () => {
  const t0 = 1_700_000_100 // 非整桶起点
  const base = Math.floor(t0 / 300) * 300
  const minute = (i: number, o: number, h: number, l: number, c: number, v: number): BarLike => ({
    time: base + i * 60,
    open: o,
    high: h,
    low: l,
    close: c,
    volume: v,
  })

  it('5 根 1 分钟 → 1 根 5 分钟：OHLC 标准聚合 + 量求和 + 桶起点对齐', () => {
    const src = [
      minute(0, 10, 10.2, 9.9, 10.1, 100),
      minute(1, 10.1, 10.5, 10, 10.4, 200),
      minute(2, 10.4, 10.6, 10.3, 10.2, 300),
      minute(3, 10.2, 10.3, 9.8, 9.9, 400),
      minute(4, 9.9, 10.1, 9.85, 10.0, 500),
    ]
    const out = resampleBars(src, 5)
    expect(out).toHaveLength(1)
    expect(out[0].time).toBe(base)
    expect(out[0].open).toBe(10)
    expect(out[0].high).toBe(10.6)
    expect(out[0].low).toBe(9.8)
    expect(out[0].close).toBe(10.0)
    expect(out[0].volume).toBe(1500)
  })

  it('跨桶不串：6 根 → 两个桶（第二桶单根）', () => {
    const src = [0, 1, 2, 3, 4, 5].map((i) => minute(i, 10, 10, 10, 10 + i, 10))
    const out = resampleBars(src, 5)
    expect(out).toHaveLength(2)
    expect(out[0].close).toBe(14)
    expect(out[1].close).toBe(15)
    expect(out[1].time).toBe(base + 300)
  })

  it('空输入 → []；无数据段不补零桶', () => {
    expect(resampleBars([], 15)).toEqual([])
    // 中间空 10 分钟：只有两个有数据的桶，不插空桶。
    const src = [minute(0, 10, 10, 10, 10, 1), minute(10, 11, 11, 11, 11, 1)]
    const out = resampleBars(src, 5)
    expect(out).toHaveLength(2)
    expect(out.map((b) => b.time)).toEqual([base, base + 600])
  })

  it('60 分钟桶：整点对齐', () => {
    const hour = Math.floor(t0 / 3600) * 3600
    const src = [
      { time: hour, open: 10, high: 10, low: 10, close: 10, volume: 1 },
      { time: hour + 3540, open: 10, high: 12, low: 9, close: 11, volume: 2 },
      { time: hour + 3600, open: 11, high: 11, low: 11, close: 11, volume: 3 },
    ]
    const out = resampleBars(src, 60)
    expect(out).toHaveLength(2)
    expect(out[0].high).toBe(12)
    expect(out[0].close).toBe(11)
    expect(out[0].volume).toBe(3)
    expect(out[1].time).toBe(hour + 3600)
  })
})

describe('withCostLine 持仓成本线', () => {
  it('未持仓（null/价非正）不产出成本线，levels 原样', () => {
    const levels = [{ price: 9, title: '止损' }]
    expect(withCostLine(levels, null)).toEqual(levels)
    expect(withCostLine(levels, { price: 0 })).toEqual(levels)
    expect(withCostLine(levels, { price: -1 })).toEqual(levels)
    expect(withCostLine(levels, { price: Number.NaN })).toEqual(levels)
  })

  it('有持仓：成本线居首、实线、默认标题「成本」、颜色可覆盖', () => {
    const out = withCostLine([{ price: 9, title: '止损' }], { price: 10 }, '#0ff')
    expect(out).toHaveLength(2)
    expect(out[0]).toMatchObject({ price: 10, title: '成本', lineStyle: 'solid', color: '#0ff' })
    expect(out[1].title).toBe('止损')
    expect(withCostLine([], { price: 10, title: '持仓成本' })[0].title).toBe('持仓成本')
  })

  it('与既有 levels 同价：成本线替换占位（不双线），且不改入参', () => {
    const levels = [{ price: 10, title: '止盈' }, { price: 9 }]
    const out = withCostLine(levels, { price: 10 })
    expect(out.filter((l) => l.price === 10)).toHaveLength(1)
    expect(out[0].title).toBe('成本')
    expect(levels[0].title).toBe('止盈')
  })
})

describe('aggregateMarkers 逐笔采样', () => {
  /** 生成 time 从 0 递增、side 交替的 marker 序列。 */
  const dense = (n: number) =>
    Array.from({ length: n }, (_, i) => ({ time: i, side: i % 2 === 0 ? 'buy' : 'sell' }))

  it('空输入 → []', () => {
    expect(aggregateMarkers([], 10)).toEqual([])
  })

  it('数量 ≤ maxCount 原样带走（同序、同引用）', () => {
    const marks = dense(6)
    const out = aggregateMarkers(marks, 6)
    expect(out).toEqual(marks)
    expect(out).not.toBe(marks) // 新数组，调用方改动不回写
    expect(out[0]).toBe(marks[0])
  })

  it('密集成交按桶采样：输出严格少于输入，且时间升序', () => {
    const out = aggregateMarkers(dense(200), 10)
    expect(out.length).toBeLessThan(200)
    expect(out.length).toBeGreaterThan(0)
    const times = out.map((m) => m.time)
    expect(times).toEqual([...times].sort((a, b) => a - b))
  })

  it('同桶一进一出不互吞：买入与卖出各自保留', () => {
    // maxCount=2 → 2 个桶、宽 3.5；t1 sell 与 t2 buy 同落桶 0，两条都要在。
    const out = aggregateMarkers(dense(8), 2)
    const times = out.map((m) => m.time)
    expect(times).toContain(1)
    expect(times).toContain(2)
    expect(out.filter((m) => m.time > 0 && m.time < 3.5).map((m) => m.side)).toEqual(['sell', 'buy'])
  })

  it('首末两条恒保留（边缘再密也不丢）', () => {
    const marks = dense(50)
    const out = aggregateMarkers(marks, 3)
    expect(out[0]).toBe(marks[0])
    expect(out[out.length - 1]).toBe(marks[marks.length - 1])
  })

  it('maxCount<=0 视为 1：单桶每 side 各留最早一条', () => {
    const marks = dense(5) // t0..t4，buy/sell 交替
    const out = aggregateMarkers(marks, 0)
    expect(out.map((m) => m.time)).toEqual([0, 1, 2, 4]) // 首 t0 + 最早 sell t1 + 最早 buy t2 + 末 t4
    expect(aggregateMarkers(marks, -3).map((m) => m.time)).toEqual([0, 1, 2, 4])
  })

  it('缺失 side 视为同组（不做方向分层）', () => {
    const marks = Array.from({ length: 12 }, (_, i) => ({ time: i }))
    const out = aggregateMarkers(marks, 2)
    // 2 桶各留最早一条 + 首末。
    expect(out.map((m) => m.time)).toEqual([0, 1, 6, 11])
  })

  it('不改入参（纯函数）', () => {
    const marks = dense(30)
    const snapshot = marks.map((m) => ({ ...m }))
    aggregateMarkers(marks, 4)
    expect(marks).toEqual(snapshot)
  })
})
