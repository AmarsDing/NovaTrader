/**
 * panel-prefs（W3-2）合同测试：
 * - 白名单逐条过滤：非法字段/非法值丢弃，合法条目保留；
 * - 读路径容错：坏 JSON/数组/非对象/无存储 → 空档，绝不抛；
 * - 写路径：只写 allow 字段，undefined 抹字段；空结果写 '{}'（不留脏尾）。
 */
import { describe, expect, it } from 'vitest'
import { loadPanelPrefs, savePanelPrefs, type PrefAllow } from '../panel-prefs'

const KEY = 'nt.prefs.test.v1'
const ALLOW: PrefAllow = {
  period: ['1m', '5m', '1d'],
  side: ['buy', 'sell'],
  pool: '*',
}

interface MemStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

function mem(initial: Record<string, string> = {}): MemStorage & { map: Record<string, string> } {
  const map = { ...initial }
  return {
    map,
    getItem: (k) => (k in map ? map[k] : null),
    setItem: (k, v) => {
      map[k] = v
    },
  }
}

describe('loadPanelPrefs', () => {
  it('只放行白名单内的字段与值，非法逐条丢弃', () => {
    const storage = mem({ [KEY]: JSON.stringify({ period: '5m', side: 'all', stray: 'x' }) })
    expect(loadPanelPrefs(KEY, ALLOW, storage)).toEqual({ period: '5m' })
  })

  it('坏 JSON / 数组 / 非对象 / 空档 → 空档不抛', () => {
    expect(loadPanelPrefs(KEY, ALLOW, mem({ [KEY]: '{bad' }))).toEqual({})
    expect(loadPanelPrefs(KEY, ALLOW, mem({ [KEY]: '[1,2]' }))).toEqual({})
    expect(loadPanelPrefs(KEY, ALLOW, mem({ [KEY]: '42' }))).toEqual({})
    expect(loadPanelPrefs(KEY, ALLOW, mem({}))).toEqual({})
  })

  it('无存储（undefined）→ 空档', () => {
    expect(loadPanelPrefs(KEY, ALLOW, undefined)).toEqual({})
  })

  it('动态字段（*）放行任意非空串，空串与非法类型仍丢弃', () => {
    const storage = mem({ [KEY]: JSON.stringify({ pool: '早盘池', side: 'buy', period: '1h' }) })
    expect(loadPanelPrefs(KEY, ALLOW, storage)).toEqual({ pool: '早盘池', side: 'buy' })
    const empty = mem({ [KEY]: JSON.stringify({ pool: '', side: 1 }) })
    expect(loadPanelPrefs(KEY, ALLOW, empty)).toEqual({})
  })
})

describe('savePanelPrefs', () => {
  it('只写白名单字段，undefined 抹字段', () => {
    const storage = mem()
    savePanelPrefs(KEY, ALLOW, { period: '1m', side: undefined, stray: 'x' }, storage)
    expect(JSON.parse(storage.map[KEY])).toEqual({ period: '1m' })
  })

  it('全抹时写 {} 且往返读出空档', () => {
    const storage = mem()
    savePanelPrefs(KEY, ALLOW, {}, storage)
    expect(storage.map[KEY]).toBe('{}')
    expect(loadPanelPrefs(KEY, ALLOW, storage)).toEqual({})
  })

  it('写入值不在白名单 → 丢弃该字段', () => {
    const storage = mem()
    savePanelPrefs(KEY, ALLOW, { side: 'hold' }, storage)
    expect(JSON.parse(storage.map[KEY])).toEqual({})
  })

  it('动态字段（*）原样写入，空串不写', () => {
    const storage = mem()
    savePanelPrefs(KEY, ALLOW, { pool: '尾盘池', side: '' }, storage)
    expect(JSON.parse(storage.map[KEY])).toEqual({ pool: '尾盘池' })
  })
})
