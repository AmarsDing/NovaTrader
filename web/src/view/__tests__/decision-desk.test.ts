import { describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { flushPromises, shallowMount } from '@vue/test-utils'
import SignalView from '../SignalView.vue'
import { request } from '@/api/http'
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
  buildDecisionAction,
  buildTickets,
  dimVotes,
  isPendingStatus,
  objectionsOf,
  splitReasons,
  ticketRisk,
  signalBook,
  hasAvailableSource,
  type DecisionContext,
} from '../decision-desk'
import type { Candidate, SignalItem } from '@/api/types/strategy'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
vi.mock('@/ws/hub', () => ({ isLive: () => true, onPush: () => () => {}, onResync: () => () => {} }))

function signal(patch: Partial<SignalItem>): SignalItem {
  return {
    id: 's1',
    book: 'paper',
    strategyVersionId: '2',
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
      signal({ id: 'e', status: 'closed', exitKind: 'take', hasPnl: true, pnlPct: 3.2 }),
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

  it('保护性卖出排在高分买入之前，保留票的账本和版本', () => {
    const { pending } = buildTickets([
      signal({ id: 'buy', score: 99 }),
      signal({ id: 'sell', side: 'sell', score: 60 }),
    ])
    expect(pending.map((row) => row.id)).toEqual(['sell', 'buy'])
    expect(pending[0]).toMatchObject({ book: 'paper', strategyVersionId: '2', traceId: 't1' })
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

describe('白话行动计划与执行准备', () => {
  const now = Date.parse('2026-10-12T10:00:00+08:00')
  const ready: DecisionContext = {
    checkedAt: now,
    accountBook: 'SIM',
    accountReady: true,
    positionsReady: true,
    signalsReady: true,
    riskReady: true,
    riskMode: 'L1',
    killActive: false,
    openHalted: false,
    haltReason: '',
    marketReady: true,
    marketAsOf: '2026-10-12T10:00:00+08:00',
    marketStale: false,
    sourcesReady: true,
    live: true,
    positions: [],
  }

  it('高分只表示计划，尚无实时触发实检时不宣称可以买', () => {
    const action = buildDecisionAction(signal({ score: 99 }), ready, now)
    expect(action.canOpenTrade).toBe(true)
    expect(action.title).toBe('买入计划待执行校验')
    expect(action.timing).toContain('最新价格仍在')
    expect(action.invalidation).toContain('超出入场区间')
    expect(action.caution).toContain('证据未完整')
  })

  it('探测中与未知源不算已确认可用', () => {
    expect(
      hasAvailableSource([
        { enabled: true, status: 'half_open' },
        { enabled: true, status: 'unknown' },
      ]),
    ).toBe(false)
    expect(hasAvailableSource([{ enabled: false, status: 'closed' }])).toBe(false)
    expect(hasAvailableSource([{ enabled: true, status: 'closed' }])).toBe(true)
  })

  it.each(['pending', 'risk_checking', 'rejected', 'expired', 'cancelled', 'unknown'])(
    '%s 状态不能进入交易准备',
    (status) => {
      const action = buildDecisionAction(signal({ status }), ready, now)
      expect(action.canOpenTrade).toBe(false)
      expect(action.blocker).not.toBe('')
    },
  )

  it('有效期到点立即禁用；无法解析的有效期也禁用', () => {
    expect(buildDecisionAction(signal({ validUntil: '2026-10-12T10:00:00+08:00' }), ready, now).blocker).toContain(
      '已过期',
    )
    expect(buildDecisionAction(signal({ validUntil: 'bad-date' }), ready, now).blocker).toContain('有效期缺失')
  })

  it('账本缺失或与账户不一致不能默认执行，版本缺失留给正式检查', () => {
    expect(signalBook('paper')).toBe('SIM')
    expect(signalBook('LIVE')).toBe('LIVE')
    expect(signalBook('')).toBe('')
    expect(buildDecisionAction(signal({ book: '' }), ready, now).blocker).toContain('账本不明确')
    expect(buildDecisionAction(signal({ book: 'live' }), ready, now).blocker).toContain('不一致')
    expect(buildDecisionAction(signal({ strategyVersionId: '' }), ready, now).blocker).toContain('策略版本')
    expect(buildDecisionAction(signal({ strategyVersionId: '0' }), ready, now).canOpenTrade).toBe(false)
  })

  it('接口失败、旧快照、断线、没有可用源与过期检查都不沿用可执行状态', () => {
    for (const patch of [
      { accountReady: false },
      { positionsReady: false },
      { riskReady: false },
      { signalsReady: false },
      { marketStale: true },
      { marketAsOf: '2026-10-12T09:50:00+08:00' },
      { live: false },
      { sourcesReady: false },
      { checkedAt: now - 46_000 },
    ]) {
      expect(buildDecisionAction(signal({}), { ...ready, ...patch }, now).canOpenTrade).toBe(false)
    }
  })

  it('急停与对账停买阻断买入；人工保护性退出仍能准备', () => {
    expect(buildDecisionAction(signal({}), { ...ready, killActive: true }, now).blocker).toContain('急停')
    expect(
      buildDecisionAction(signal({}), { ...ready, openHalted: true, haltReason: '数量不一致' }, now).blocker,
    ).toContain('数量不一致')
    const context = {
      ...ready,
      killActive: true,
      riskMode: 'L0',
      positions: [{ symbol: '600519.SH', quantity: 100, available: 100 }],
    }
    const action = buildDecisionAction(signal({ side: 'sell', reason: '触及止损' }), context, now)
    expect(action.title).toBe('退出计划待执行校验')
    expect(action.timing).toContain('触及止损')
    expect(action.canOpenTrade).toBe(true)
  })

  it('卖出票缺可卖数量时明确 T+1 约束，买出票已有持仓时阻断重复开仓', () => {
    const context = { ...ready, positions: [{ symbol: '600519.SH', quantity: 100, available: 0 }] }
    expect(buildDecisionAction(signal({ side: 'sell' }), context, now).blocker).toContain('T+1')
    expect(buildDecisionAction(signal({}), context, now).blocker).toContain('已持有')
  })

  it('已开仓与已经平仓使用不同结论，执行中不重复提交', () => {
    expect(buildDecisionAction(signal({ status: 'done' }), null, now).title).toContain('已开仓')
    expect(buildDecisionAction(signal({ status: 'closed' }), null, now).title).toContain('已平仓')
    expect(buildDecisionAction(signal({ status: 'executing' }), ready, now).canOpenTrade).toBe(false)
  })

  it('主要反证直接显示，不被高综合分覆盖', () => {
    const action = buildDecisionAction(
      signal({ score: 99, dims: { technical: 95, news: 30, capital: 90, external: 80 } }),
      ready,
      now,
    )
    expect(action.caution).toBe('消息面 30 分，反对')
  })

  it('价格计划不合法时阻断，实盘计划本轮不能准备执行', () => {
    expect(buildDecisionAction(signal({ stop: 1700 }), ready, now).blocker).toContain('价格不完整')
    expect(
      buildDecisionAction(signal({ book: 'live' }), { ...ready, accountBook: 'LIVE', riskMode: 'L3' }, now).blocker,
    ).toContain('实盘执行通道尚未验收')
  })
})

describe('信号页行动入口', () => {
  function mockReadOnlyRequests(patch: Record<string, unknown>, riskFailed = false) {
    const now = Date.now()
    vi.mocked(request).mockImplementation(async <T>(path: string): Promise<T> => {
      let result: unknown = {}
      if (path === '/api/strategy/signals')
        result = {
          items: [
            {
              ...signal({}),
              validUntil: new Date(now + 60_000).toISOString(),
              finalScore: 99,
              stopLoss: 1650,
              takeProfit: 1736,
              ...patch,
            },
          ],
        }
      if (path === '/api/trade/account') result = { account: 'SIM', equity: 100000, cash: 100000 }
      if (path === '/api/trade/positions') result = { items: [] }
      if (path === '/api/risk/state') {
        if (riskFailed) throw new Error('风控未连接')
        result = { mode: 'L1', killSwitch: { active: false } }
      }
      if (path === '/api/market/sentiment') result = { upCount: 100, downCount: 50, asOf: new Date(now).toISOString() }
      if (path === '/api/datahub/sources') result = { items: [{ source: 'test', state: 'closed', enabled: true }] }
      return result as T
    })
  }

  it('被拒绝的高分票仍展示原因，原生交易按钮明确禁用', async () => {
    mockReadOnlyRequests({ status: 'rejected', reason: 'R07 仓位超限' })
    const wrapper = shallowMount(SignalView, { global: { plugins: [createPinia()] } })
    try {
      await flushPromises()
      expect(wrapper.text()).toContain('主要阻断R07 仓位超限')
      expect(wrapper.get('button.go').attributes()).toHaveProperty('disabled')
    } finally {
      wrapper.unmount()
    }
  })

  it('风控读取失败不会让 approved 票显示成可执行', async () => {
    mockReadOnlyRequests({ status: 'approved' }, true)
    const wrapper = shallowMount(SignalView, { global: { plugins: [createPinia()] } })
    try {
      await flushPromises()
      expect(wrapper.text()).toContain('风控状态未返回')
      expect(wrapper.get('button.go').attributes()).toHaveProperty('disabled')
    } finally {
      wrapper.unmount()
    }
  })
})
