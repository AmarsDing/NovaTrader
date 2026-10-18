/**
 * 行情跳动闪动钟（G1，参照 gloomberb components/quote-flash 移植）。
 *
 * 每 symbol 独立时钟：价格变动闪 FLASH_MS，随后 CALM_MS 冷静期——
 * 高频股每秒多次跳变，不隔开会让价格常驻变暗反而更难读。
 * 精确调度下一次状态翻转（不轮询）；无变化时不产新快照，避免无谓重渲。
 */
export type FlashDirection = 'up' | 'down' | 'flat'

export interface FlashSample {
  direction: FlashDirection
  /** 当前是否在闪动窗口内。 */
  flashing: boolean
}

interface FlashEntry {
  direction: FlashDirection
  /** 闪动结束时刻（now() 计）。 */
  flashEndAt: number
  /** 冷静期结束时刻；此后条目可被清理。 */
  calmEndAt: number
}

export interface QuoteFlashClock {
  now(): number
  defer(run: () => void, afterMs: number): () => void
}

export interface QuoteFlash {
  /**
   * 记录一次价格变动。闪动窗口内同方向再变：窗口顺延；
   * 冷静期内的变动只记录不闪（防常驻变暗）；
   * 方向反转立即重闪（反转本身是关键信息）。
   */
  mark(symbol: string, direction: FlashDirection): void
  /** 当前快照（每次内容变化才换新 Map，同一对象可直接做引用比较/memo）。 */
  snapshot(): ReadonlyMap<string, FlashSample>
  /** 内容变化时回调；返回退订函数。 */
  onChange(listener: (snapshot: ReadonlyMap<string, FlashSample>) => void): () => void
  /** 某 symbol 当前样本（无记录返回 undefined）。 */
  get(symbol: string): FlashSample | undefined
  dispose(): void
}

/** 闪动窗口（毫秒）。 */
export const QUOTE_FLASH_MS = 300
/** 冷静期（毫秒）。 */
export const QUOTE_FLASH_CALM_MS = 300

function defaultClock(): QuoteFlashClock {
  return {
    now: () => (typeof performance !== 'undefined' ? performance.now() : Date.now()),
    defer: (run, afterMs) => {
      const id = setTimeout(run, Math.max(0, afterMs))
      return () => clearTimeout(id)
    },
  }
}

function snapshotOf(entries: Map<string, FlashEntry>, now: number): Map<string, FlashSample> {
  const out = new Map<string, FlashSample>()
  entries.forEach((entry, symbol) => {
    out.set(symbol, { direction: entry.direction, flashing: now < entry.flashEndAt })
  })
  return out
}

function sameSamples(a: ReadonlyMap<string, FlashSample>, b: ReadonlyMap<string, FlashSample>): boolean {
  if (a.size !== b.size) return false
  for (const [key, sample] of a) {
    const other = b.get(key)
    if (!other || other.direction !== sample.direction || other.flashing !== sample.flashing) return false
  }
  return true
}

export function createQuoteFlash(clock: QuoteFlashClock = defaultClock()): QuoteFlash {
  const entries = new Map<string, FlashEntry>()
  const listeners = new Set<(snapshot: ReadonlyMap<string, FlashSample>) => void>()
  let current = new Map<string, FlashSample>()
  let cancelTimer: (() => void) | null = null
  let disposed = false

  function prune(now: number) {
    entries.forEach((entry, symbol) => {
      if (now >= entry.calmEndAt) entries.delete(symbol)
    })
  }

  function nextChangeAt(now: number): number | null {
    let at: number | null = null
    entries.forEach((entry) => {
      for (const boundary of [entry.flashEndAt, entry.calmEndAt]) {
        if (boundary > now && (at === null || boundary < at)) at = boundary
      }
    })
    return at
  }

  function reschedule() {
    cancelTimer?.()
    cancelTimer = null
    if (disposed) return
    const now = clock.now()
    const at = nextChangeAt(now)
    if (at !== null) cancelTimer = clock.defer(step, at - now)
  }

  function emitIfChanged(now: number) {
    const next = snapshotOf(entries, now)
    if (sameSamples(current, next)) return
    current = next
    listeners.forEach((listener) => listener(current))
  }

  function step() {
    cancelTimer = null
    if (disposed) return
    const now = clock.now()
    prune(now)
    emitIfChanged(now)
    reschedule()
  }

  function mark(symbol: string, direction: FlashDirection) {
    if (disposed || !symbol) return
    const now = clock.now()
    const existing = entries.get(symbol)
    if (existing && existing.direction === direction && now >= existing.flashEndAt) {
      // 冷静期内的同向再变：只顺延清理时间，不重闪。
      existing.calmEndAt = now + QUOTE_FLASH_CALM_MS
    } else {
      entries.set(symbol, {
        direction,
        flashEndAt: now + QUOTE_FLASH_MS,
        calmEndAt: now + QUOTE_FLASH_MS + QUOTE_FLASH_CALM_MS,
      })
    }
    emitIfChanged(now)
    reschedule()
  }

  function onChange(listener: (snapshot: ReadonlyMap<string, FlashSample>) => void) {
    listeners.add(listener)
    return () => listeners.delete(listener)
  }

  function dispose() {
    disposed = true
    cancelTimer?.()
    cancelTimer = null
    entries.clear()
    current = new Map()
    listeners.clear()
  }

  return {
    mark,
    snapshot: () => current,
    onChange,
    get: (symbol) => current.get(symbol),
    dispose,
  }
}

/* ---------- 模块级单例 ---------- */

export const quoteFlash = createQuoteFlash()
