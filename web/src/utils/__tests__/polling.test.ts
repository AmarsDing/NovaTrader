import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createVersionedLoader, createVisibilityPolling } from '../polling'

function spyVisibility(state: 'visible' | 'hidden') {
  return vi.spyOn(document, 'visibilityState', 'get').mockReturnValue(state)
}

describe('createVisibilityPolling', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('start 立即执行一次，此后按 interval 轮询；stop 后不再触发', async () => {
    spyVisibility('visible')
    const run = vi.fn().mockResolvedValue(undefined)
    const handle = createVisibilityPolling({ intervalMs: 1000, run })
    handle.start()
    expect(run).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(3000)
    expect(run).toHaveBeenCalledTimes(4)
    handle.stop()
    expect(handle.running).toBe(false)
    await vi.advanceTimersByTimeAsync(3000)
    expect(run).toHaveBeenCalledTimes(4)
  })

  it('页面不可见时停问，回见立即补一次', async () => {
    const spy = spyVisibility('hidden')
    const run = vi.fn().mockResolvedValue(undefined)
    const handle = createVisibilityPolling({ intervalMs: 1000, run })
    handle.start()
    await vi.advanceTimersByTimeAsync(3000)
    expect(run).toHaveBeenCalledTimes(0)
    spy.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(run).toHaveBeenCalledTimes(1)
    handle.stop()
  })

  it('上一次未回完不叠加下一次（单 flight）', async () => {
    spyVisibility('visible')
    let release!: () => void
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    const run = vi.fn().mockImplementation(() => gate)
    const handle = createVisibilityPolling({ intervalMs: 500, run })
    handle.start()
    await vi.advanceTimersByTimeAsync(2000)
    expect(run).toHaveBeenCalledTimes(1)
    release()
    handle.stop()
  })

  it('immediate=false 时 start 不立即执行', async () => {
    spyVisibility('visible')
    const run = vi.fn().mockResolvedValue(undefined)
    const handle = createVisibilityPolling({ intervalMs: 500, run, immediate: false })
    handle.start()
    expect(run).toHaveBeenCalledTimes(0)
    await vi.advanceTimersByTimeAsync(500)
    expect(run).toHaveBeenCalledTimes(1)
    handle.stop()
  })
})

describe('createVersionedLoader', () => {
  it('只有最后一枚 token 有效，invalidate 后全员失效', () => {
    const loader = createVersionedLoader()
    const t1 = loader.next()
    const t2 = loader.next()
    expect(loader.isCurrent(t1)).toBe(false)
    expect(loader.isCurrent(t2)).toBe(true)
    loader.invalidate()
    expect(loader.isCurrent(t2)).toBe(false)
    const t3 = loader.next()
    expect(loader.isCurrent(t3)).toBe(true)
  })
})
