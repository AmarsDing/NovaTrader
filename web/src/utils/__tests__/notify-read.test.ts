/**
 * G3 通知逐条已读态持久化：去重截断语义、坏存储容错、save/load 往返。
 */
import { describe, expect, it } from 'vitest'
import { capNotifReadIds, loadNotifReadIds, NOTIF_READ_CAP, NOTIF_READ_KEY, saveNotifReadIds } from '../notify-read'

function fakeStorage(initial: Record<string, string> = {}) {
  const data = { ...initial }
  return {
    getItem: (key: string) => (key in data ? data[key] : null),
    setItem: (key: string, value: string) => {
      data[key] = value
    },
    dump: () => ({ ...data }),
  }
}

describe('loadNotifReadIds', () => {
  it('无键 / 无 storage → 空集', () => {
    expect(loadNotifReadIds(fakeStorage())).toEqual([])
    expect(loadNotifReadIds(undefined)).toEqual([])
  })

  it('坏 JSON / 非数组 → 空集（读路径容错）', () => {
    expect(loadNotifReadIds(fakeStorage({ [NOTIF_READ_KEY]: '{oops' }))).toEqual([])
    expect(loadNotifReadIds(fakeStorage({ [NOTIF_READ_KEY]: '{"a":1}' }))).toEqual([])
  })

  it('过滤非字符串与空串，去重截断', () => {
    const stored = JSON.stringify(['a', 1, '', 'a', 'b', null])
    expect(loadNotifReadIds(fakeStorage({ [NOTIF_READ_KEY]: stored }))).toEqual(['a', 'b'])
  })
})

describe('capNotifReadIds', () => {
  it('去重保首个，截断到容量（新已读在头部，最老被挤出）', () => {
    const ids = Array.from({ length: NOTIF_READ_CAP + 10 }, (_, i) => `id-${i}`)
    const capped = capNotifReadIds(['x', ...ids, 'x'])
    expect(capped).toHaveLength(NOTIF_READ_CAP)
    expect(capped[0]).toBe('x')
    expect(capped.at(-1)).toBe(`id-${NOTIF_READ_CAP - 2}`)
  })
})

describe('save/load 往返', () => {
  it('写入即截断，读回一致', () => {
    const st = fakeStorage()
    const ids = Array.from({ length: NOTIF_READ_CAP + 5 }, (_, i) => `m-${i}`)
    saveNotifReadIds(ids, st)
    const back = loadNotifReadIds(st)
    expect(back).toHaveLength(NOTIF_READ_CAP)
    expect(back[0]).toBe('m-0')
  })
})
