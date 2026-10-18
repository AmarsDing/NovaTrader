import { expect, it, vi } from 'vitest'
import { createRefreshQueue } from '../refresh-queue'

it('突发刷新合成一轮后续请求，期间没有并发且每批可完成', async () => {
  const releases: ((value: number) => void)[] = []
  const load = vi.fn(() => new Promise<number>((resolve) => releases.push(resolve)))
  const refresh = createRefreshQueue(load)
  const initial = refresh('SIM')
  const burst = Array.from({ length: 100 }, () => refresh('SIM'))
  expect(load).toHaveBeenCalledTimes(1)
  releases[0]!(1)
  expect(await initial).toBe(1)
  expect(load).toHaveBeenCalledTimes(2)
  const next = refresh('SIM')
  releases[1]!(2)
  expect(await Promise.all(burst)).toEqual(Array(100).fill(2))
  expect(load).toHaveBeenCalledTimes(3)
  releases[2]!(3)
  expect(await next).toBe(3)
})

it('不同账户独立刷新，失败不吞后续刷新且完成后可重试', async () => {
  const load = vi.fn(async (key: string) => { if (key === 'SIM' && load.mock.calls.length === 1) throw new Error('offline'); return key })
  const refresh = createRefreshQueue(load)
  await expect(refresh('SIM')).rejects.toThrow('offline')
  expect(await refresh('LIVE')).toBe('LIVE')
  expect(await refresh('SIM')).toBe('SIM')
})
