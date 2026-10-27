/**
 * G5 panel-bus 参数快照：stage 两路（pending / 立即推送）都落快照，
 * peek 只读不消费、返回副本隔离；空参数 stage 不写入。
 * P0-1 进站归一：symbol 字符串在 stage 时归一成通达信形态（600519.SH），认不出的原文透传。
 */
import { describe, expect, it } from 'vitest'
import { consumePanelParams, onPanelParams, peekPanelParams, stagePanelParams } from '../panel-bus'

describe('peekPanelParams 快照回读', () => {
  it('stage 后（pending 未消费）peek 立即可见，裸码已归一', () => {
    stagePanelParams('g5-bus-a', { symbol: '600519' })
    expect(peekPanelParams('g5-bus-a')).toEqual({ symbol: '600519.SH' })
  })

  it('已挂订阅者的立即推送路径同样落快照', () => {
    const seen: Array<Record<string, unknown>> = []
    const off = onPanelParams('g5-bus-b', (p) => seen.push(p))
    stagePanelParams('g5-bus-b', { side: 'buy' })
    off()
    expect(seen).toEqual([{ side: 'buy' }])
    expect(peekPanelParams('g5-bus-b')).toEqual({ side: 'buy' })
  })

  it('peek 返回副本：改返回值不回写快照', () => {
    stagePanelParams('g5-bus-c', { symbol: '000001' })
    const copy = peekPanelParams('g5-bus-c')
    copy!.symbol = 'MUTATED'
    expect(peekPanelParams('g5-bus-c')).toEqual({ symbol: '000001.SZ' })
  })

  it('peek 是只读：consume 语义不变，快照不因 peek 消失', () => {
    // 用 3 开头深股（510300 属基金段不可推断市场，另用例锁透传）。
    stagePanelParams('g5-bus-d', { symbol: '300750' })
    expect(peekPanelParams('g5-bus-d')).toEqual({ symbol: '300750.SZ' })
    expect(peekPanelParams('g5-bus-d')).toEqual({ symbol: '300750.SZ' })
    expect(consumePanelParams('g5-bus-d')).toEqual({ symbol: '300750.SZ' })
    expect(consumePanelParams('g5-bus-d')).toBeUndefined()
    // 快照仍保留最近应用参数（发问场景面板已挂载消费后也要能回读）
    expect(peekPanelParams('g5-bus-d')).toEqual({ symbol: '300750.SZ' })
  })

  it('空参数 stage 不写入快照', () => {
    stagePanelParams('g5-bus-e', {})
    expect(peekPanelParams('g5-bus-e')).toBeUndefined()
  })

  it('认不出的 symbol（指数/港股等）原文透传，交给后端判错', () => {
    stagePanelParams('g5-bus-f', { symbol: 'HSI' })
    expect(peekPanelParams('g5-bus-f')).toEqual({ symbol: 'HSI' })
    // 51xxxx 基金/ETF 段按 A 股代码段规则不可推断市场，透传不猜。
    stagePanelParams('g5-bus-g', { symbol: '510300' })
    expect(peekPanelParams('g5-bus-g')).toEqual({ symbol: '510300' })
  })
})
