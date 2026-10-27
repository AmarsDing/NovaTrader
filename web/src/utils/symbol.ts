/**
 * A 股代码归一单源（P0-1 裸码修复）：规则与 backend/pkg/symbol FromAShareCode 同一代码段
 * （6 沪 / 0、3 深 / 4、8、92 北）。
 *   600519 → 600519.SH；sh600519 → 600519.SH；600519.sh → 600519.SH；
 *   认不出的（指数、基金、港股等）原文透传——后端 parseSymbol（ParseAShare）是唯一判错闸门。
 * stores/symbol 与 panel-bus 共用此函数，禁止再各自实现归一逻辑。
 */
const CODE_RE = /^\d{6}$/
const FULL_RE = /^(\d{6})\.(SH|SZ|BJ)$/
const PREFIX_RE = /^(SH|SZ|BJ)(\d{6})$/

export function normalizeCode(raw: string): string {
  const s = raw.trim().toUpperCase()
  const full = FULL_RE.exec(s)
  if (full) return `${full[1]}.${full[2]}`
  const prefixed = PREFIX_RE.exec(s)
  if (prefixed) return `${prefixed[2]}.${prefixed[1]}`
  if (CODE_RE.test(s)) {
    const market = s[0] === '6' ? 'SH'
      : s[0] === '0' || s[0] === '3' ? 'SZ'
        : s[0] === '4' || s[0] === '8' || s.startsWith('92') ? 'BJ' : ''
    if (market) return `${s}.${market}`
  }
  return s
}
