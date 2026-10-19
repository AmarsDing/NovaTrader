/**
 * 全局当前证券（视频设计指引 §8「切 symbol 不换布局」）：
 * - code 为面板传入的原文（'600519' 或 '600519.SH' 均可，后端 parseSymbol 归一）；
 * - source 记录发起方（面板 id），供回跳/排查，不做路由；
 * - name 为可选显示名（接口返回后回填），空不猜；
 * - setSymbol 空值/同值短路：消费者 watch code 即可，布局恒不动。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface SymbolSwitch {
  code: string
  name: string
  source: string
  switchedAt: number
}

export const useSymbolStore = defineStore('symbol', () => {
  const code = ref('')
  const name = ref('')
  const source = ref('')
  const switchedAt = ref(0)

  /** normalize：trim+大写 + A 股归一为通达信形态（与 backend/pkg/symbol.go FromAShareCode 同一
   * 代码段规则：6 沪，0/3 深，4/8/92 北）；认不出的原文透传——后端 parseSymbol 是唯一判错闸门。 */
  const CODE_RE = /^\d{6}$/
  const FULL_RE = /^(\d{6})\.(SH|SZ|BJ)$/
  const PREFIX_RE = /^(SH|SZ|BJ)(\d{6})$/
  function normalize(raw: string): string {
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

  function setSymbol(raw: string, from: string, displayName?: string): boolean {
    const next = normalize(raw)
    if (!next) return false
    if (next === code.value) {
      if (displayName && displayName !== name.value) name.value = displayName
      return false
    }
    code.value = next
    name.value = displayName ?? ''
    source.value = from
    switchedAt.value = Date.now()
    return true
  }

  /** 接口回来后的名称回填；只有同代码时才接受，防慢响应盖掉新切换。 */
  function fillName(forCode: string, displayName: string) {
    if (!displayName) return
    if (normalize(forCode) === code.value) name.value = displayName
  }

  return { code, name, source, switchedAt, setSymbol, fillName }
})
