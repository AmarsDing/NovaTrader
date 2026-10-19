/**
 * G5 面板上下文附件：把「当前聚焦面板是什么、带着什么参数」收敛成可发给 LLM 的附件。
 * 隐私与体量纪律（抄 gloomberb ask-gloom/pane-context.ts）：
 *   ① 账面键（滚动/尺寸/排序/选中/布局类）一律剔除；
 *   ② 敏感键（口令/令牌/webhook/cookie 类）一律剔除；
 *   ③ 只收标量（string/number/boolean），≤8 个，字符串 trim 后 ≤80 字符；
 *   ④ 嵌入问题的附件段总长 ≤800 字符，超出从尾部参数对开始裁；
 *   ⑤ 参数整片禁出的面板（settings 配置）只报 id/标题，不报参数。
 * AskRequest proto 尚无 pane 字段，附件以规范化文本内嵌问题（诚实路径），
 * 字段化后本模块输出可直接改走结构化通道。
 */
export interface PaneAttachment {
  panelId: string
  panelTitle: string
  params: Record<string, string | number | boolean>
}

export const PANE_MAX_PARAMS = 8
export const PANE_MAX_VALUE_LEN = 80
export const PANE_MAX_EMBED_LEN = 800

/** 账面键正则表：滚动/几何/排序/选中/布局簿记，绝不外发。 */
const BOOKKEEPING_KEY_RE = /^(?:__|scroll|left|top$|width|height|sort|order$|filter|cursor|selection|selected|collapse|expand|zoom|pane|panel|dock|drag|resize|layout|version|index|offset)/i

/** 敏感键正则表：任何可能含凭据语义的键名。 */
const SENSITIVE_KEY_RE = /password|passwd|secret|token|credential|api[-_]?key|webhook|session|cookie|auth/i

/** 参数整片禁出的面板：配置面板参数未来可能经总线携带路径/地址，宁可少报。 */
const PANE_PARAM_DENY: ReadonlySet<string> = new Set(['settings'])

export function buildPaneAttachment(
  panelId: string,
  panelTitle: string,
  rawParams: Record<string, unknown> | undefined | null,
): PaneAttachment {
  const params: Record<string, string | number | boolean> = {}
  if (rawParams && !PANE_PARAM_DENY.has(panelId)) {
    for (const [key, value] of Object.entries(rawParams)) {
      if (Object.keys(params).length >= PANE_MAX_PARAMS) break
      if (BOOKKEEPING_KEY_RE.test(key) || SENSITIVE_KEY_RE.test(key)) continue
      const scalar = toScalar(value)
      if (scalar !== undefined) params[key] = scalar
    }
  }
  return { panelId, panelTitle, params }
}

function toScalar(value: unknown): string | number | boolean | undefined {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return Number.isFinite(value) ? value : undefined
  if (typeof value === 'string') {
    const trimmed = value.trim()
    return trimmed ? trimmed.slice(0, PANE_MAX_VALUE_LEN) : undefined
  }
  return undefined
}

/** 附件 + 用户问题 → 发送文本。附件段超限从尾部参数对裁；无附件原样返回。 */
export function composePaneQuestion(
  attachment: PaneAttachment | null | undefined,
  question: string,
): string {
  const q = question.trim()
  if (!attachment || !attachment.panelId) return q
  const pairs = Object.entries(attachment.params).map(([key, value]) => `${key}=${String(value)}`)
  let header = embedLine(attachment, pairs)
  while (pairs.length && header.length > PANE_MAX_EMBED_LEN) {
    pairs.pop()
    header = embedLine(attachment, pairs)
  }
  return `${header}\n问：${q}`
}

function embedLine(attachment: PaneAttachment, pairs: string[]): string {
  const base = `【面板 ${attachment.panelTitle}（${attachment.panelId}）】`
  return pairs.length ? `${base}${pairs.join(' ')}` : base
}

/** 问 AI 候选出现条件：≥3 字符且无助记符命中（助记符场景键盘意图明确，不抢置顶）。 */
export function offerAskCandidate(keyword: string, mnemonicMatched: boolean): boolean {
  return !mnemonicMatched && keyword.trim().length >= 3
}
