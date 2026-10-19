/**
 * G2 F1 功能卡索引：纯数据与纯构造（无副作用，浮层与测试共用）。
 * - cardOf：F1 快照取卡——打开瞬间按聚焦面板定格（随后切面板不追踪，关闭即弃）；
 * - cardByPrefix / helpCandidates：命令面板「帮助 <助记符>」入口的取卡与候选（精确命中置顶）；
 * - parseHelpQuery / buildHelpSpecs：帮助态分流判据与行构造（组件仅余门闸，审查闭环回写）；
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

/** 按助记符取卡（命令面板「帮助 <助记符>」入口）：prefix/alias 大写归一精确命中，未知归 null。 */
export function cardByPrefix(
  token: string,
  cards: FunctionCard[] = functionIndex(),
): FunctionCard | null {
  const t = token.trim().toUpperCase()
  if (!t) return null
  return (
    cards.find((c) => c.prefix === t || c.aliases.some((alias) => alias.toUpperCase() === t)) ?? null
  )
}

/** 「帮助 <词>」候选：精确命中优先，其后按 prefix/alias/标题子串补齐（保持注册表顺序）。 */
export function helpCandidates(
  term: string,
  cards: FunctionCard[] = functionIndex(),
  limit = 5,
): FunctionCard[] {
  const t = term.trim().toUpperCase()
  if (!t) return cards.slice(0, limit)
  const exact = cardByPrefix(t, cards)
  const rest = cards.filter(
    (c) =>
      c !== exact &&
      (c.prefix.includes(t) ||
        c.title.toUpperCase().includes(t) || // 三条匹配路径统一大写归一（拉丁标题不对称潜伏告警）
        c.aliases.some((alias) => alias.toUpperCase().includes(t))),
  )
  return [...(exact ? [exact] : []), ...rest].slice(0, limit)
}

/** 「帮助」查询分流：命中「帮助[词]」语法返回词尾（空词归空串），非帮助查询归 null。命令态（`>`）由调用方门闸。 */
export function parseHelpQuery(keyword: string): string | null {
  const m = /^帮助\s*(.*)$/.exec(keyword)
  return m ? m[1].trim() : null
}

export interface HelpRowSpec {
  id: string
  label: string
  hint: string
  /** 显式带卡时的面板 id；缺省（索引行）表示开浮层定格当前面板。 */
  panelId?: string
  /** open = 行可执行（开浮层）；none = 提示行（Enter 忽略）。 */
  action: 'open' | 'none'
}

/** 帮助行动项纯构造：空词出索引行；有词精确卡置顶 + 子串候选；零命中出提示行（不杜撰）。 */
export function buildHelpSpecs(term: string, cards: FunctionCard[] = functionIndex()): HelpRowSpec[] {
  if (!term) {
    return [{ id: 'help:index', label: '功能卡索引', hint: 'F1 · 打开即定格当前面板', action: 'open' }]
  }
  const hits = helpCandidates(term, cards)
  if (hits.length) {
    return hits.map((c) => ({
      id: `help:${c.id}`,
      label: `帮助 ${c.prefix} — ${c.title}`,
      hint: `${usageOf(c)} · Enter 看功能卡`,
      panelId: c.id,
      action: 'open' as const,
    }))
  }
  return [
    { id: 'help:none', label: `帮助 ${term}`, hint: '未找到该助记符的功能卡（示例：帮助 GP / 帮助 JY）', action: 'none' },
  ]
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
