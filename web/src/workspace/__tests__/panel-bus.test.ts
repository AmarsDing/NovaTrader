/**
 * G5 panel-bus 参数快照：stage 两路（pending / 立即推送）都落快照，
 * peek 只读不消费、返回副本隔离；空参数 stage 不写入。
 */
import { describe, expect, it } from 'vitest'
import { consumePanelParams, onPanelParams, peekPanelParams, stagePanelParams } from '../panel-bus'

describe('peekPanelParams 快照回读', () => {
  it('stage 后（pending 未消费）peek 立即可见', () => {
    stagePanelParams('g5-bus-a', { symbol: '600519' })
    expect(peekPanelParams('g5-bus-a')).toEqual({ symbol: '600519' })
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
    expect(peekPanelParams('g5-bus-c')).toEqual({ symbol: '000001' })
  })

  it('peek 是只读：consume 语义不变，快照不因 peek 消失', () => {
    stagePanelParams('g5-bus-d', { symbol: '510300' })
    expect(peekPanelParams('g5-bus-d')).toEqual({ symbol: '510300' })
    expect(peekPanelParams('g5-bus-d')).toEqual({ symbol: '510300' })
    expect(consumePanelParams('g5-bus-d')).toEqual({ symbol: '510300' })
    expect(consumePanelParams('g5-bus-d')).toBeUndefined()
    // 快照仍保留最近应用参数（发问场景面板已挂载消费后也要能回读）
    expect(peekPanelParams('g5-bus-d')).toEqual({ symbol: '510300' })
  })

  it('空参数 stage 不写入快照', () => {
    stagePanelParams('g5-bus-e', {})
    expect(peekPanelParams('g5-bus-e')).toBeUndefined()
  })
})
