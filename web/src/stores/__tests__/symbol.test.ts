/**
 * 全局当前证券 store（§8 恒随）：
 * - A 股归一与 backend/pkg/symbol.go FromAShareCode 同一代码段规则；
 * - setSymbol 空/同值短路，source 记录发起方；fillName 不许慢响应盖掉新切换。
 */
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

  it('fillName 只接受当前代码的名称，防慢响应盖掉新切换', () => {
    const store = useSymbolStore()
    store.setSymbol('600519', 'GP')
    store.fillName('000001.SZ', '平安银行')
    expect(store.name).toBe('')
    store.fillName('600519.SH', '贵州茅台')
    expect(store.name).toBe('贵州茅台')
    store.setSymbol('000001', 'QB')
    store.fillName('600519.SH', '贵州茅台')
    expect(store.name).toBe('')
  })
})
