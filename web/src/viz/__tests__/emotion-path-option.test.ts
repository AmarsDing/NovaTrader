/**
 * 情绪路径 option 构建器契约：
 * - 三线（涨停/跌停/炸板）色序 = up/down/warn（palette 注入，禁字面量）；
 * - 竞价截面点单独 accent 散点系列，不进三线；
 * - 时间轴标签走 formatMinute（上海时区 HH:mm）；
 * - 图例只留三线（竞价是标记不是线）。
 */
import { describe, expect, it } from 'vitest'
import type { EmotionPathPoint } from '@/api/market'
import type { Palette } from '@/theme/tokens'
import { buildEmotionPathOption } from '@/viz/emotion-path-option'

const palette = {
  bg0: '', bg1: '', bg2: '', bg3: '', border1: 'B1', border2: '', text0: '', text1: 'T1', text2: 'T2', text3: 'T3',
  accent: 'ACC', accent2: '', warn: 'WRN', danger: '', success: '', info: '', up: 'UP', down: 'DOWN',
} as unknown as Palette

function point(time: string, auction: boolean, up: number): EmotionPathPoint {
  return {
    time,
    auction,
    up,
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

const points = [
  point('2026-11-08T09:25:00+08:00', true, 12),
  point('2026-11-08T09:35:00+08:00', false, 20),
  point('2026-11-08T09:40:00+08:00', false, 26),
]

describe('buildEmotionPathOption', () => {
  it('三线 + 竞价散点，色序 up/down/warn 且竞价标 accent', () => {
    const option = buildEmotionPathOption(points, palette)
    expect(option.color).toEqual(['UP', 'DOWN', 'WRN'])
    const series = option.series as Array<{ name: string; type: string; data: unknown[] }>
    expect(series.map((s) => s.name)).toEqual(['涨停', '跌停', '炸板', '竞价'])
    expect(series[0]!.data).toEqual([12, 20, 26])
    expect(series[1]!.data).toEqual([2, 2, 2])
    expect(series[2]!.data).toEqual([1, 1, 1])
    // 竞价散点只标 auction 截面（09:25 一格）。
    expect(series[3]!.data).toEqual([{ coord: ['09:25', 12], value: 12 }])
  })

  it('时间轴标签为上海 HH:mm；图例只含三线', () => {
    const option = buildEmotionPathOption(points, palette)
    const xAxis = option.xAxis as { data: string[] }
    expect(xAxis.data).toEqual(['09:25', '09:35', '09:40'])
    const legend = option.legend as { data: string[] }
    expect(legend.data).toEqual(['涨停', '跌停', '炸板'])
  })
})
