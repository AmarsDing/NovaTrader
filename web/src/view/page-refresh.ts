import { createRefreshQueue } from '@/utils/refresh-queue'

/** Bounded read batches with a guaranteed trailing push refresh. Each caller awaits its own batch. */
export function createPageRefresh(options: {
  key: () => string
  read: (key: string) => Promise<boolean>
  pushIntervalMs: number
}) {
  let disposed = false
  let lastPush = -Infinity
  let timer: ReturnType<typeof setTimeout> | undefined
  let pending: { key: string; promise: Promise<boolean>; resolve: (value: boolean) => void; reject: (error: unknown) => void } | undefined
  const queued = createRefreshQueue(async (key) => !disposed && key === options.key() ? options.read(key) : false)

  function refresh(): Promise<boolean> {
    return disposed ? Promise.resolve(false) : queued(options.key())
  }

  function fromPush(): Promise<boolean> {
    if (disposed) return Promise.resolve(false)
    const key = options.key()
    if (pending && pending.key !== key) {
      clearTimeout(timer)
      pending.resolve(false)
      pending = undefined
    }
    const wait = Math.max(0, lastPush + options.pushIntervalMs - Date.now())
    if (!pending && wait === 0) {
      lastPush = Date.now()
      return queued(key)
    }
    if (!pending) {
      let resolve!: (value: boolean) => void
      let reject!: (error: unknown) => void
      const promise = new Promise<boolean>((yes, no) => { resolve = yes; reject = no })
      pending = { key, promise, resolve, reject }
      timer = setTimeout(() => {
        const batch = pending
        pending = undefined
        timer = undefined
        if (!batch) return
        lastPush = Date.now()
        void queued(batch.key).then(batch.resolve, batch.reject)
      }, wait)
    }
    return pending.promise
  }

  function dispose() {
    disposed = true
    clearTimeout(timer)
    pending?.resolve(false)
    pending = undefined
  }

  return { refresh, fromPush, dispose }
}
