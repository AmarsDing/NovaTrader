import { describe, expect, it } from 'vitest'
import { buildGaps, countBlockedSources, missLabel, sourceBlocked, strategyLabel } from '../decision-desk'

const clear: Parameters<typeof buildGaps>[0] = {
  sourcesFailed: false,
  sourcesEmpty: false,
  sourcesBlocked: 0,
  noTape: false,
  briefingMissing: false,
  briefingStale: false,
  anyDegraded: false,
  paperDays: 20,
  killActive: false,
  live: true,
}

describe('决策台缺口', () => {
  it('链路齐时不报缺口', () => {
    expect(buildGaps(clear)).toEqual([])
  })

  it('急停和没快照排在模型降级前面', () => {
    const gaps = buildGaps({ ...clear, killActive: true, noTape: true, anyDegraded: true, live: false })
    expect(gaps.map((gap) => gap.id)).toEqual(['kill', 'tape', 'live', 'ai'])
    expect(gaps[0]?.level).toBe('block')
    expect(gaps[0]?.panel).toBe('risk-chain')
  })

  it('数据源整段失败时不再重复报「若干源不可用」', () => {
    const gaps = buildGaps({ ...clear, sourcesFailed: true, sourcesBlocked: 4, sourcesEmpty: true })
    expect(gaps.map((gap) => gap.id)).toEqual(['sources'])
  })

  it('晨报缺失和过期只留一条', () => {
    const gaps = buildGaps({ ...clear, briefingMissing: true, briefingStale: true })
    expect(gaps.map((gap) => gap.id)).toEqual(['brief'])
  })

  it('模拟盘未满 20 日要标明实盘关闭', () => {
    const gaps = buildGaps({ ...clear, paperDays: 3 })
    expect(gaps[0]?.text).toContain('3/20')
    expect(gaps[0]?.panel).toBe('trade')
  })

  it('天数未知不单独报缺口', () => {
    expect(buildGaps({ ...clear, paperDays: null })).toEqual([])
  })
})

describe('决策台文案', () => {
  it('策略和漏选原因翻成中文，未知代码原样保留', () => {
    expect(strategyLabel('first_board_weak2strong')).toBe('首板弱转强')
    expect(strategyLabel('pullback_dip')).toBe('强势回踩')
    expect(strategyLabel('custom')).toBe('custom')
    expect(missLabel('below_threshold')).toBe('综合分低于 75')
    expect(missLabel('')).toBe('—')
  })

  it('只统计已启用且断路的数据源', () => {
    expect(sourceBlocked('unavailable')).toBe(true)
    expect(sourceBlocked('closed')).toBe(false)
    expect(
      countBlockedSources([
        { enabled: true, status: 'unavailable' },
        { enabled: false, status: 'unavailable' },
        { enabled: true, status: 'closed' },
      ]),
    ).toBe(1)
  })
})
