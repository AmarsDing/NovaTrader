import { describe, expect, it } from 'vitest'
import type { CustomBarItemData, CustomSeriesOptions, Time } from 'lightweight-charts'
import { chartTime } from '@/utils/time'
import { bandKind, createSessionBandPaneView, minuteOfDay, sessionBandData, type BandItem, type BandKind } from '../session-bands'

const t = (text: string): number => {
  const value = chartTime(text, '1m')
  if (typeof value !== 'number') throw new Error(`chartTime(${text}) not numeric`)
  return value
}

describe('竞价时段窗', () => {
  it('chartTime 的 +8h 平移秒 → 上海历当日分钟', () => {
    expect(minuteOfDay(t('2026-10-19 09:18'))).toBe(9 * 60 + 18)
    expect(minuteOfDay(t('2026-10-19 15:00'))).toBe(15 * 60)
    // 日线字符串与非法值不属于任何时段
    expect(minuteOfDay('2026-10-19')).toBeNull()
    expect(minuteOfDay(NaN)).toBeNull()
  })

  it('开盘竞价 09:15–09:25、静默 09:25–09:30、收盘竞价 14:57–15:00（含 15:00 当根）', () => {
    expect(bandKind(t('2026-10-19 09:15'))).toBe('morning')
    expect(bandKind(t('2026-10-19 09:24'))).toBe('morning')
    expect(bandKind(t('2026-10-19 09:25'))).toBe('quiet')
    expect(bandKind(t('2026-10-19 09:29'))).toBe('quiet')
    expect(bandKind(t('2026-10-19 09:30'))).toBeNull()
    expect(bandKind(t('2026-10-19 10:30'))).toBeNull()
    expect(bandKind(t('2026-10-19 14:57'))).toBe('closing')
    expect(bandKind(t('2026-10-19 15:00'))).toBe('closing')
    expect(bandKind('2026-10-19')).toBeNull()
  })

  it('sessionBandData 只保留窗内的根并标注类别', () => {
    const rows = sessionBandData([t('2026-10-19 09:18'), t('2026-10-19 10:30'), t('2026-10-19 14:58'), t('2026-10-19 15:00')])
    expect(rows.map((row) => row.customValues.band)).toEqual(['morning', 'closing', 'closing'])
    expect(rows[0].time).toBe(t('2026-10-19 09:18'))
  })
})

describe('createSessionBandPaneView', () => {
  const colors = () => ({ morning: 'm', quiet: 'q', closing: 'c' })

  it('option 面：不产价格线/末值/十字标，且退出自动缩放', () => {
    const view = createSessionBandPaneView(colors)
    const options = view.defaultOptions()
    expect(options.priceLineVisible).toBe(false)
    expect(options.lastValueVisible).toBe(false)
    const autoscale = options.autoscaleInfoProvider as CustomSeriesOptions['autoscaleInfoProvider']
    expect(typeof autoscale).toBe('function')
    expect(autoscale?.(() => null)).toBeNull()
    expect(view.priceValueBuilder({ time: 0 as unknown as Time, customValues: { band: 'morning' } })).toEqual([])
    expect(view.isWhitespace({ time: 0 as unknown as Time } as BandItem)).toBe(true)
    expect(view.isWhitespace({ time: 0 as unknown as Time, customValues: { band: 'closing' } } as BandItem)).toBe(false)
  })

  it('draw：相邻同类带合并为一条，颜色实时取自 getter', () => {
    const view = createSessionBandPaneView(colors)
    const bars = (
      [
        [10, 'morning'],
        [20, 'morning'],
        [30, 'quiet'],
        [50, 'closing'],
      ] as [number, BandKind][]
    ).map(([x, band]) => ({
      x,
      time: 0,
      originalData: { time: 0 as unknown as Time, customValues: { band } },
      barColor: '',
    })) as CustomBarItemData<Time, BandItem>[]
    view.update({ bars, barSpacing: 10, conflationFactor: 1, visibleRange: null }, view.defaultOptions())

    const fills: { x: number; w: number; h: number; color: string }[] = []
    const ctx = {
      fillStyle: '',
      fillRect(x: number, y: number, w: number, h: number) {
        fills.push({ x, w, h, color: this.fillStyle })
      },
    }
    const target = {
      useMediaCoordinateSpace: <T>(fn: (scope: { context: typeof ctx; mediaSize: { width: number; height: number } }) => T): T =>
        fn({ context: ctx, mediaSize: { width: 800, height: 300 } }),
    }
    // 我们的 renderer 只用 target 单参；按实现签名窄化调用，绕过接口的 3 参要求。
    const renderer = view.renderer() as { draw: (drawTarget: unknown) => void }
    renderer.draw(target)

    expect(fills).toEqual([
      { x: 5, w: 20, h: 300, color: 'm' },
      { x: 25, w: 10, h: 300, color: 'q' },
      { x: 45, w: 10, h: 300, color: 'c' },
    ])
  })
})
