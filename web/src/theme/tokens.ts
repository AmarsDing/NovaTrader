/**
 * 视觉令牌 TS 镜像（theme/tokens.css 的脚本侧读取）。
 * ECharts / lightweight-charts / SVG 图表的颜色从这里取，禁止硬编码第二份调色板。
 */

export const PALETTE = {
  bg0: '#0a0e17',
  bg1: '#0e1220',
  bg2: '#131828',
  bg3: '#1a2136',
  border1: 'rgba(255,255,255,0.07)',
  border2: 'rgba(255,255,255,0.14)',
  text1: 'rgba(255,255,255,0.92)',
  text2: 'rgba(255,255,255,0.58)',
  text3: 'rgba(255,255,255,0.38)',
  accent: '#00e5ff',
  accent2: '#7b61ff',
  warn: '#ff6b00',
  danger: '#ff3a3a',
  success: '#00d084',
  info: '#4c9aff',
  up: '#ff4d4d', // 红涨
  down: '#00d084', // 绿跌
} as const

/** ECharts 文字/轴线公共配置。 */
export const CHART_TEXT_STYLE = {
  color: PALETTE.text2,
  fontFamily: "'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif",
  fontSize: 11,
} as const
