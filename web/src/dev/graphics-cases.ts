/**
 * P2-12 viz 图形回归用例登记（仅 DEV）。
 * /dev/gallery?case=<id> 进入单用例定格态：只渲染该用例的 section，关闭全部动态源
 * （S6 脉冲定时器、画布脉冲/环境流向点、Modal 自动打开），供 Playwright 固定视口截图。
 * 矩阵 = 本表用例 × 主题（dark/light）× 密度（normal/compact），golden 命名
 * <case>.<theme>.<density>.png，由 web/graphics/gallery.spec.ts 驱动。
 *
 * 登记即是防回归合同：新增走查 section 顺手登记一行，截图面自动跟进；
 * 尺寸 w = 用例定格态宽度（px），选「内容自然排布的最小整宽」，宁宽勿紧（防换行抖动）。
 */
export interface GraphicsCase {
  id: string
  /** 走查说明（spec 名 + 截图标题同源）。 */
  title: string
  /** 定格态内容框宽度 px。 */
  w: number
}

export const GRAPHICS_CASES: GraphicsCase[] = [
  { id: 'tokens', title: '设计令牌', w: 1000 },
  { id: 'buttons', title: '按钮', w: 760 },
  { id: 'forms', title: '表单', w: 1000 },
  { id: 'feedback', title: '徽标 / 状态 / 反馈', w: 760 },
  { id: 'icons', title: '图标全集', w: 1000 },
  { id: 'table', title: '表格', w: 960 },
  { id: 'viz-svg', title: 'viz·轻量 SVG', w: 720 },
  { id: 'kline', title: 'NtKline 40 根日线', w: 980 },
  { id: 'equity', title: 'NtEquityCurve', w: 980 },
  { id: 'sector-bar', title: 'NtSectorBar', w: 520 },
  { id: 'heatmap', title: 'NtHeatmap', w: 520 },
  { id: 'indicator', title: 'NtIndicatorChart', w: 980 },
  { id: 'depth', title: 'NtDepthBoard×3（实盘/合成/封板）', w: 1000 },
  { id: 'display-cards', title: 'display·StatCard', w: 1000 },
  { id: 'display-lists', title: 'display·Timeline/Funnel/Ladder', w: 1000 },
  { id: 'monitor-band-hud', title: '监控 S1 时段带/HUD', w: 1000 },
  { id: 'monitor-activity', title: '监控 S2 活动条', w: 1000 },
  { id: 'monitor-stage', title: '监控画布 S3 静态场景', w: 980 },
  { id: 'monitor-flow', title: '监控 S6 束线/急停（定格）', w: 980 },
  { id: 'terminal-tape', title: '监控 S4 终端带·tape', w: 1000 },
  { id: 'terminal-positions', title: '监控 S4 终端带·持仓', w: 1000 },
  { id: 'deep-sheets', title: '监控 S5 详情窗六牌', w: 1000 },
  { id: 'modal', title: 'NtModal', w: 1100 },
]

const BY_ID = new Map(GRAPHICS_CASES.map((item) => [item.id, item]))

export function graphicsCaseOf(id: unknown): GraphicsCase | null {
  return typeof id === 'string' ? BY_ID.get(id) ?? null : null
}
