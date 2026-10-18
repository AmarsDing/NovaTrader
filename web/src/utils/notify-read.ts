/**
 * 通知中心逐条已读态（G3）：localStorage 持久化已读消息 id 集合。
 * 纯函数 + 显式 storage 参数，组件侧自备响应式包装；坏存储显式当空集（读路径容错），
 * 写入失败静默（不影响 UI 已读反馈）。
 */
export const NOTIF_READ_KEY = 'nt.notify-read.v1'
export const NOTIF_READ_CAP = 500

export function loadNotifReadIds(storage: Pick<Storage, 'getItem'> | undefined = pickStorage()): string[] {
  if (!storage) return []
  try {
    const raw = storage.getItem(NOTIF_READ_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return capNotifReadIds(parsed.filter((x): x is string => typeof x === 'string' && x.length > 0))
  } catch {
    return []
  }
}

export function saveNotifReadIds(ids: readonly string[], storage: Pick<Storage, 'setItem'> | undefined = pickStorage()): void {
  if (!storage) return
  try {
    storage.setItem(NOTIF_READ_KEY, JSON.stringify(capNotifReadIds(ids)))
  } catch {
    /* 存储不可用时仅在内存生效 */
  }
}

/** 去重（保首个）+ 截断到容量。调用方语义：新已读 id 放头部，最老被挤出。 */
export function capNotifReadIds(ids: readonly string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const id of ids) {
    if (seen.has(id)) continue
    seen.add(id)
    out.push(id)
  }
  return out.slice(0, NOTIF_READ_CAP)
}

function pickStorage(): Storage | undefined {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : undefined
  } catch {
    return undefined
  }
}
