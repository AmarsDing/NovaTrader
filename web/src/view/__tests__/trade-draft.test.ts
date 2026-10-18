import { describe, expect, it } from 'vitest'
import { draftPrice, normalizeBook, readPendingTrade, signalContextOf, signalDraftBlock } from '../trade-draft'

const now = Date.parse('2026-10-14T10:00:00+08:00')
const input = { signalId: 12, strategyVersionId: 9, book: 'paper', symbol: '600519.SH', side: 'buy', validUntil: '2026-10-14T10:01:00+08:00' }
const draft = { account: 'SIM', symbol: '600519.SH', side: 'buy' }

describe('signal draft identity', () => {
  it('接受数字参考价，拒绝非有限和非正价格，未给价时清空', () => {
    expect(draftPrice(12.34)).toBe('12.34')
    expect(draftPrice('12.34')).toBe('12.34')
    for (const value of [undefined, null, '', ' ', 0, -1, Infinity, 'NaN', {}]) expect(draftPrice(value)).toBe('')
  })

  it('账户只接受明确账本，paper映射SIM，缺失不能默认SIM', () => {
    expect(normalizeBook('paper')).toBe('SIM')
    expect(normalizeBook('LIVE')).toBe('LIVE')
    expect(normalizeBook(undefined)).toBeNull()
    expect(normalizeBook('unknown')).toBeNull()
  })

  it('完整保留int64身份，既有手工入口无需信号', () => {
    expect(signalContextOf(input)).toMatchObject({ signalId: '12', strategyVersionId: '9', book: 'SIM' })
    expect(signalContextOf({ symbol: input.symbol })).toBeNull()
    expect(signalDraftBlock(null, draft, now)).toBe('')
    expect(signalDraftBlock(signalContextOf(input), draft, now)).toBe('')
  })

  it('账户切换和缺失、证券或方向变化均阻断原信号，不能改投另一账本', () => {
    expect(signalDraftBlock(signalContextOf({ ...input, book: undefined }), draft, now)).toContain('账户未返回')
    expect(signalDraftBlock(signalContextOf(input), { ...draft, account: 'LIVE' }, now)).toContain('不一致')
    expect(signalDraftBlock(signalContextOf(input), { ...draft, symbol: '000001.SZ' }, now)).toContain('不属于')
    expect(signalDraftBlock(signalContextOf(input), { ...draft, side: 'sell' }, now)).toContain('不属于')
  })

  it('过期边界和无有效期不能提交', () => {
    expect(signalDraftBlock(signalContextOf(input), draft, now + 60_000)).toContain('已过期')
    expect(signalDraftBlock(signalContextOf({ ...input, validUntil: 'bad' }), draft, now)).toContain('有效期未返回')
    expect(signalDraftBlock(signalContextOf({ ...input, signalId: '' }), draft, now)).toContain('编号缺失')
    expect(signalDraftBlock(signalContextOf({ ...input, strategyVersionId: '' }), draft, now)).toContain('版本未归档')
  })

  it('持久化待核对编号可恢复，损坏记录不能当成没有委托', () => {
    const value = { clientOrderId: 'manual-original', account: 'SIM', symbol: '600519.SH', side: 'buy', price: 10, volume: 100, operator: 'tester' }
    expect(readPendingTrade({ getItem: () => JSON.stringify(value) }, 'key')).toMatchObject(value)
    expect(() => readPendingTrade({ getItem: () => '{"clientOrderId":"lost"}' }, 'key')).toThrow()
    expect(() => readPendingTrade({ getItem: () => 'bad json' }, 'key')).toThrow()
  })
})
