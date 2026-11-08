/**
 * 盘中情绪路径图 option 构建（纯函数，可单测；viz/NtEmotionPath.vue 的 option 唯一出处）：
 * 涨停/跌停/炸板三线 + 竞价截面散点标记。
 * 色值全部来自入参 palette（脚本侧颜色唯一来源 theme/tokens.ts，PALETTE/useChartPalette 取）；
 * 本文件禁写任何颜色字面量（ui-literals 守护扫描 viz/）。
 */
import type { EChartsCoreOption } from 'echarts/core'
import type { EmotionPathPoint } from '@/api/market'
import type { Palette } from '@/theme/tokens'
import { formatMinute } from '@/utils/time'

export function buildEmotionPathOption(points: EmotionPathPoint[], p: Palette): EChartsCoreOption {
  const labels = points.map((pt) => formatMinute(pt.time) || pt.time)
  // 竞价截面（09:25 占位点）用 accent 散点单独标出——它是集合竞价的落锤读数，与盘中截面语义不同。
  const auctionDots = points
    .map((pt, i) => (pt.auction ? { coord: [labels[i]!, pt.up], value: pt.up } : null))
    .filter((d): d is { coord: [string, number]; value: number } => d !== null)
  return {
    color: [p.up, p.down, p.warn],
    grid: { left: 40, right: 14, top: 28, bottom: 24 },
    legend: {
      top: 0,
      data: ['涨停', '跌停', '炸板'],
      textStyle: { color: p.text3, fontSize: 11 },
      itemWidth: 14,
      itemHeight: 8,
    },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: labels,
      boundaryGap: false,
      axisLine: { lineStyle: { color: p.border1 } },
      axisTick: { show: false },
      axisLabel: { color: p.text3, fontSize: 10 },
    },
    yAxis: {
      type: 'value',
      minInterval: 1,
      splitLine: { lineStyle: { color: p.border1 } },
      axisLabel: { color: p.text3, fontSize: 10 },
    },
    series: [
      { name: '涨停', type: 'line', data: points.map((pt) => pt.up), symbol: 'none', lineStyle: { width: 1.6 } },
      { name: '跌停', type: 'line', data: points.map((pt) => pt.down), symbol: 'none', lineStyle: { width: 1.6 } },
      { name: '炸板', type: 'line', data: points.map((pt) => pt.broken), symbol: 'none', lineStyle: { width: 1.2, type: 'dashed' } },
      {
        name: '竞价',
        type: 'scatter',
        data: auctionDots,
        symbolSize: 7,
        itemStyle: { color: p.accent },
        tooltip: { show: false },
        z: 3,
      },
    ],
  }
}
