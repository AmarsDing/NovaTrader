/** 同一key最多一个请求和一个后续刷新。每批Promise只等所属轮次，持续推送不会令恢复永远等待。 */
export function createRefreshQueue<T>(load: (key: string) => Promise<T>): (key?: string) => Promise<T> {
  interface Batch { promise: Promise<T>; resolve: (value: T) => void; reject: (reason: unknown) => void }
  interface Entry { current: Batch; next?: Batch }
  const entries = new Map<string, Entry>()
  function batch(): Batch {
    let resolve!: Batch['resolve']
    let reject!: Batch['reject']
    const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
    return { promise, resolve, reject }
  }
  async function drain(key: string, entry: Entry) {
    for (;;) {
      try { entry.current.resolve(await load(key)) } catch (error) { entry.current.reject(error) }
      if (!entry.next) { entries.delete(key); return }
      entry.current = entry.next
      entry.next = undefined
    }
  }
  return (key = '') => {
    const active = entries.get(key)
    if (active) { active.next ??= batch(); return active.next.promise }
    const first = batch()
    const entry: Entry = { current: first }
    entries.set(key, entry)
    void drain(key, entry)
    return first.promise
  }
}
