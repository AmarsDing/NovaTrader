/**
 * 集合竞价时段底色 custom series（v5 `addCustomSeries`）。
 * 沪深两市盘前集合竞价 09:15–09:25、静默报单 09:25–09:30、收盘集合竞价 14:57–15:00，
 * 在 1m K 线之下整高铺色带。数据点只放「落在时段窗内的根」，
 * 时段外的根不进序列，天然不需要空白占位。
 */
import {
  customSeriesDefaultOptions,
  type CustomBarItemData,
  type CustomData,
  type CustomSeriesOptions,
  type CustomSeriesWhitespaceData,
  type ICustomSeriesPaneRenderer,
  type ICustomSeriesPaneView,
  type ISeriesApi,
  type PaneRendererCustomData,
  type Time,
  type WhitespaceData,
} from 'lightweight-charts'

export type BandKind = 'morning' | 'quiet' | 'closing'

export interface BandItem extends CustomData<Time> {
  customValues: { band: BandKind }
}

export type BandSeriesApi = ISeriesApi<'Custom', Time, BandItem | WhitespaceData<Time>, CustomSeriesOptions>

/** [from, to) 当日分钟区间；09:25–09:30 可报不可撤（静默），收盘竞价含 15:00 当根。 */
const WINDOWS: readonly { from: number; to: number; kind: BandKind }[] = [
  { from: 9 * 60 + 15, to: 9 * 60 + 25, kind: 'morning' },
  { from: 9 * 60 + 25, to: 9 * 60 + 30, kind: 'quiet' },
  { from: 14 * 60 + 57, to: 15 * 60 + 1, kind: 'closing' },
]

/**
 * K 线时间值里的「当日分钟」（上海历，chartTime 已按 +8h 平移，取 mod 即墙上时间）。
 * 日线周期的字符串时间一律返回 null——色带只在分钟周期有意义。
 */
export function minuteOfDay(time: string | number): number | null {
  if (typeof time !== 'number' || !Number.isFinite(time)) return null
  return Math.floor((((time % 86400) + 86400) % 86400) / 60)
}

/** 该时间值落在哪个竞价时段窗；不在任何窗 → null。 */
export function bandKind(time: string | number): BandKind | null {
  const minute = minuteOfDay(time)
  if (minute == null) return null
  const window = WINDOWS.find((item) => minute >= item.from && minute < item.to)
  return window ? window.kind : null
}

/** bars 时间序列 → 色带数据：只保留落在时段窗内的根，并标上时段类别。 */
export function sessionBandData(times: readonly (string | number)[]): BandItem[] {
  const out: BandItem[] = []
  times.forEach((time) => {
    const band = bandKind(time)
    if (band) out.push({ time: time as Time, customValues: { band } })
  })
  return out
}

export interface BandColors {
  morning: string
  quiet: string
  closing: string
}

/**
 * 创建色带 pane view。颜色在 draw 时实时读 getter——主题切换不用重建序列，
 * 每次重绘自动吃到新 token 值。
 */
export function createSessionBandPaneView(colors: () => BandColors): ICustomSeriesPaneView<Time, BandItem> {
  let bars: readonly CustomBarItemData<Time, BandItem>[] = []
  let spacing = 0
  let factor = 1
  const renderer: ICustomSeriesPaneRenderer = {
    draw(target) {
      // 相邻同类色带合并成一条，减少 fillRect 次数。
      target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
        let start = 0
        while (start < bars.length) {
          const band = bars[start].originalData.customValues?.band
          let end = start + 1
          while (end < bars.length && bars[end].originalData.customValues?.band === band) end += 1
          const half = (spacing * factor) / 2
          ctx.fillStyle = colors()[band as BandKind]
          ctx.fillRect(bars[start].x - half, 0, bars[end - 1].x - bars[start].x + half * 2, mediaSize.height)
          start = end
        }
      })
    },
  }
  return {
    renderer: () => renderer,
    update(data: PaneRendererCustomData<Time, BandItem>) {
      bars = data.bars
      spacing = data.barSpacing
      factor = data.conflationFactor
    },
    // 背景色带：无价格语义，返回空价格集，既不参与自动缩放也不进十字线取值。
    priceValueBuilder: () => [],
    isWhitespace: (data: BandItem | CustomSeriesWhitespaceData<Time>): data is CustomSeriesWhitespaceData<Time> =>
      (data as BandItem).customValues?.band == null,
    defaultOptions: (): CustomSeriesOptions => ({
      ...customSeriesDefaultOptions,
      color: 'transparent',
      priceLineVisible: false,
      lastValueVisible: false,
      autoscaleInfoProvider: () => null,
    }),
  }
}
