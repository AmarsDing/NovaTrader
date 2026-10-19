import { describe, expect, it } from 'vitest'
import type { ISeriesPrimitive, Time } from 'lightweight-charts'
import { createMeasurePrimitive, measureStats } from '../measure-box'

const rows = [10, 11, 12, 13, 14].map((close) => ({ high: close + 1, low: close - 1, close }))

describe('measureStats：区间涨跌统计', () => {
  it('正向拖拽：起点收盘为基准，区间极值算振幅', () => {
    const stats = measureStats(rows, 1, 3)
    expect(stats).not.toBeNull()
    expect(stats?.bars).toBe(3)
    expect(stats?.delta).toBe(2) // 13 - 11
    expect(stats?.deltaPct).toBeCloseTo((2 / 11) * 100, 6)
    // 区间 high 最大 14（row3.high）、low 最小 10（row1.low）
    expect(stats?.amplitudePct).toBeCloseTo((4 / 11) * 100, 6)
  })

  it('反向拖拽：方向性统计为负，振幅基准仍取起点', () => {
    const stats = measureStats(rows, 3, 1)
    expect(stats?.delta).toBe(-2)
    expect(stats?.deltaPct).toBeCloseTo((-2 / 13) * 100, 6)
    expect(stats?.bars).toBe(3)
    expect(stats?.amplitudePct).toBeCloseTo((4 / 13) * 100, 6)
  })

  it('索引越界收敛到数据两端；起点无收盘或空数据 → null', () => {
    expect(measureStats(rows, 0, 99)?.bars).toBe(5)
    expect(measureStats([], 0, 1)).toBeNull()
    expect(measureStats([{ high: 2, low: 1, close: null }, { high: 3, low: 2, close: 2 }], 0, 1)).toBeNull()
    expect(measureStats(rows, 2, 2)?.bars).toBe(1)
  })
})

describe('createMeasurePrimitive', () => {
  const style = () => ({ fill: 'fill-color', stroke: 'stroke-color' })

  function fakeAttach(prim: ISeriesPrimitive<Time>, scale: { logicalToCoordinate?: (l: number) => number | null } = {}) {
    prim.attached?.({
      chart: {
        timeScale: () => ({ logicalToCoordinate: scale.logicalToCoordinate ?? ((logical: number) => logical * 10) }),
      },
      series: { priceToCoordinate: (value: number) => (20 - value) * 10 },
      requestUpdate: () => {},
      horzScaleBehavior: {},
    } as unknown as Parameters<NonNullable<ISeriesPrimitive<Time>['attached']>>[0])
  }

  it('draw：半透明填充 + 半像素描边，几何来自 logical/price 坐标换算', () => {
    const prim = createMeasurePrimitive(style)
    fakeAttach(prim)
    prim.setBox({ fromLogical: 1, toLogical: 3, fromPrice: 10, toPrice: 14 })

    const ops: { kind: string; args: number[]; color: string }[] = []
    const ctx = {
      fillStyle: '',
      strokeStyle: '',
      lineWidth: 0,
      fillRect(...args: number[]) { ops.push({ kind: 'fill', args, color: this.fillStyle }) },
      strokeRect(...args: number[]) { ops.push({ kind: 'stroke', args, color: this.strokeStyle }) },
    }
    const target = {
      useMediaCoordinateSpace: <T>(fn: (scope: { context: typeof ctx }) => T): T => fn({ context: ctx }),
    }
    const view = prim.paneViews?.()[0]
    const renderer = view?.renderer?.() as { draw: (drawTarget: unknown) => void } | null
    renderer?.draw(target)

    // x: 1*10..3*10 → (10,30)；y: 14→60、10→100 → rect y=60 h=40；描边外扩 0.5。
    expect(ops).toEqual([
      { kind: 'fill', args: [10, 60, 20, 40], color: 'fill-color' },
      { kind: 'stroke', args: [10.5, 60.5, 20, 40], color: 'stroke-color' },
    ])
  })

  it('hitTest：框内 range 级命中，框外与出屏框返回 null', () => {
    const prim = createMeasurePrimitive(style)
    fakeAttach(prim)
    prim.setBox({ fromLogical: 1, toLogical: 3, fromPrice: 10, toPrice: 14 })
    const hit = prim.hitTest?.(20, 80)
    expect(hit).toMatchObject({ externalId: 'measure-box', hitTestPriority: 0, zOrder: 'normal' })
    expect(prim.hitTest?.(5, 5)).toBeNull()

    // 逻辑坐标越出可视区（null）→ 不画也不命中。
    const offscreen = createMeasurePrimitive(style)
    fakeAttach(offscreen, { logicalToCoordinate: () => null })
    offscreen.setBox({ fromLogical: 1, toLogical: 3, fromPrice: 10, toPrice: 14 })
    expect(offscreen.hitTest?.(20, 80)).toBeNull()
    const pane = offscreen.paneViews?.()[0]
    const renderer = pane?.renderer?.() as { draw: (t: unknown) => void } | null
    const ctx = { fillStyle: '', strokeStyle: '', lineWidth: 0, fillRect: () => { throw new Error('不应绘制') }, strokeRect: () => { throw new Error('不应绘制') } }
    expect(() => renderer?.draw({ useMediaCoordinateSpace: <T>(fn: (s: { context: typeof ctx }) => T): T => fn({ context: ctx }) })).not.toThrow()
  })

  it('detached 复位：清框清引用，setBox 不再生效', () => {
    const prim = createMeasurePrimitive(style)
    fakeAttach(prim)
    prim.setBox({ fromLogical: 1, toLogical: 2, fromPrice: 10, toPrice: 11 })
    expect(prim.box()).not.toBeNull()
    prim.detached?.()
    expect(prim.box()).toBeNull()
    prim.setBox({ fromLogical: 1, toLogical: 2, fromPrice: 10, toPrice: 11 })
    expect(prim.hitTest?.(15, 95)).toBeNull()
  })
})
