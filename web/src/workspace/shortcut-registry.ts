// shortcut-registry.ts：全局快捷键登记表唯一出处（W5b；UI 宪法 R-UI-31/37）。
//
// 纪律：
//   - 组合键先归一（ctrl→alt→shift→meta→key 固定序、键名大写），同一 scope+combo 只许一个 id；
//   - 重复登记即抛 ShortcutConflictError（面板重复挂载不静默覆盖，冲突必须当场暴露）；
//   - 面板卸载调 unregisterScope(panelId) 全量释放，泄漏的键不得继续吞事件；
//   - 「提交前拦截」：isEditableTarget 命中输入控件时不触发（快捷键不劫持打字）。

export interface ShortcutSpec {
  /** 全局唯一 id，约定 `<panel>.<action>`，如 trade.buy / trade.sell */
  id: string
  /** 组合键，如 'F9'、'ctrl+alt+k'；大小写与顺序由 normalizeCombo 归一 */
  combo: string
  /** 人类可读说明（帮助面板/提示位直接引用） */
  desc: string
  /** 归属面板 id；缺省 'global'，面板注销按此全量释放 */
  scope?: string
}

export type ShortcutHandler = () => void

export class ShortcutConflictError extends Error {
  constructor(
    readonly combo: string,
    readonly scope: string,
    readonly existingId: string,
    readonly incomingId: string,
  ) {
    super(`快捷键冲突：${scope} 内 ${combo} 已被 ${existingId} 占用（${incomingId}）`)
    this.name = 'ShortcutConflictError'
  }
}

const MOD_ORDER = ['ctrl', 'alt', 'shift', 'meta'] as const
const MOD_ALIAS: Record<string, string> = {
  ctrl: 'ctrl',
  control: 'ctrl',
  alt: 'alt',
  option: 'alt',
  shift: 'shift',
  meta: 'meta',
  cmd: 'meta',
  command: 'meta',
}

/** 组合键归一：修饰键固定序 + 主键大写（单字符）/原名（F9、Enter、Escape）。 */
export function normalizeCombo(combo: string): string {
  const parts = combo
    .split(/[+\s]+/)
    .map((p) => p.trim().toLowerCase())
    .filter(Boolean)
  const mods = new Set<string>()
  let key = ''
  for (const p of parts) {
    const mod = MOD_ALIAS[p]
    if (mod) mods.add(mod)
    else key = p.length === 1 ? p.toUpperCase() : p.charAt(0).toUpperCase() + p.slice(1)
  }
  const ordered = MOD_ORDER.filter((m) => mods.has(m))
  return [...ordered, key].filter(Boolean).join('+')
}

/** 键盘事件 → 归一组合键（"meta" 与 ctrl 不合并：跨平台语义不同，登记表显式区分）。 */
export function comboFromEvent(e: KeyboardEvent): string {
  if (!e.key || e.key === 'Unidentified') return ''
  const parts: string[] = []
  if (e.ctrlKey) parts.push('ctrl')
  if (e.altKey) parts.push('alt')
  if (e.shiftKey) parts.push('shift')
  if (e.metaKey) parts.push('meta')
  parts.push(e.key)
  return normalizeCombo(parts.join('+'))
}

/** 焦点在可编辑控件里时不触发快捷键（不劫持打字）。 */
export function isEditableTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || typeof el.tagName !== 'string') return false
  const tag = el.tagName.toLowerCase()
  if (tag === 'input' || tag === 'textarea' || tag === 'select') return true
  // jsdom 不实现 isContentEditable 映射，补 contenteditable 属性兜底。
  if (el.isContentEditable) return true
  return typeof el.getAttribute === 'function' && el.getAttribute('contenteditable') === 'true'
}

export interface ShortcutRegistry {
  register(spec: ShortcutSpec): void
  /** 绑定处理器；返回解绑函数。未登记 id 直接抛错（先登记后绑定）。 */
  bind(id: string, handler: ShortcutHandler): () => void
  unregister(id: string): void
  /** 面板注销：释放该 scope 下全部登记与处理器。 */
  unregisterScope(scope: string): void
  /** 命中返回 id（并执行处理器语义交给调用方 decide）；scope 未命中 global 兜底。 */
  resolve(e: KeyboardEvent, scope?: string): string | null
  run(e: KeyboardEvent, scope?: string): boolean
  list(): ShortcutSpec[]
}

export function createShortcutRegistry(): ShortcutRegistry {
  const specs = new Map<string, Required<ShortcutSpec>>()
  const handlers = new Map<string, ShortcutHandler>()
  /** `${scope}|${combo}` → id */
  const index = new Map<string, string>()

  const scopeOf = (spec: ShortcutSpec) => spec.scope || 'global'

  function register(spec: ShortcutSpec) {
    if (!spec.id || !spec.combo) throw new Error('快捷键登记缺 id 或 combo')
    if (specs.has(spec.id)) throw new Error(`快捷键 id 重复登记：${spec.id}`)
    const scope = scopeOf(spec)
    const combo = normalizeCombo(spec.combo)
    const key = `${scope}|${combo}`
    const existing = index.get(key)
    if (existing) throw new ShortcutConflictError(combo, scope, existing, spec.id)
    specs.set(spec.id, { ...spec, combo, scope })
    index.set(key, spec.id)
  }

  function unregister(id: string) {
    const spec = specs.get(id)
    if (!spec) return
    specs.delete(id)
    handlers.delete(id)
    index.delete(`${spec.scope}|${spec.combo}`)
  }

  function bind(id: string, handler: ShortcutHandler) {
    if (!specs.has(id)) throw new Error(`快捷键未登记即绑定：${id}`)
    handlers.set(id, handler)
    return () => {
      if (handlers.get(id) === handler) handlers.delete(id)
    }
  }

  function resolve(e: KeyboardEvent, scope = 'global'): string | null {
    const combo = comboFromEvent(e)
    if (!combo) return null
    const hit = index.get(`${scope}|${combo}`)
    if (hit) return hit
    if (scope !== 'global') return index.get(`global|${combo}`) ?? null
    return null
  }

  return {
    register,
    bind,
    unregister,
    unregisterScope(scope) {
      for (const spec of [...specs.values()]) {
        if (spec.scope === scope) unregister(spec.id)
      }
    },
    resolve,
    run(e, scope = 'global') {
      if (isEditableTarget(e.target)) return false
      const id = resolve(e, scope)
      if (!id) return false
      const handler = handlers.get(id)
      if (!handler) return false
      handler()
      return true
    },
    list() {
      return [...specs.values()].map((s) => ({ ...s }))
    },
  }
}
