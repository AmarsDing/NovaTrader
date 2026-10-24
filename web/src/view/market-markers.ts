/**
 * K 线买卖标记调色的唯一来源（双色制定案）：买=up 红、卖=down 绿、方向未知=中性 text3。
 * 与全站红涨绿跌同一 token 对（--nt-up/--nt-down 四主题块钉档，color-vision 硬门禁管辖），
 * 不另起 buy/sell 别名防第二来源；方向未知不默认红（对齐 GalleryView:1127「认不出来用中性色」口径）。
 * 管辖范围=markers 调色板键（PALETTE key）；确认单/总览/信号页的方向角标走 CSS class（up/down，
 * 未知='' 继承正文色）的类名约定，与本映射并行不悖，双方均遵守「未知方向不默认红」。
 */
export type FillSideTone = 'up' | 'down' | 'text3'

export function fillSideTone(side: string | null | undefined): FillSideTone {
  if (side === 'sell') return 'down'
  if (side === 'buy') return 'up'
  return 'text3'
}
