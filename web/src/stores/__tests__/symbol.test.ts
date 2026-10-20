/**
 * 全局当前证券 store（§8 恒随）：
 * - A 股归一与 backend/pkg/symbol.go FromAShareCode 同一代码段规则；
 * - setSymbol 空/同值短路，source 记录发起方；
 * - 名称走 §6.2 security 名称字典：fillName 落字典到自己代码那一格，慢响应盖不到当前票，
 *   切到已养过名的票立即回显。 */
import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSymbolStore } from '../symbol'

describe('symbol store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('A 股裸码归一为通达信形态，带后缀/前缀原样规整，认不出的透传', () => {
    const store = useSymbolStore()
    expect(store.setSymbol('600519', 'test')).toBe(true)
    expect(store.code).toBe('600519.SH')
    store.setSymbol('300750', 'test')
    expect(store.code).toBe('300750.SZ')
    store.setSymbol('920001', 'test')
    expect(store.code).toBe('920001.BJ')
    store.setSymbol('sh600519', 'test')
    expect(store.code).toBe('600519.SH')
    store.setSymbol('600519.sz', 'test')
    expect(store.code).toBe('600519.SZ')
    store.setSymbol('HSI', 'test')
    expect(store.code).toBe('HSI')
  })

  it('setSymbol 空值不写、同值短路只回填名称，切换记录发起方', () => {
    const store = useSymbolStore()
    expect(store.setSymbol('', 'test')).toBe(false)
    expect(store.code).toBe('')
    expect(store.setSymbol('600519', 'GP')).toBe(true)
    expect(store.source).toBe('GP')
    const at = store.switchedAt
    expect(store.setSymbol('600519.SH', 'other')).toBe(false)
    expect(store.switchedAt).toBe(at)
    expect(store.source).toBe('GP')
    // 同值时带名只更新显示名，不算一次切换。
    expect(store.setSymbol('600519.SH', 'other', '贵州茅台')).toBe(false)
    expect(store.name).toBe('贵州茅台')
    expect(store.setSymbol('000001', 'QB')).toBe(true)
    expect(store.code).toBe('000001.SZ')
    expect(store.source).toBe('QB')
    expect(store.name).toBe('')
  })

  it('fillName 落名称字典：只归自己代码那格，慢响应盖不到当前票；切到养过名的票直接回显', () => {
    const store = useSymbolStore()
    store.setSymbol('600519', 'GP')
    // 别的票的名称回填进字典但不显在当前票上（当前票字典缺名 → 空串）。
    store.fillName('000001.SZ', '平安银行')
    expect(store.name).toBe('')
    store.fillName('600519.SH', '贵州茅台')
    expect(store.name).toBe('贵州茅台')
    // §6.2 字典语义：走过一次的票已养名，切过去立即回显，不再发空窗。
    store.setSymbol('000001', 'QB')
    expect(store.name).toBe('平安银行')
    store.fillName('600519.SH', '贵州茅台')
    expect(store.name).toBe('平安银行')
  })
})
