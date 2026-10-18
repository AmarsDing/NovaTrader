import { describe, expect, it, vi } from 'vitest'
import {
  createDataFrame,
  FRAME_MIN_INTERVAL_MS,
  HIDDEN_FRAME_INTERVAL_MS,
  type DataFrameClock,
} from '@/ws/data-frame'

/** 手动时钟：now 单调推进；request 只保留最近一次登记并记录 afterMs。 */
function makeClock() {
  let now = 0
  let hidden = false
  let frame: { run: () => void; afterMs: number } | null = null
  const requested: number[] = []
  const clock: DataFrameClock = {
    now: () => now,
    request: (run, afterMs) => {
      frame = { run, afterMs }
      requested.push(afterMs)
      return () => {
        if (frame?.run === run) frame = null
      }
    },
    isHidden: () => hidden,
  }
  return {
    clock,
    get hidden() {
      return hidden
    },
    set hidden(value: boolean) {
      hidden = value
    },
    /** 下一帧的请求延迟；null 表示当前没有排帧。 */
    afterMs: () => frame?.afterMs ?? null,
    allAfterMs: () => [...requested],
    /** 推进时间并执行已排帧（若有）。 */
    step(ms: number) {
      now += ms
      const f = frame
      frame = null
      f?.run()
    },
  }
}

describe('data-frame 合帧', () => {
  it('同 key 一帧内后到覆盖先到，只执行最新任务', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    const calls: string[] = []
    df.schedule('limit-ladder', () => calls.push('stale'))
    df.schedule('limit-ladder', () => calls.push('fresh'))
    env.step(100)
    expect(calls).toEqual(['fresh'])
    df.dispose()
  })

  it('不同 key 同帧都执行', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    const calls: string[] = []
    df.schedule('a', () => calls.push('a'))
    df.schedule('b', () => calls.push('b'))
    env.step(100)
    expect(calls.sort()).toEqual(['a', 'b'])
    df.dispose()
  })

  it('可见页帧间隔不低于 66ms（约 15Hz）', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    df.schedule('a', vi.fn())
    const delay = env.afterMs()
    expect(delay).not.toBeNull()
    expect(delay!).toBeGreaterThanOrEqual(FRAME_MIN_INTERVAL_MS)
    df.dispose()
  })

  it('flush 立即安排一帧', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    df.flush()
    expect(env.afterMs()).toBe(0)
    df.dispose()
  })

  it('隐藏页退化为 1Hz 兜底', () => {
    const env = makeClock()
    env.hidden = true
    const df = createDataFrame({ clock: env.clock })
    df.schedule('a', vi.fn())
    expect(env.afterMs()).toBeGreaterThanOrEqual(HIDDEN_FRAME_INTERVAL_MS)
    df.dispose()
  })

  it('dispose 取消已排帧且任务不再执行', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    const task = vi.fn()
    df.schedule('a', task)
    df.dispose()
    env.step(1000)
    expect(task).not.toHaveBeenCalled()
  })
})

describe('data-frame 负载自适应', () => {
  it('帧成本显著上升后间隔被拉长', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    const initial = df.costMs
    // 帧任务本身花 200ms（模拟应用+提交成本），成本估计应显著抬升。
    df.schedule('heavy-task', () => {
      env.step(200)
    })
    env.step(env.afterMs() ?? FRAME_MIN_INTERVAL_MS)
    expect(df.costMs).toBeGreaterThan(initial)
    df.dispose()
  })

  it('超过 500ms 的帧视为毛刺，不进入成本估计', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    const before = df.costMs
    df.schedule('spike', () => {
      env.step(800)
    })
    env.step(env.afterMs() ?? FRAME_MIN_INTERVAL_MS)
    expect(df.costMs).toBe(before)
    df.dispose()
  })
})

describe('data-frame 业务节流', () => {
  it('scheduleThrottled 未到最小间隔不执行，到点执行', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    const task = vi.fn()
    df.scheduleThrottled('limit-ladder', 100, task)
    env.step(env.afterMs() ?? 0) // 首帧：首次立即执行
    expect(task).toHaveBeenCalledTimes(1)

    df.scheduleThrottled('limit-ladder', 100, task)
    env.step(50) // 距上次 50ms < 100，不执行
    expect(task).toHaveBeenCalledTimes(1)

    env.step(60) // 累计 110ms > 100，下一帧执行
    expect(task).toHaveBeenCalledTimes(2)
    df.dispose()
  })

  it('节流任务同 key 重排覆盖旧任务', () => {
    const env = makeClock()
    const df = createDataFrame({ clock: env.clock })
    const calls: string[] = []
    df.scheduleThrottled('k', 0, () => calls.push('old'))
    df.scheduleThrottled('k', 0, () => calls.push('new'))
    env.step(100)
    expect(calls).toEqual(['new'])
    df.dispose()
  })
})
