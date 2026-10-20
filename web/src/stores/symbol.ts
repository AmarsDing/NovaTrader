/**
 * 全局当前证券（视频设计指引 §8「切 symbol 不换布局」）：
 * - code 为面板传入的原文（'600519' 或 '600519.SH' 均可，后端 parseSymbol 归一）；
 * - source 记录发起方（面板 id），供回跳/排查，不做路由；
 * - name 改为从 security 名称字典（方案 §6.2）按当前 code 查表：字典已有名切票即显，
 *   缺名回空串（UI 统一走 hasName=false 显「名称未返回」，数据纪律）；
 * - setSymbol 空值/同值短路：消费者 watch code 即可，布局恒不动；切换顺带 ensure 补名字典。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useSecurityStore } from '@/stores/security'

export interface SymbolSwitch {
  code: string
  name: string
  source: string
  switchedAt: number
}

export const useSymbolStore = defineStore('symbol', () => {
  const code = ref('')
  const source = ref('')
  const switchedAt = ref(0)
  const security = useSecurityStore()

  /** 名称唯一出处：§6.2 名称字典。历史字段名保留（空串=未取到），fillName 逻辑下沉为字典写入。 */
  const name = computed(() => (code.value ? security.resolve(code.value).name : ''))

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
    // 顺手养字典：displayName 落名称字典；缺名走后台批量补（fetcher 未注册时静默 noop）。
    if (displayName) security.put([{ code: next, name: displayName }])
    security.ensure([next])
    if (next === code.value) return false
    code.value = next
    source.value = from
    switchedAt.value = Date.now()
    return true
  }

  /** 接口回来后的名称回填：写入名称字典——慢响应只落自己代码那一格，天然盖不到当前票。 */
  function fillName(forCode: string, displayName: string) {
    if (!displayName) return
    security.put([{ code: forCode, name: displayName }])
  }

  return { code, name, source, switchedAt, setSymbol, fillName }
})
