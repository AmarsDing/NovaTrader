/**
 * 证券名称字典（方案 §6.2）：全站唯一的 code → 基本信息（名称/市场/行业/估值章）查表。
 *
 * 纪律：
 * - 「没取到≠0」：name 为 null 表示未取到，UI 走 resolve(...).hasName=false 显「名称未返回」，
 *   禁空串冒充、禁拿 code 冒充名称占位不标注；
 * - 填充三路：registerBasicsFetcher 注入的批量补齐（store 不 import api 层，防循环依赖）、
 *   各 API 适配层拿到 name 时顺手 put、GetQuote 单点回填；
 * - ensure 四重去重：已有名 / 待出窗（pending）/ 在飞行（inflight）/ 失败冷却 5min 内不再发；
 *   300ms 批量窗合并；fetcher 未注册时静默 noop；
 * - put 合并：name 为 null/undefined/'' 不覆盖已有 name；updatedAt 只增（旧快照整体拒收）；
 * - 持久化：localStorage 'nt.secbasic.v1'，上限 3000 条，超出按 updatedAt 最旧淘汰（LRU）。
 *
 * 注意：useSecurityStore() 必须在函数体内调用，禁模块顶层调用（pinia 初始化时序）。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface SecurityBasic {
  /** 规范全码，如 '600519.SH'。 */
  code: string
  /** null = 未取到（数据纪律：缺失非空串）。 */
  name: string | null
  market?: string // 'SH'|'SZ'|'BJ'
  industry?: string
  st?: boolean
  suspended?: boolean
  peTtm?: number | null
  pb?: number | null
  /** 流通市值，元。 */
  circMv?: number | null
  /** epoch ms。 */
  updatedAt?: number
}

export type SecurityBasicPatch = Partial<SecurityBasic> & { code: string }

export type SecurityBasicsFetcher = (codes: string[]) => Promise<SecurityBasicPatch[]>

export interface SecurityResolved {
  code: string
  /** hasName=false 时为空串，UI 据此显「名称未返回」。 */
  name: string
  hasName: boolean
}

const KEY = 'nt.secbasic.v1'
const MAX = 3000
const BATCH_MS = 300
const COOLDOWN_MS = 5 * 60 * 1000

const CODE_RE = /^\d{6}$/
const FULL_RE = /^(\d{6})\.(SH|SZ|BJ)$/
const PREFIX_RE = /^(SH|SZ|BJ)(\d{6})$/

/** 裸六位归一为全码：6xx/605/688→.SH，00x/30x→.SZ，4xx/8xx/92x→.BJ。
 * 与 stores/symbol.ts 的 normalize、backend/pkg/symbol.go FromAShareCode 同一代码段规则；
 * 认不出的原文透传（trim+大写），判错闸门留给后端 parseSymbol。 */
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

export const useSecurityStore = defineStore('security', () => {
  const basics = ref(new Map<string, SecurityBasic>())

  let fetcher: SecurityBasicsFetcher | null = null
  let hydrated = false
  const pending = new Set<string>()
  const inflight = new Set<string>()
  /** code → 冷却截止 epoch ms（取数失败/查无此名）。 */
  const cooldown = new Map<string, number>()
  let flushTimer: ReturnType<typeof setTimeout> | null = null

  /** api 层注入批量取数器；返回反注册函数。重复注册以新为准。 */
  function registerBasicsFetcher(fn: SecurityBasicsFetcher): () => void {
    fetcher = fn
    return () => {
      if (fetcher === fn) fetcher = null
    }
  }

  /** 超上限时按 updatedAt 最旧淘汰。 */
  function evictOverflow() {
    const map = basics.value
    if (map.size <= MAX) return
    const oldestFirst = [...map.values()].sort((a, b) => (a.updatedAt ?? 0) - (b.updatedAt ?? 0))
    for (const entry of oldestFirst.slice(0, map.size - MAX)) map.delete(entry.code)
  }

  function persist() {
    try {
      localStorage.setItem(KEY, JSON.stringify([...basics.value.values()]))
    } catch {
      /* 存储满/隐私模式忽略 */
    }
  }

  /** 合并写入：null/undefined/'' 的 name 不覆盖已有名称；updatedAt 回退的旧快照整体拒收；
   * undefined 的可选字段不覆盖已有值（局部补丁友好）。 */
  function put(list: SecurityBasicPatch[]) {
    if (!Array.isArray(list)) return
    let dirty = false
    for (const item of list) {
      if (!item || typeof item.code !== 'string' || !item.code.trim()) continue
      const code = normalizeCode(item.code)
      if (!code) continue
      const prev = basics.value.get(code)
      if (prev?.updatedAt != null && item.updatedAt != null && item.updatedAt < prev.updatedAt) continue
      const next: SecurityBasic = {
        code,
        name: item.name ? item.name : (prev?.name ?? null),
        market: item.market ?? prev?.market,
        industry: item.industry ?? prev?.industry,
        st: item.st ?? prev?.st,
        suspended: item.suspended ?? prev?.suspended,
        peTtm: item.peTtm !== undefined ? item.peTtm : (prev?.peTtm ?? null),
        pb: item.pb !== undefined ? item.pb : (prev?.pb ?? null),
        circMv: item.circMv !== undefined ? item.circMv : (prev?.circMv ?? null),
        updatedAt: Math.max(prev?.updatedAt ?? 0, item.updatedAt ?? Date.now()),
      }
      basics.value.set(code, next)
      dirty = true
    }
    if (dirty) {
      evictOverflow()
      persist()
    }
  }

  /** 缺名代码收进 300ms 批量窗，经注入 fetcher 后台补齐；fetcher 未注册时静默 noop。 */
  function ensure(codes: string[]) {
    if (!fetcher || !Array.isArray(codes)) return
    const now = Date.now()
    for (const raw of codes) {
      if (typeof raw !== 'string' || !raw.trim()) continue
      const code = normalizeCode(raw)
      if (!code) continue
      // 四重去重：已有名 / 已在窗 / 在飞行 / 冷却中。
      if (basics.value.get(code)?.name) continue
      if (pending.has(code) || inflight.has(code)) continue
      const until = cooldown.get(code)
      if (until != null && until > now) continue
      pending.add(code)
    }
    if (pending.size > 0) scheduleFlush()
  }

  function scheduleFlush() {
    if (flushTimer != null) return
    flushTimer = setTimeout(() => {
      flushTimer = null
      void flush()
    }, BATCH_MS)
  }

  async function flush() {
    if (!fetcher || pending.size === 0) return
    const batch = [...pending]
    pending.clear()
    batch.forEach((c) => inflight.add(c))
    try {
      put(await fetcher(batch))
      // 取回仍无名的按「查无此名」冷却，防渲染期反复 ensure 造成请求风暴。
      const missAt = Date.now() + COOLDOWN_MS
      for (const c of batch) if (!basics.value.get(c)?.name) cooldown.set(c, missAt)
    } catch {
      // 取数失败冷却 5min；不写 0 不写空名，缺失交 UI 显「名称未返回」。
      const retryAt = Date.now() + COOLDOWN_MS
      batch.forEach((c) => cooldown.set(c, retryAt))
    } finally {
      batch.forEach((c) => inflight.delete(c))
      // 飞行期间新来的继续成窗。
      if (pending.size > 0) scheduleFlush()
    }
  }

  /** UI 取显示名：hasName=false 时必须显「名称未返回」。 */
  function resolve(raw: string): SecurityResolved {
    const code = normalizeCode(raw)
    const name = code ? (basics.value.get(code)?.name ?? null) : null
    return { code, name: name ?? '', hasName: name != null && name !== '' }
  }

  function get(raw: string): SecurityBasic | undefined {
    return basics.value.get(normalizeCode(raw))
  }

  /** 启动时从 localStorage 恢复字典（main.ts 在 pinia 激活后调用一次）；坏存储净化而非崩溃。 */
  function hydrate() {
    if (hydrated) return
    hydrated = true
    try {
      const raw = localStorage.getItem(KEY)
      if (!raw) return
      const data: unknown = JSON.parse(raw)
      if (!Array.isArray(data)) return
      for (const entry of data) {
        if (!entry || typeof entry !== 'object') continue
        const e = entry as Record<string, unknown>
        if (typeof e.code !== 'string' || !e.code.trim()) continue
        const code = normalizeCode(e.code)
        if (!code) continue
        basics.value.set(code, {
          code,
          name: typeof e.name === 'string' && e.name ? e.name : null,
          market: typeof e.market === 'string' && e.market ? e.market : undefined,
          industry: typeof e.industry === 'string' && e.industry ? e.industry : undefined,
          st: typeof e.st === 'boolean' ? e.st : undefined,
          suspended: typeof e.suspended === 'boolean' ? e.suspended : undefined,
          peTtm: typeof e.peTtm === 'number' ? e.peTtm : null,
          pb: typeof e.pb === 'number' ? e.pb : null,
          circMv: typeof e.circMv === 'number' ? e.circMv : null,
          updatedAt: typeof e.updatedAt === 'number' ? e.updatedAt : 0,
        })
      }
      evictOverflow()
    } catch {
      // 损坏存储回空，不阻塞启动。
    }
  }

  return { basics, put, ensure, resolve, get, hydrate, registerBasicsFetcher, normalizeCode }
})
