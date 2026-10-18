import { describe, expect, it } from 'vitest'
import { canCancelOrder, orderStatusLabel } from '../format'

describe('trade服务委托状态', () => {
  it('实际已报和待人工确认状态都有中文含义且可撤', () => {
    expect(orderStatusLabel('submitted')).toBe('已报')
    expect(orderStatusLabel('pending_confirm')).toBe('待人工确认')
    expect(canCancelOrder('submitted')).toBe(true)
    expect(canCancelOrder('pending_confirm')).toBe(true)
  })

  it('终态与未知状态不能展示撤单操作', () => {
    for (const status of ['filled', 'cancelled', 'rejected', 'expired', 'unknown', '']) {
      expect(canCancelOrder(status)).toBe(false)
    }
  })
})
