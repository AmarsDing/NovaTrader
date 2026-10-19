/**
 * G2 F1 功能卡索引：纯数据与纯构造（无副作用，浮层与测试共用）。
 * - cardOf：F1 快照取卡——打开瞬间按聚焦面板定格（随后切面板不追踪，关闭即弃）；
 * - usageOf：彭博式「prefix 参数」用法行；
 * - HOTKEY_ROWS：全局热键清单的呈现副本（与 shortcuts.ts / WorkbenchView Ctrl 表同源同步）。
 */
import { functionIndex, type FunctionCard } from './command-grammar'

/** 按面板 id 取功能卡；面板未挂助记符（或未注册）返回 null，浮层按「无卡面板」呈现。 */
export function cardOf(
  panelId: string | undefined,
  cards: FunctionCard[] = functionIndex(),
): FunctionCard | null {
  if (!panelId) return null
  return cards.find((c) => c.id === panelId) ?? null
}

/** 用法行：无参数面板直开；有参数面板拼 Bloomberg 式「prefix 参数形态」。 */
export function usageOf(card: FunctionCard): string {
  if (card.argKind === 'none') return `${card.prefix}（无参数直接开）`
  return card.argHint ? `${card.prefix} ${card.argHint}` : card.prefix
}

export interface HotkeyRow {
  keys: string
  action: string
}

/**
 * 全局热键清单（呈现副本）：与 workspace/shortcuts.ts 的裸键/Shift 表和 WorkbenchView 的 Ctrl 表一一对应。
 * 改任一处热键必须同步本表——快照测试盯着这份清单漂移。
 */
export const HOTKEY_ROWS: readonly HotkeyRow[] = [
  { keys: 'B / S', action: '开交易面板并预填买 / 卖' },
  { keys: 'Shift+T', action: '开交易面板' },
  { keys: 'Shift+K', action: '开 Kill Switch 对话框（需二次确认）' },
  { keys: 'Shift+M', action: '开涨停梯队' },
  { keys: 'Shift+R', action: '开风控链' },
  { keys: 'Shift+1..4', action: '切换交易时段桌（盘前/竞价/打板/复盘）' },
  { keys: '1..9', action: '聚焦组内第 N 个面板' },
  { keys: 'Ctrl+K', action: '命令面板（面板态）' },
  { keys: 'Ctrl+Shift+P / Ctrl+Shift+K', action: '命令面板（命令态）' },
  { keys: 'Ctrl+Z / Ctrl+Shift+Z', action: '布局撤销 / 重做' },
  { keys: 'F1', action: '本帮助浮层' },
]
