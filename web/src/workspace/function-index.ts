/**
 * G2 F1 功能卡索引：纯数据与纯构造（无副作用，浮层与测试共用）。
 * - cardOf：F1 快照取卡——打开瞬间按聚焦面板定格（随后切面板不追踪，关闭即弃）；
 * - cardByPrefix / helpCandidates：命令面板「帮助 <助记符>」入口的取卡与候选（精确命中置顶）；
 * - parseHelpQuery / buildHelpSpecs：帮助态分流判据与行构造（组件仅余门闸，审查闭环回写）；
 * - usageOf：彭博式「prefix 参数」用法行；
 * - HOTKEY_ROWS：全局热键清单的呈现副本（与 shortcuts.ts / WorkbenchView Ctrl 表同源同步）。
 */
import { configCategories, type ConfigCategory } from '@/view/config-catalog'
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
  { keys: 'Shift+5..9', action: '套用已保存的布局预设（保存时自动分配空位）' },
  { keys: '1..9', action: '聚焦组内第 N 个面板' },
  { keys: 'Ctrl+K', action: '命令面板（面板态）' },
  { keys: 'Ctrl+Shift+P / Ctrl+Shift+K', action: '命令面板（命令态）' },
  { keys: 'Ctrl+Z / Ctrl+Shift+Z', action: '布局撤销 / 重做' },
  { keys: 'F1', action: '本帮助浮层' },
  { keys: 'F9 / F10', action: '下单条：按表单方向直接提交买 / 卖（仅下单页，与提交钮同闸）' },
]

/* ---------- 「设置 <关键词>」直达（§9.3，S2 批） ----------
 * 复用 G2 帮助态三件套（parse → candidates/specs 纯构造 → palette 结果池整段接管）：
 * ConfigCenter 分组为词库单源（config-catalog 叶子模块，无环）；
 * 行动作约定：派发 window 事件 nt:open-config 并带 detail.category=分组 id，
 * ConfigCenter（overlay 挂载前暂存 / 存活面板即时）双侧接收定位，见 view/ConfigCenter.vue。 */

export interface SettingRowSpec {
  id: string
  label: string
  hint: string
  /** 显式带组时的 ConfigCenter 分组 id（nav 成员，含 config-catalog 业务分组）。 */
  categoryId?: string
  /** open = 行可执行（开配置并定位分组）；none = 提示行（Enter 忽略）。 */
  action: 'open' | 'none'
}

/** 「设置 <词>」分流判据（同 parseHelpQuery 纪律）：首词「设置」命中即接管，词尾空串=出全部分组。 */
export function parseSettingsQuery(keyword: string): string | null {
  const m = /^设置\s*(.*)$/.exec(keyword)
  return m ? m[1].trim() : null
}

/** 「设置 <词>」候选：标题/说明/字段名与提示的宽松子串命中；空词出全部；保持 catalog 原顺序。 */
export function settingsCandidates(
  term: string,
  categories: ConfigCategory[] = configCategories,
): ConfigCategory[] {
  const t = term.trim().toLowerCase()
  if (!t) return categories
  return categories.filter((cat) =>
    [cat.title, cat.note, ...cat.fields.map((f) => `${f.key} ${f.label} ${f.hint}`)]
      .join(' ')
      .toLowerCase()
      .includes(t),
  )
}

/** 设置行动项纯构造：空词=全分组索引；有词=命中组可执行；零命中出提示行（不杜撰分组）。 */
export function buildSettingsSpecs(
  term: string,
  categories: ConfigCategory[] = configCategories,
): SettingRowSpec[] {
  const hits = settingsCandidates(term, categories)
  if (hits.length) {
    return hits.map((cat) => ({
      id: `setting:${cat.id}`,
      label: `设置 ${cat.title}`,
      hint: `${cat.note} · Enter 打开并定位`,
      categoryId: cat.id,
      action: 'open' as const,
    }))
  }
  return [
    { id: 'setting:none', label: `设置 ${term}`, hint: '没有匹配的配置分组（示例：设置 数据源 / 设置 模型 / 设置 风控）', action: 'none' },
  ]
}

