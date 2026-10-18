/**
 * G2 命令助记符语法：彭博式「前缀 + 参数」解析与功能卡索引。
 * 移植 gloomberb command-bar/commands/registry.ts 的解析纪律：
 * - 输入去空格分段：首段转大写，按 prefix/aliases 精确匹配面板，余段为参数；
 * - argKind=ticker 归一为 6 位代码（兼容 SH/SZ/BJ 前后缀写法），
 *   1–5 位数字视为「输入中」而非错误；
 * - argKind=side 归一 buy/sell（buy/b/买、sell/s/卖）；
 * - 本模块只解析不执行：执行决策（开面板/推参数/记 recent）由命令面板负责，便于纯函数测试。
 */
import { listPanels, type PanelDef, type PanelShortcut } from './panel-registry'

export interface ParsedInput {
  /** 大写后的助记符候选。 */
  prefix: string
  /** 前缀之后的全部参数文本（已 trim）。 */
  arg: string
}

/** 首段字母数字 1–4 位 + 可选空格参数；不匹配返回 null。 */
export function parseMnemonic(input: string): ParsedInput | null {
  const text = input.trim()
  if (!text) return null
  const m = text.match(/^([A-Za-z0-9]{1,4})(?:\s+(.*))?$/)
  if (!m) return null
  return { prefix: m[1].toUpperCase(), arg: (m[2] ?? '').trim() }
}

const TICKER_RE = /^\d{6}$/

/** 归一股票代码为 6 位数字：`600519.SH`、`SH600519`、`sz000001` 均可；无法归一则 null。 */
export function normalizeTicker(raw: string): string | null {
  let t = raw.trim().toUpperCase()
  if (!t) return null
  t = t.replace(/^(SH|SZ|BJ)/, '').replace(/\.(SH|SZ|BJ)$/, '')
  return TICKER_RE.test(t) ? t : null
}

/** 已归一的 6 位代码形态判定（recent 纪律同源）。 */
export function isTicker(raw: string): boolean {
  return TICKER_RE.test(raw.trim())
}

export type TradeSide = 'buy' | 'sell'

/** 归一买卖方向：buy/b/买 → buy，sell/s/卖 → sell，其余 null。 */
export function normalizeSide(raw: string): TradeSide | null {
  const t = raw.trim().toLowerCase()
  if (t === 'buy' || t === 'b' || t === '买') return 'buy'
  if (t === 'sell' || t === 's' || t === '卖') return 'sell'
  return null
}

export type ArgState = 'empty' | 'partial' | 'valid' | 'invalid'

export interface MnemonicResolution {
  def: PanelDef
  shortcut: PanelShortcut
  /** 原始参数文本（已 trim，可为 ''）。 */
  arg: string
  /** 参数状态：empty 直接开面板；partial 输入中；valid 带参执行；invalid 报错提示。 */
  state: ArgState
  /** state=valid 且 argKind=ticker 时的 6 位归一代码。 */
  ticker: string | null
  /** state=valid 且 argKind=side 时的方向。 */
  side: TradeSide | null
}

/** prefix/alias → 面板索引（每次解析重建，21 条成本可忽略）。 */
function shortcutIndex(panels: PanelDef[]): Map<string, PanelDef> {
  const map = new Map<string, PanelDef>()
  for (const def of panels) {
    const s = def.shortcut
    if (!s) continue
    map.set(s.prefix.toUpperCase(), def)
    for (const alias of s.aliases ?? []) map.set(alias.toUpperCase(), def)
  }
  return map
}

/** 解析一条助记符命令；前缀不认识返回 null（交给普通模糊搜索）。 */
export function resolveMnemonic(input: string, panels: PanelDef[] = listPanels()): MnemonicResolution | null {
  const parsed = parseMnemonic(input)
  if (!parsed) return null
  const def = shortcutIndex(panels).get(parsed.prefix)
  if (!def?.shortcut) return null
  const shortcut = def.shortcut
  const arg = parsed.arg

  let state: ArgState
  let ticker: string | null = null
  let side: TradeSide | null = null

  if (shortcut.argKind === 'none') {
    // 无参数面板：多余参数按彭博宽松纪律忽略，照常打开。
    state = 'valid'
  } else if (!arg) {
    state = 'empty'
  } else if (shortcut.argKind === 'ticker') {
    ticker = normalizeTicker(arg)
    if (ticker) state = 'valid'
    else if (/^\d{1,5}$/.test(arg.trim())) state = 'partial'
    else state = 'invalid'
  } else {
    side = normalizeSide(arg)
    state = side ? 'valid' : 'invalid'
  }

  return { def, shortcut, arg, state, ticker, side }
}

/** F1 功能卡（注册表自动生成）：面板概况 + 助记符 + 参数说明。 */
export interface FunctionCard {
  id: string
  title: string
  prefix: string
  aliases: string[]
  argKind: PanelShortcut['argKind']
  argHint: string
  description: string
}

export function functionIndex(panels: PanelDef[] = listPanels()): FunctionCard[] {
  return panels.flatMap((def) => {
    const s = def.shortcut
    if (!s) return []
    return [{
      id: def.id,
      title: def.title,
      prefix: s.prefix,
      aliases: [...(s.aliases ?? [])],
      argKind: s.argKind,
      argHint: s.argHint ?? '',
      description: s.description,
    }]
  })
}
