/**
 * 框选测量：项目首个 ISeriesPrimitive 落地件。
 * 用户在图上拖出一个「逻辑索引 × 价格」矩形，primitive 只负责画框
 * （半透明填充 + 单像素描边），统计读数由组件侧 overlay 呈现。
 * 框用**逻辑索引**存储而非时间戳：缩放/平移后矩形仍贴在原来的那批 K 线上。
 */
import {
  type IChartApiBase,
  type IPrimitivePaneRenderer,
  type IPrimitivePaneView,
  type ISeriesApi,
  type ISeriesPrimitive,
  type Logical,
  type PrimitiveHoveredItem,
  type SeriesAttachedParameter,
  type Time,
} from 'lightweight-charts'

/** 测量框：逻辑索引区间 × 价格区间（不保证有序，读取时自归一）。 */
export interface MeasureBox {
  fromLogical: number
  toLogical: number
  fromPrice: number
  toPrice: number
}

export interface MeasureStyle {
  fill: string
  stroke: string
}

export interface MeasureStats {
  bars: number
  delta: number
  deltaPct: number
  amplitudePct: number
}

interface MeasureRow {
  high?: number | null
  low?: number | null
  close?: number | null
}

/**
 * 区间涨跌统计（方向性）：基准=拖拽起点的收盘、终点=松手的收盘；
 * 振幅=区间极值差/起点收盘。行数组下标即主图的逻辑索引。
 */
export function measureStats(rows: readonly MeasureRow[], from: number, to: number): MeasureStats | null {
  if (!rows.length) return null
  const clamp = (value: number) => Math.min(Math.max(value, 0), rows.length - 1)
  const first = clamp(Math.min(from, to))
  const last = clamp(Math.max(from, to))
  const base = rows[clamp(from)].close
  const target = rows[clamp(to)].close
  if (base == null || base <= 0 || target == null) return null
  let high = -Infinity
  let low = Infinity
  for (let i = first; i <= last; i += 1) {
    const rowHigh = rows[i].high
    const rowLow = rows[i].low
    if (typeof rowHigh === 'number' && Number.isFinite(rowHigh)) high = Math.max(high, rowHigh)
    if (typeof rowLow === 'number' && Number.isFinite(rowLow)) low = Math.min(low, rowLow)
  }
  return {
    bars: last - first + 1,
    delta: target - base,
    deltaPct: ((target - base) / base) * 100,
    amplitudePct: high > low ? ((high - low) / base) * 100 : 0,
  }
}

export interface MeasurePrimitive extends ISeriesPrimitive<Time> {
  setBox(box: MeasureBox | null): void
  box(): MeasureBox | null
}

/**
 * 创建测量框 primitive。颜色经 getter 实时取（主题切换零重建）。
 * 框的角超出可视区时 logicalToCoordinate/priceToCoordinate 返回 null，
 * 整框跳过绘制——即「滚出去就不画」，不做半框裁剪。
 */
export function createMeasurePrimitive(style: () => MeasureStyle): MeasurePrimitive {
  let chart: IChartApiBase<Time> | null = null
  let series: ISeriesApi<'Candlestick', Time> | null = null
  let requestUpdate: () => void = () => {}
  let measureBox: MeasureBox | null = null

  interface Bounds {
    x: number
    y: number
    w: number
    h: number
  }

  function bounds(): Bounds | null {
    if (!chart || !series || !measureBox) return null
    const left = Math.min(measureBox.fromLogical, measureBox.toLogical)
    const right = Math.max(measureBox.fromLogical, measureBox.toLogical)
    // MeasureBox 以纯 number 携带逻辑索引；lwc 的 Logical 是品牌类型，换回坐标处恢复。
    const x1 = chart.timeScale().logicalToCoordinate(left as Logical)
    const x2 = chart.timeScale().logicalToCoordinate(right as Logical)
    const y1 = series.priceToCoordinate(measureBox.fromPrice)
    const y2 = series.priceToCoordinate(measureBox.toPrice)
    if (x1 == null || x2 == null || y1 == null || y2 == null) return null
    return { x: x1, y: Math.min(y1, y2), w: x2 - x1, h: Math.abs(y2 - y1) }
  }

  const renderer: IPrimitivePaneRenderer = {
    draw(target) {
      const rect = bounds()
      if (!rect) return
      const { fill, stroke } = style()
      target.useMediaCoordinateSpace(({ context: ctx }) => {
        ctx.fillStyle = fill
        ctx.fillRect(rect.x, rect.y, rect.w, rect.h)
        // 半像素对齐：1px 描边落在像素网格正中，不发虚。
        ctx.strokeStyle = stroke
        ctx.lineWidth = 1
        ctx.strokeRect(rect.x + 0.5, rect.y + 0.5, rect.w, rect.h)
      })
    },
  }
  const paneView: IPrimitivePaneView = { renderer: () => renderer }

  return {
    attached(param: SeriesAttachedParameter<Time>) {
      chart = param.chart as IChartApiBase<Time>
      series = param.series as unknown as ISeriesApi<'Candlestick', Time>
      requestUpdate = param.requestUpdate
    },
    detached() {
      chart = null
      series = null
      measureBox = null
      requestUpdate = () => {}
    },
    paneViews: () => [paneView],
    hitTest: (x: number, y: number): PrimitiveHoveredItem | null => {
      const rect = bounds()
      if (!rect) return null
      const inside = x >= rect.x && x <= rect.x + rect.w && y >= rect.y && y <= rect.y + rect.h
      if (!inside) return null
      // range 级命中（priority 0）：不抢 K 线十字线的点/线级语义。
      return { distance: 0, hitTestPriority: 0, externalId: 'measure-box', cursorStyle: 'default', zOrder: 'normal' }
    },
    setBox(next: MeasureBox | null) {
      measureBox = next
      requestUpdate()
    },
    box: () => measureBox,
  }
}
