/**
 * 买卖标记双色制钉向测试：买→up、卖→down、未知→text3 一字锁定。
 * 改了映射就是改了全站买卖表达口径，必须连本测试一起过评审。
 */
import { describe, expect, it } from 'vitest'
import { fillSideTone } from '../market-markers'

describe('买卖标记双色制（红买绿卖，一字锁定）', () => {
  it('买→up 红', () => {
    expect(fillSideTone('buy')).toBe('up')
  })

  it('卖→down 绿', () => {
    expect(fillSideTone('sell')).toBe('down')
  })

  it('方向未知（空串/undefined/其他值）→ text3 中性，不默认红', () => {
    expect(fillSideTone('')).toBe('text3')
    expect(fillSideTone(undefined)).toBe('text3')
    expect(fillSideTone(null)).toBe('text3')
    expect(fillSideTone('hold')).toBe('text3')
  })
})
