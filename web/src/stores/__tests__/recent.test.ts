/**
 * recent store 纪律测试（G2）：联想数据源只收 6 位代码。
 * 关键约束：自由文本绝不写入持久化（防敏感文本泄漏进联想列表）。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, it } from 'vitest'
import { useRecentStore } from '../recent'

const KEY = 'nt.recent-tickers.v1'

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})

it('推入 6 位代码：最新在前并去重', () => {
  const recent = useRecentStore()
  recent.pushTicker('600519')
  recent.pushTicker('000001')
  recent.pushTicker('600519')
  expect(recent.tickers).toEqual(['600519', '000001'])
})

it('非代码一律拒收：自由文本/短码/超长码/前后缀形态', () => {
  const recent = useRecentStore()
  recent.pushTicker('复盘备注：茅台放量')
  recent.pushTicker('60051')
  recent.pushTicker('6005199')
  recent.pushTicker('600519.SH')
  recent.pushTicker('')
  expect(recent.tickers).toEqual([])
  expect(localStorage.getItem(KEY)).toBeNull()
})

it('容量封顶 20 条，最老的被淘汰', () => {
  const recent = useRecentStore()
  for (let i = 0; i < 25; i += 1) recent.pushTicker(String(600000 + i))
  expect(recent.tickers).toHaveLength(20)
  expect(recent.tickers[0]).toBe('600024')
  expect(recent.tickers.at(-1)).toBe('600005')
})

it('写入 localStorage 并可跨实例恢复', () => {
  const recent = useRecentStore()
  recent.pushTicker('600519')
  recent.pushTicker('000001')
  // 最新在前：000001 后推，排第一位。
  expect(JSON.parse(localStorage.getItem(KEY) ?? '[]')).toEqual(['000001', '600519'])

  setActivePinia(createPinia())
  const restored = useRecentStore()
  expect(restored.tickers).toEqual(['000001', '600519'])
})

it('历史存储污染净化：混进的非法项载入时被过滤', () => {
  localStorage.setItem(KEY, JSON.stringify(['600519', '脏文本', '123', '000001']))
  const recent = useRecentStore()
  expect(recent.tickers).toEqual(['600519', '000001'])
})

it('损坏 JSON 回空不崩溃', () => {
  localStorage.setItem(KEY, '{oops')
  const recent = useRecentStore()
  expect(recent.tickers).toEqual([])
})

it('clear 清空并持久化', () => {
  const recent = useRecentStore()
  recent.pushTicker('600519')
  recent.clear()
  expect(recent.tickers).toEqual([])
  expect(JSON.parse(localStorage.getItem(KEY) ?? '[]')).toEqual([])
})
