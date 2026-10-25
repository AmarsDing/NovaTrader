/**
 * 轮询与版本守卫（UI 宪法 R-UI-34/35 落地）。
 * - 轮询必须感知页面可见性：不可见停问，回见即补一次；
 * - 同一条轮询单flight：上一次未回完不叠加下一次；
 * - 一切异步回填必须经版本守卫，慢响应不得盖新响应。
 * 慢变数据（榜单/健康/账目）默认轮询；告警/信号/委托状态走 WebSocket，勿用本件伪造实时。
 */

export interface PollingOptions {
  /** 轮询间隔（毫秒）。 */
  intervalMs: number
  /** 单次任务；抛错由调用方自行消化，本件不吞错也不重试。 */
  run: () => Promise<void> | void
  /** start 时是否立即执行一次，默认 true。 */
  immediate?: boolean
}

export interface PollingHandle {
  start(): void
  stop(): void
  /** 强制补跑一次（不等下一个 tick）。 */
  readonly tick: () => Promise<void>
  /** 是否处于运行档。 */
  readonly running: boolean
}

export function createVisibilityPolling(options: PollingOptions, doc: Document = document): PollingHandle {
  const { intervalMs, run, immediate = true } = options
  let timer: ReturnType<typeof setInterval> | null = null
  let active = false
  let inFlight = false
  let queuedWhileHidden = false

  async function safeRun(): Promise<void> {
    if (inFlight) return
    if (doc.visibilityState === 'hidden') {
      queuedWhileHidden = true
      return
    }
    inFlight = true
    try {
      await run()
    } finally {
      inFlight = false
    }
  }

  function onVisibility(): void {
    if (doc.visibilityState !== 'hidden' && queuedWhileHidden) {
      queuedWhileHidden = false
      void safeRun()
    }
  }

  return {
    get running() {
      return active
    },
    start() {
      if (active) return
      active = true
      if (immediate) void safeRun()
      timer = setInterval(() => void safeRun(), intervalMs)
      doc.addEventListener('visibilitychange', onVisibility)
    },
    stop() {
      if (!active) return
      active = false
      if (timer !== null) {
        clearInterval(timer)
        timer = null
      }
      doc.removeEventListener('visibilitychange', onVisibility)
      queuedWhileHidden = false
    },
    tick: safeRun,
  }
}

export interface VersionedLoader {
  /** 发起新一轮取数前取一枚 token。 */
  next(): number
  /** 回写前先问：这枚 token 还是不是当前轮次。 */
  isCurrent(candidate: number): boolean
  /** 废弃当前轮（卸载/换标的），在途 token 全部失效。 */
  invalidate(): void
}

export function createVersionedLoader(): VersionedLoader {
  let token = 0
  return {
    next() {
      token += 1
      return token
    },
    isCurrent(candidate: number) {
      return candidate === token
    },
    invalidate() {
      token += 1
    },
  }
}
