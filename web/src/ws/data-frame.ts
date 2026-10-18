/**
 * 数据帧调度器（G1，参照 gloomberb src/market-data/frame-scheduler 移植）。
 *
 * 单一时钟：ws 驱动的高频更新合到同一帧——一波推送 = 一次任务批处理，
 * 而不是每条消息各触发一轮渲染。帧节奏：
 * - 可见页：rAF 对齐，帧间隔 ≥ MIN_INTERVAL_MS（约 15Hz）；
 * - 隐藏页（document.hidden）：1Hz 兜底，告警与市值心跳不断；
 * - 无 rAF 环境（vitest/node）：退化为定时器，同样受间隔约束。
 * 负载自适应：一帧引出的执行工作最多占帧间隔 1/FRAME_LOAD_FACTOR；
 * 持续超限自动拉长间隔（不降输入响应），成本指数平滑、毛刺不计。
 */
export type DataFrameTask = () => void

export interface DataFrameClock {
  now(): number
  /** 在下一个绘制机会且不早于 afterMs 后执行 run；返回取消函数。 */
  request(run: () => void, afterMs: number): () => void
  isHidden(): boolean
}

export interface DataFrameOptions {
  clock?: DataFrameClock
}

export interface DataFrame {
  /** 排入一次性任务；同 key 后到覆盖先到（一帧内同 key 只取最新）。 */
  schedule(key: string, task: DataFrameTask): void
  /**
   * 排入带业务最小间隔的任务（如"至多每 15s 重拉一次"）。
   * 帧到点且距上次执行 ≥ minIntervalMs 才真正执行，节奏仍受全局帧钟约束。
   */
  scheduleThrottled(key: string, minIntervalMs: number, task: DataFrameTask): void
  /** 冲刷：尽快安排一帧（如断线恢复后立即应用）。 */
  flush(): void
  dispose(): void
  /** 平滑后的帧任务成本估计（毫秒），供观测与测试。 */
  readonly costMs: number
}

/** 可见页最小帧间隔（约 15Hz）。 */
export const FRAME_MIN_INTERVAL_MS = 66
/** 隐藏页兜底帧间隔。 */
export const HIDDEN_FRAME_INTERVAL_MS = 1000
/** 一帧任务的预算占比：成本 ×4 为降频后的间隔。 */
export const FRAME_LOAD_FACTOR = 4
/** 降频也不比离屏节奏更慢。 */
const MAX_PACED_INTERVAL_MS = 1000
/** 单帧超过此值视为毛刺/被系统节流，不计入成本估计。 */
const FRAME_COST_OUTLIER_MS = 500
/** 新成本样本权重：一次 GC 毛刺几乎不动估计。 */
const FRAME_COST_SMOOTHING = 0.3

interface ThrottledEntry {
  minIntervalMs: number
  task: DataFrameTask
  lastRunAt: number
  pending: boolean
}

function defaultClock(): DataFrameClock {
  const hasRaf = typeof requestAnimationFrame === 'function' && typeof cancelAnimationFrame === 'function'
  return {
    now: () => (typeof performance !== 'undefined' ? performance.now() : Date.now()),
    request: (run, afterMs) => {
      if (!hasRaf) {
        const id = setTimeout(run, Math.max(0, afterMs))
        return () => clearTimeout(id)
      }
      const deadline = (typeof performance !== 'undefined' ? performance.now() : Date.now()) + Math.max(0, afterMs)
      let handle = 0
      const tick = () => {
        const now = typeof performance !== 'undefined' ? performance.now() : Date.now()
        if (now >= deadline) run()
        else handle = requestAnimationFrame(tick)
      }
      handle = requestAnimationFrame(tick)
      return () => cancelAnimationFrame(handle)
    },
    isHidden: () => (typeof document !== 'undefined' ? document.hidden : false),
  }
}

export function createDataFrame(options: DataFrameOptions = {}): DataFrame {
  const clock = options.clock ?? defaultClock()
  const pending = new Map<string, DataFrameTask>()
  const throttled = new Map<string, ThrottledEntry>()
  let cancelFrame: (() => void) | null = null
  let lastFrameAt = 0
  let costEstimateMs = FRAME_MIN_INTERVAL_MS / FRAME_LOAD_FACTOR
  let disposed = false

  function desiredInterval(): number {
    if (clock.isHidden()) return HIDDEN_FRAME_INTERVAL_MS
    return Math.min(MAX_PACED_INTERVAL_MS, Math.max(FRAME_MIN_INTERVAL_MS, costEstimateMs * FRAME_LOAD_FACTOR))
  }

  function ensureFrame() {
    if (cancelFrame || disposed) return
    if (pending.size === 0 && ![...throttled.values()].some((entry) => entry.pending)) return
    const elapsed = clock.now() - lastFrameAt
    const delay = Math.max(0, desiredInterval() - elapsed)
    cancelFrame = clock.request(runFrame, delay)
  }

  function runFrame() {
    cancelFrame = null
    if (disposed) return
    const frameStart = clock.now()
    lastFrameAt = frameStart

    const once = [...pending.values()]
    pending.clear()
    once.forEach((task) => task())

    const nowAfterOnce = clock.now()
    throttled.forEach((entry, key) => {
      if (!entry.pending) return
      if (nowAfterOnce - entry.lastRunAt < entry.minIntervalMs && entry.lastRunAt > 0) return
      entry.pending = false
      entry.lastRunAt = nowAfterOnce
      entry.task()
      if (throttled.get(key) === entry && entry.pending) {
        /* 任务内重排：以新任务为准。 */
      }
    })

    const cost = clock.now() - frameStart
    if (cost < FRAME_COST_OUTLIER_MS)
      costEstimateMs += (Math.max(cost, 0.1) - costEstimateMs) * FRAME_COST_SMOOTHING

    ensureFrame()
  }

  function schedule(key: string, task: DataFrameTask) {
    if (disposed) return
    pending.set(key, task)
    ensureFrame()
  }

  function scheduleThrottled(key: string, minIntervalMs: number, task: DataFrameTask) {
    if (disposed) return
    const entry = throttled.get(key)
    if (entry) {
      entry.minIntervalMs = minIntervalMs
      entry.task = task
      entry.pending = true
    } else {
      throttled.set(key, { minIntervalMs, task, lastRunAt: 0, pending: true })
    }
    ensureFrame()
  }

  function flush() {
    if (disposed || cancelFrame) return
    cancelFrame = clock.request(runFrame, 0)
  }

  function dispose() {
    disposed = true
    cancelFrame?.()
    cancelFrame = null
    pending.clear()
    throttled.clear()
  }

  return {
    schedule,
    scheduleThrottled,
    flush,
    dispose,
    get costMs() {
      return costEstimateMs
    },
  }
}

/* ---------- 模块级单例（与 ws/hub 同生命周期） ---------- */

export const dataFrame = createDataFrame()
