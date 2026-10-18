import { afterEach, expect, it, vi } from 'vitest'
import { createPageRefresh } from '../page-refresh'

afterEach(() => vi.useRealTimers())

it('短窗第二次推送保证尾随，最后一批调用等待尾随完成', async () => {
  vi.useFakeTimers()
  vi.setSystemTime(1000)
  const read = vi.fn().mockResolvedValue(true)
  const page = createPageRefresh({ key: () => 'SIM', read, pushIntervalMs: 2000 })
  await page.fromPush()
  await vi.advanceTimersByTimeAsync(500)
  let finished = false
  const last = page.fromPush().then(() => { finished = true })
  await vi.advanceTimersByTimeAsync(1499)
  expect(read).toHaveBeenCalledTimes(1)
  expect(finished).toBe(false)
  await vi.advanceTimersByTimeAsync(1)
  await last
  expect(read).toHaveBeenCalledTimes(2)
  page.dispose()
})

it('同上下文慢读期间只有当前与一轮尾随，持续输入不无限延长首批等待', async () => {
  let release!: (value: boolean) => void
  const read = vi.fn().mockImplementationOnce(() => new Promise<boolean>((resolve) => { release = resolve }))
    .mockResolvedValue(true)
  const page = createPageRefresh({ key: () => 'SIM', read, pushIntervalMs: 0 })
  const first = page.refresh()
  const next = page.refresh()
  const third = page.refresh()
  expect(read).toHaveBeenCalledTimes(1)
  expect(next).toBe(third)
  release(true)
  await first
  await next
  expect(read).toHaveBeenCalledTimes(2)
  page.dispose()
})

it('切上下文不会执行旧尾随，卸载取消定时且完成等待', async () => {
  vi.useFakeTimers()
  vi.setSystemTime(1000)
  let key = 'A'
  const read = vi.fn().mockResolvedValue(true)
  const page = createPageRefresh({ key: () => key, read, pushIntervalMs: 2000 })
  await page.fromPush()
  const old = page.fromPush()
  key = 'B'
  await page.refresh()
  await vi.advanceTimersByTimeAsync(2000)
  expect(await old).toBe(false)
  expect(read.mock.calls.map(([value]) => value)).toEqual(['A', 'B'])
  const waiting = page.fromPush()
  page.dispose()
  expect(await waiting).toBe(false)
  await vi.advanceTimersByTimeAsync(5000)
  expect(read).toHaveBeenCalledTimes(2)
})
