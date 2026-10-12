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

import {
  buildMissed,
  buildTickets,
  dimVotes,
  isPendingStatus,
  objectionsOf,
  splitReasons,
  ticketRisk,
} from '../decision-desk'
import type { Candidate, SignalItem } from '@/api/types/strategy'

function signal(patch: Partial<SignalItem>): SignalItem {
  return {
    id: 's1',
    symbol: '600519',
    name: '贵州茅台',
    side: 'buy',
    strategy: 'first_board_weak2strong',
    entry: 1682,
    entryLow: 1680,
    entryHigh: 1686,
    stop: 1650,
    take: 1736,
    score: 88,
    ruleScore: 80,
    aiScore: 91,
    degraded: false,
    highValue: true,
    evidence: ['F12', 'R07'],
    reason: '首板弱转强成立。研判：量能配合',
    status: 'approved',
    dims: {},
    time: '2026-10-12 09:45:00',
    tradeDate: '2026-10-12',
    atr: 12.5,
    positionPct: 0.08,
    validUntil: '2026-10-12 14:30:00',
    holdDaysMax: 3,
    exitKind: '',
    exitPrice: null,
    pnlPct: null,
    hasPnl: false,
    holdingDays: null,
    traceId: 't1',
    closedAt: '',
    ...patch,
  }
}

describe('一张票：状态分栏', () => {
  it('approved / risk_checking 进待执行，done / closed 进已执行，rejected 不进左栏', () => {
    const list = [
      signal({ id: 'a', status: 'approved' }),
      signal({ id: 'b', status: 'risk_checking' }),
      signal({ id: 'c', status: 'pending' }),
      signal({ id: 'd', status: 'done' }),
      signal({ id: 'e', status: 'closed', exitKind: 'take', hasPnl: true, pnlPct: 0.032 }),
      signal({ id: 'f', status: 'rejected' }),
      signal({ id: 'g', status: 'expired' }),
    ]
    const { pending, executed } = buildTickets(list)
    expect(pending.map((item) => item.id).sort()).toEqual(['a', 'b', 'c'])
    expect(executed.map((item) => item.id).sort()).toEqual(['d', 'e'])
    expect(executed.find((item) => item.id === 'e')?.closedNote).toContain('后验 3.2%')
  })

  it('待执行按综合分倒序', () => {
    const { pending } = buildTickets([signal({ id: 'low', score: 76 }), signal({ id: 'high', score: 92 })])
    expect(pending.map((item) => item.id)).toEqual(['high', 'low'])
  })

  it('isPendingStatus 与分栏同口径', () => {
    expect(isPendingStatus('approved')).toBe(true)
    expect(isPendingStatus('risk_checking')).toBe(true)
    expect(isPendingStatus('rejected')).toBe(false)
    expect(isPendingStatus('done')).toBe(false)
  })
})

describe('一张票：四维投票与异议', () => {
  it('四维按固定四行出，缺的维给 null；低于 50 记反对', () => {
    const votes = dimVotes({ technical: 82, news: 45 })
    expect(votes.map((vote) => vote.label)).toEqual(['技术面', '消息面', '资金面', '外围（情绪）'])
    expect(votes[0]).toMatchObject({ score: 82, against: false })
    expect(votes[1]).toMatchObject({ score: 45, against: true })
    expect(votes[2]).toMatchObject({ score: null, against: false })
    expect(votes[3]).toMatchObject({ score: null, against: false })
  })

  it('dims 值兼容对象形态 {score}，中文键也认', () => {
    const votes = dimVotes({ 技术面: { score: 70 }, capital: 38 })
    expect(votes[0]?.score).toBe(70)
    expect(votes[2]?.score).toBe(38)
    expect(votes[2]?.against).toBe(true)
  })

  it('异议先行：反对维在前，降级单独一条，不循环', () => {
    const votes = dimVotes({ technical: 30, news: 40, capital: 60, external: 55 })
    expect(objectionsOf(votes, false)).toEqual(['技术面 30 分，反对', '消息面 40 分，反对'])
    const degraded = objectionsOf(votes, true)
    expect(degraded).toHaveLength(3)
    expect(degraded[2]).toContain('模型降级')
  })
})

describe('一张票：理由拆条', () => {
  it('按句号分号切，去重，最多 5 条', () => {
    expect(splitReasons('首板弱转强成立。量能配合；情绪回暖')).toEqual(['首板弱转强成立', '量能配合', '情绪回暖'])
    expect(splitReasons('一。一。二。三。四。五。六')).toEqual(['一', '二', '三', '四', '五'])
    expect(splitReasons('')).toEqual([])
  })
})

describe('一张票：风控块', () => {
  it('读到风控参数时给允许仓位，超了写「夹掉的部分留在现金」', () => {
    const risk = ticketRisk(signal({ positionPct: 0.08 }), 0.05)
    expect(risk.requestedPct).toBe(0.08)
    expect(risk.allowedPct).toBe(0.05)
    expect(risk.note).toContain('夹掉的部分留在现金')
    expect(risk.note).toContain('以风控实检为准')
    expect(risk.rule).toBe('R07')
  })

  it('参数没读到就如实留空，不猜一个数', () => {
    const risk = ticketRisk(signal({ positionPct: 0.08, evidence: [] }), null)
    expect(risk.allowedPct).toBeNull()
    expect(risk.note).toBe('以风控实检为准')
    expect(risk.rule).toBe('')
  })
})

describe('一张票：漏选列', () => {
  function candidate(patch: Partial<Candidate>): Candidate {
    return {
      id: 'c1',
      tradeDate: '2026-10-12',
      strategy: 'pullback_dip',
      symbol: '000001',
      name: '平安银行',
      pool: 'watch',
      stage: 'scored',
      missReason: 'below_threshold',
      ruleScore: 60,
      aiScore: null,
      finalScore: null,
      signalId: '',
      refPrice: 11.2,
      retT1: null,
      retT3: null,
      retT5: null,
      hasT1: false,
      hasT3: false,
      hasT5: false,
      ...patch,
    }
  }

  it('只要漏斗里没入选且带原因的，按规则分倒序', () => {
    const rows = buildMissed([
      candidate({ id: 'a', stage: 'selected', missReason: '' }),
      candidate({ id: 'b', symbol: '000002', ruleScore: 55 }),
      candidate({ id: 'c', symbol: '000003', ruleScore: 70 }),
    ])
    expect(rows.map((row) => row.symbol)).toEqual(['000003', '000002'])
    expect(rows[0]?.reason).toBe('below_threshold')
  })
})
