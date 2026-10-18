/**
 * G6 交易时段桌面包：超短线一天四种工作面的一键切换（抄 gloomberb layout/desks.ts）。
 * 纪律：
 * - desk = 面板实例 + 参数 + 落位（ref/direction，与 DockHost DEFAULT_LAYOUT 同一形态；
 *   落位参照必须先于依赖者出现，addPanel 按序引用）；
 * - 套用 desk 等价于顺序执行一组面板命令：DockHost 清空后重建，参数走 panel-bus，
 *   不手写 dockview toJSON —— 布局结构永远走验证过的构建管线；
 * - 切桌销毁浮窗（整张票换工作面）；desk 不开 popout，trade/settings/ops 禁弹约束天然成立；
 * - Shift+1..4 快速切桌（event.code 判定，与键盘布局无关）。
 */
import { getPanel } from './panel-registry'

export interface DeskPanelSpec {
  id: string
  /** 开面板参数（panel-bus 通道，如 { symbol: '000001' }）。 */
  params?: Record<string, unknown>
  /** 落位参照面板（必须先于本项出现；缺省首项独占 dock）。 */
  ref?: string
  direction?: 'right' | 'below' | 'within'
}

export interface DeskDef {
  id: string
  title: string
  /** Shift+N 的 N（全局唯一，1..4）。 */
  hotkey: number
  description: string
  panels: DeskPanelSpec[]
  /** 套用后激活的面板（必须在 panels 内）。 */
  focus: string
}

export const DESKS: DeskDef[] = [
  {
    id: 'preopen',
    title: '盘前准备',
    hotkey: 1,
    description: '晨报 · 数据源健康 · 股池黑名单',
    panels: [
      { id: 'morning-briefing' },
      { id: 'datasource-health', ref: 'morning-briefing', direction: 'right' },
      { id: 'pool-manager', ref: 'datasource-health', direction: 'within' },
    ],
    focus: 'morning-briefing',
  },
  {
    id: 'auction',
    title: '竞价监控',
    hotkey: 2,
    description: '行情（预绑 000001）· 涨停梯队 · 自选扫描',
    panels: [
      { id: 'market', params: { symbol: '000001' } },
      { id: 'limit-ladder', ref: 'market', direction: 'right' },
      { id: 'watchlist', ref: 'limit-ladder', direction: 'within' },
    ],
    focus: 'market',
  },
  {
    id: 'trading',
    title: '打板作战',
    hotkey: 3,
    description: '行情 · 信号 · 交易 · 风控链',
    panels: [
      { id: 'market' },
      { id: 'signals', ref: 'market', direction: 'right' },
      { id: 'risk-chain', ref: 'signals', direction: 'within' },
      { id: 'trade', ref: 'market', direction: 'below' },
    ],
    focus: 'signals',
  },
  {
    id: 'review',
    title: '盘后复盘',
    hotkey: 4,
    description: '回测 · 影子进化 · 风控日报',
    panels: [
      { id: 'backtest' },
      { id: 'evolution', ref: 'backtest', direction: 'right' },
      { id: 'risk-report', ref: 'evolution', direction: 'within' },
    ],
    focus: 'backtest',
  },
]

const byId = new Map(DESKS.map((d) => [d.id, d]))
const byHotkey = new Map(DESKS.map((d) => [d.hotkey, d]))

export function getDesk(id: string): DeskDef | undefined {
  return byId.get(id)
}

export function deskByHotkey(n: number): DeskDef | undefined {
  return byHotkey.get(n)
}

/** Shift+数字 → 桌；非桌键（无 shift、带 Ctrl/Meta/Alt、未占用数字）返回 null。 */
export function matchDeskHotkey(event: KeyboardEvent): DeskDef | null {
  if (!event.shiftKey || event.ctrlKey || event.metaKey || event.altKey) return null
  const m = /^Digit([1-9])$/.exec(event.code)
  if (!m) return null
  return deskByHotkey(Number(m[1])) ?? null
}

/** 定义完整性自证（desks 测试同源约束）：返回问题列表，空 = 合法。 */
export function validateDesks(): string[] {
  const issues: string[] = []
  const hotkeys = new Set<number>()
  for (const desk of DESKS) {
    if (hotkeys.has(desk.hotkey)) issues.push(`热键重复 Shift+${desk.hotkey}`)
    hotkeys.add(desk.hotkey)
    const seen = new Set<string>()
    if (!desk.panels.length) issues.push(`${desk.id} 没有面板`)
    for (const p of desk.panels) {
      if (!getPanel(p.id)) issues.push(`${desk.id} 引用未知面板 ${p.id}`)
      if (seen.has(p.id)) issues.push(`${desk.id} 面板 ${p.id} 重复`)
      if (p.ref && !seen.has(p.ref)) issues.push(`${desk.id} 面板 ${p.id} 的 ref=${p.ref} 未先于它出现`)
      seen.add(p.id)
    }
    if (!seen.has(desk.focus)) issues.push(`${desk.id} focus=${desk.focus} 不在 panels 内`)
  }
  return issues
}
