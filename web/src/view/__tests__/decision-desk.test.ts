import { describe, expect, it, vi, afterEach } from 'vitest'
import { createPinia } from 'pinia'
import { flushPromises, shallowMount } from '@vue/test-utils'
import SignalView from '../SignalView.vue'
import OverviewView from '../OverviewView.vue'
import { request } from '@/api/http'
import { stagePanelParams } from '@/workspace/panel-bus'
import { buildGaps, countBlockedSources, missLabel, sourceBlocked, strategyLabel } from '../decision-desk'

/* FE-O5：竞价强弱条只在竞价时段（09:15–09:30 上海）显——组件用 serverNow 判定，测试经 mock 钟控时。
 * vi.mock 提升（hoisted）须搭档 vi.hoisted 的可变钟；每个用例后回拨真实钟，不惊动既有用例。 */
const clockCtl = vi.hoisted(() => ({ now: Date.now() }))
vi.mock('@/utils/server-clock', () => ({ serverNow: () => clockCtl.now }))
/* FE-O2 漏选钻取断言：工作台注入换桩——漏选行点击应 openPanel('signals', {symbol})。
 * importOriginal 保住 registerWorkbench/useWorkbench 等其余导出（挂载子树里有间接引用）。 */
const workbench = vi.hoisted(() => ({ openPanel: vi.fn() }))
vi.mock('@/workspace/inject', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/workspace/inject')>()),
  getWorkbench: () => workbench,
}))
afterEach(() => {
  clockCtl.now = Date.now()
  workbench.openPanel.mockClear()
})

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

describe('FE-O1 可交易状态卡', () => {
  it('链路齐是常态不着色；只有告警转 warn，有阻断转 danger 且值只数阻断', () => {
    expect(buildTradeState([])).toEqual({ value: '链路齐', tone: 'neutral', sub: '链路检查全部通过' })
    const warnOnly = buildTradeState([{ id: 'brief', level: 'warn', text: '', panel: 'morning-briefing' }])
    expect(warnOnly).toMatchObject({ value: '链路齐', tone: 'warn' })
    expect(warnOnly.sub).toContain('1 项告警')
    const blocked = buildTradeState([
      { id: 'kill', level: 'block', text: '', panel: 'risk-chain' },
      { id: 'tape', level: 'block', text: '', panel: 'datasource-health' },
      { id: 'brief', level: 'warn', text: '', panel: 'morning-briefing' },
    ])
    expect(blocked.value).toBe('阻断 2')
    expect(blocked.tone).toBe('danger')
    expect(blocked.sub).toContain('1 项告警')
  })

  it('阻断且无告警时子句只留定位指引', () => {
    const blocked = buildTradeState([{ id: 'kill', level: 'block', text: '', panel: 'risk-chain' }])
    expect(blocked.value).toBe('阻断 1')
    expect(blocked.sub).toBe('点击下方清单定位')
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
  buildAuctionStrip,
  buildDecisionAction,
  buildTickets,
  buildTradeState,
  dimVotes,
  groupMissed,
  isPendingStatus,
  laneBadge,
  laneRemainText,
  objectionsOf,
  splitReasons,
  ticketRisk,
  signalBook,
  hasAvailableSource,
  type DecisionContext,
} from '../decision-desk'
import type { AuctionSummary } from '@/api/market'
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

  it('FE-O3 不再截断：超过 8 条的漏选全量返回', () => {
    const rows = buildMissed(
      Array.from({ length: 12 }, (_, i) =>
        candidate({ id: `m${i}`, symbol: `0001${String(i).padStart(2, '0')}`, ruleScore: 50 + i }),
      ),
    )
    expect(rows).toHaveLength(12)
    expect(rows[0]?.ruleScore).toBe(61)
  })

  it('FE-O3 分组：按 missReason 聚合计数头，组按条数降序，组内保持规则分倒序', () => {
    const groups = groupMissed(
      buildMissed([
        candidate({ id: 'a', symbol: '000001', missReason: 'below_threshold', ruleScore: 55 }),
        candidate({ id: 'b', symbol: '000002', missReason: 'below_threshold', ruleScore: 70 }),
        candidate({ id: 'c', symbol: '000003', missReason: 'pool_filtered', ruleScore: 80 }),
      ]),
    )
    expect(groups.map((group) => [group.reason, group.count])).toEqual([
      ['below_threshold', 2],
      ['pool_filtered', 1],
    ])
    // 计数头给中文口径；组内不重新排序，沿用 buildMissed 的规则分倒序。
    expect(groups[0]?.label).toBe('综合分低于 75')
    expect(groups[0]?.rows.map((row) => row.ruleScore)).toEqual([70, 55])
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
    /* W0-3b/FE-O6：行动计划块已收编 TicketAction 子组件，shallowMount 须取消打桩才断言得到正文。
       W2 收编：票体全量走 TicketDetail——正文链路=SignalView→TicketDetail→TicketAction，两层都解桩。 */
    const wrapper = shallowMount(SignalView, {
      global: { plugins: [createPinia()], stubs: { TicketDetail: false, TicketAction: false } },
    })
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
    const wrapper = shallowMount(SignalView, {
      global: { plugins: [createPinia()], stubs: { TicketDetail: false, TicketAction: false } },
    })
    try {
      await flushPromises()
      expect(wrapper.text()).toContain('风控状态未返回')
      expect(wrapper.get('button.go').attributes()).toHaveProperty('disabled')
    } finally {
      wrapper.unmount()
    }
  })

  it('W2 收编：卡片全量走 TicketDetail——出场事实带持有天数、四维评语入投票块、已平仓不给下单钮', async () => {
    mockReadOnlyRequests({
      status: 'closed',
      reason: '突破前高放量封板。',
      exitPrice: 12.3,
      closedAt: new Date(Date.now() - 86_400_000).toISOString(),
      exitKind: '止盈',
      hasPnl: true,
      pnlPct: 5.2,
      holdingDays: 2,
      dims: { technical: { score: 88, comment: '放量突破' } },
      evidence: ['封单 2.1 亿'],
    })
    const wrapper = shallowMount(SignalView, {
      global: { plugins: [createPinia()], stubs: { TicketDetail: false, TicketAction: false } },
    })
    try {
      await flushPromises()
      const text = wrapper.text()
      // 票头了结注（closedNote 单源）+ 价格块出场事实一行（含持有天数）。
      expect(text).toContain('已了结（止盈，后验 5.2%）')
      expect(text).toContain('出场 12.30')
      expect(text).toContain('了结方式 止盈')
      expect(text).toContain('持有 2 日')
      // 四维评语收编进投票块（不再走卡内私有 ul）。
      expect(text).toContain('放量突破')
      expect(text).not.toContain('准备模拟盘确认')
    } finally {
      wrapper.unmount()
    }
  })

  it('FE-O2 漏选钻取：经总线暂存的 symbol 参数预置代码搜索词，卡片列表过滤到目标票', async () => {
    const now = Date.now()
    vi.mocked(request).mockImplementation(async <T>(path: string): Promise<T> => {
      let result: unknown = {}
      if (path === '/api/strategy/signals')
        result = {
          items: [
            { ...signal({ id: 'a', symbol: '600519.SH' }), validUntil: new Date(now + 60_000).toISOString() },
            { ...signal({ id: 'b', symbol: '000001.SZ', name: '平安银行' }), validUntil: new Date(now + 60_000).toISOString() },
          ],
        }
      if (path === '/api/trade/account') result = { account: 'SIM', equity: 100000, cash: 100000 }
      if (path === '/api/trade/positions') result = { items: [] }
      if (path === '/api/risk/state') result = { mode: 'L1', killSwitch: { active: false } }
      if (path === '/api/market/sentiment') result = { upCount: 100, downCount: 50, asOf: new Date(now).toISOString() }
      if (path === '/api/datahub/sources') result = { items: [{ source: 'test', state: 'closed', enabled: true }] }
      return result as T
    })
    // 决策台漏选行点击 → openPanel('signals', {symbol})：总线在面板未挂载时进 pending，挂载后一次性消费。
    stagePanelParams('signals', { symbol: '600519' })
    const wrapper = shallowMount(SignalView, { global: { plugins: [createPinia()] } })
    try {
      await flushPromises()
      // 暂存参数进站归一为 600519.SH，搜索词预置后只留目标票一张卡。
      expect(wrapper.findAll('article.card')).toHaveLength(1)
    } finally {
      wrapper.unmount()
    }
  })
})

describe('FE-O2 一张票：状态徽标与有效期倒计时', () => {
  const now = Date.parse('2026-10-12T10:00:00+08:00')
  const future = new Date(now + 30 * 60_000).toISOString()
  const past = new Date(now - 60_000).toISOString()

  it('未终态且有效期已过按「已过期」论；终态不做过期覆盖', () => {
    expect(laneBadge({ status: 'approved', validUntil: past }, now)?.text).toBe('已过期')
    expect(laneBadge({ status: 'done', validUntil: past }, now)?.text).toBe('已有成交')
    expect(laneBadge({ status: 'closed', validUntil: past }, now)?.text).toBe('已平仓')
    expect(laneBadge({ status: 'rejected', validUntil: past }, now)?.text).toBe('已拦截')
    expect(laneBadge({ status: 'approved', validUntil: future }, now)?.text).toBe('已通过')
  })

  it('canceled 归一到 cancelled；册外状态返回 null（模板原样回显，禁自封文案）', () => {
    expect(laneBadge({ status: 'canceled', validUntil: '' }, now)?.text).toBe('已取消')
    expect(laneBadge({ status: 'brand_new', validUntil: '' }, now)).toBeNull()
  })

  it('有效期倒计时：缺写「无有效期」，过点/无法解析写「已过期」，未过点写剩余分钟', () => {
    expect(laneRemainText('', now)).toBe('无有效期')
    expect(laneRemainText('bad-date', now)).toBe('已过期')
    expect(laneRemainText(past, now)).toBe('已过期')
    expect(laneRemainText(future, now)).toBe('剩 30 分')
  })
})

describe('FE-O5 竞价强弱条（纯函数）', () => {
  const auctionNow = Date.parse('2026-11-03T09:20:00+08:00')
  const morningNow = Date.parse('2026-11-03T10:20:00+08:00')
  const summary: AuctionSummary = {
    tradeDate: '2026-11-03',
    found: true,
    totalAmount: 5.2e9,
    limitUpCount: 12,
    asOf: '2026-11-03 09:25:01',
    limitUps: [
      { symbol: '600001.SH', auctionPct: 9.98, auctionAmount: 1e8, auctionVolRatio: 3.2, auctionTurnover: 1.1, auctionLimitUp: true, auctionUnmatched: 12_000 },
      { symbol: '600002.SH', auctionPct: 9.97, auctionAmount: 2e8, auctionVolRatio: 2.1, auctionTurnover: 0.8, auctionLimitUp: true, auctionUnmatched: null },
    ],
    topRush: [
      // 与涨停榜重复出现的行只参与涨幅/未匹配聚合，样本只数按代码去重。
      { symbol: '600002.SH', auctionPct: 9.97, auctionAmount: 2e8, auctionVolRatio: 2.1, auctionTurnover: 0.8, auctionLimitUp: true, auctionUnmatched: null },
      { symbol: '000001.SZ', auctionPct: 4.5, auctionAmount: 3e8, auctionVolRatio: 5.5, auctionTurnover: 2.0, auctionLimitUp: false, auctionUnmatched: -5_000 },
    ],
  }

  it('非竞价时段整条不渲（宿主的时段判定走 serverNow）', () => {
    expect(buildAuctionStrip(summary, morningNow)).toBeNull()
    expect(buildAuctionStrip(null, morningNow)).toBeNull()
  })

  it('样本按代码去重；均涨幅与未匹配净买按样本行聚合', () => {
    const strip = buildAuctionStrip(summary, auctionNow)
    expect(strip?.found).toBe(true)
    expect(strip?.sampleCount).toBe(3)
    expect(strip?.avgPct).toBeCloseTo((9.98 + 9.97 + 9.97 + 4.5) / 4, 3)
    expect(strip?.unmatchedShares).toBe(7_000)
    expect(strip?.totalAmount).toBe(5.2e9)
    expect(strip?.limitUpCount).toBe(12)
    expect(strip?.asOf).toBe('2026-11-03 09:25:01')
  })

  it('样本全缺涨幅/未匹配 → null 显「—」（没取到≠0）；found=false 表达截面未落库', () => {
    const strip = buildAuctionStrip({ ...summary, found: false, limitUps: [], topRush: [], asOf: '' }, auctionNow)
    expect(strip?.found).toBe(false)
    expect(strip?.sampleCount).toBe(0)
    expect(strip?.avgPct).toBeNull()
    expect(strip?.unmatchedShares).toBeNull()
  })

  it('接口未应答（summary null）时段内仍出缺口条，交给模板写「竞价接口未应答」', () => {
    const strip = buildAuctionStrip(null, auctionNow)
    expect(strip?.found).toBe(false)
    expect(strip?.sampleCount).toBe(0)
  })
})

describe('决策台票卡与竞价条（集成）', () => {
  // SymbolName 打桩须显式声明 props，否则模板里 {{ code }} 渲染为空（既有教训①）。
  const SYMBOL_STUB = { props: ['code'], template: '<span class="sym">{{ code }}</span>' }

  function mockDeskRequests(init?: {
    signals?: Array<Record<string, unknown>>
    candidates?: Array<Record<string, unknown>>
    auction?: Record<string, unknown> | 'fail'
  }) {
    vi.mocked(request).mockImplementation(async <T>(path: string): Promise<T> => {
      let result: unknown = {}
      if (path === '/api/strategy/signals') result = { items: init?.signals ?? [] }
      if (path === '/api/trade/account') result = { account: 'SIM', equity: 1_000_000, cash: 400_000 }
      if (path === '/api/trade/positions') result = { items: [] }
      if (path === '/api/risk/state') result = { mode: 'L1', killSwitch: { active: false } }
      if (path === '/api/market/sentiment')
        result = { upCount: 2100, downCount: 1800, asOf: new Date(clockCtl.now).toISOString() }
      if (path === '/api/strategy/candidates') result = { items: init?.candidates ?? [] }
      if (path === '/api/intel/hotwords') result = { items: [] }
      if (path === '/api/brain/briefing/latest') result = { summary: '', items: [] }
      if (path === '/api/datahub/sources') result = { items: [{ source: 'test', state: 'closed', enabled: true }] }
      if (path === '/api/market/auction') {
        if (init?.auction === 'fail') throw new Error('竞价服务未应答')
        result = init?.auction ?? { trade_date: '2026-11-03', found: false, limit_ups: [], top_rush: [], as_of: '' }
      }
      if (path === '/api/config') result = { values: {} }
      return result as T
    })
  }

  function mountDesk() {
    return shallowMount(OverviewView, {
      global: { plugins: [createPinia()], stubs: { NtBadge: false, SymbolName: SYMBOL_STUB } },
    })
  }

  it('FE-O2：待执行票卡出状态徽标/入场区间/有效期倒计时；漏选行点击钻取信号中枢', async () => {
    clockCtl.now = Date.parse('2026-11-03T09:20:00+08:00')
    mockDeskRequests({
      signals: [{ ...signal({ id: 'p1' }), validUntil: new Date(clockCtl.now + 30 * 60_000).toISOString() }],
      candidates: [
        { symbol: '000001.SZ', name: '平安银行', stage: 'scored', miss_reason: 'below_threshold', rule_score: 60 },
      ],
    })
    const wrapper = mountDesk()
    try {
      await flushPromises()
      const text = wrapper.text()
      // 票头行：signal 册徽标；副行：方向/综合分/入场区间/有效期倒计时。
      expect(text).toContain('已通过')
      expect(text).toContain('买入')
      expect(text).toContain('入场 1,680.00–1,686.00')
      expect(text).toContain('剩 30 分')
      // 漏选行整行可点：跳信号中枢并带代码定位（验收：漏选每行 2 次点击内到达解释）。
      await wrapper.get('button.miss-row').trigger('click')
      expect(workbench.openPanel).toHaveBeenCalledWith('signals', { symbol: '000001.SZ' })
    } finally {
      wrapper.unmount()
    }
  })

  it('FE-O2/O5：已执行票卡出了结注与成交均价/股数；竞价条时段内出全量读数', async () => {
    clockCtl.now = Date.parse('2026-11-03T09:20:00+08:00')
    mockDeskRequests({
      signals: [{ ...signal({ id: 'e1', status: 'done' }), executedPrice: 1685.5, executedVolume: 200 }],
      auction: {
        trade_date: '2026-11-03',
        found: true,
        total_amount: 5.2e9,
        limit_up_count: 12,
        as_of: '2026-11-03 09:25:01',
        limit_ups: [
          { symbol: '600001.SH', auction_pct: 10, auction_amount: 1e8, auction_limit_up: true, auction_unmatched: 12_000 },
          { symbol: '600002.SH', auction_pct: 8, auction_amount: 2e8, auction_limit_up: true },
        ],
        top_rush: [{ symbol: '000001.SZ', auction_pct: 6, auction_amount: 3e8, auction_unmatched: -5_000 }],
      },
    })
    const wrapper = mountDesk()
    try {
      await flushPromises()
      const text = wrapper.text()
      // 已执行卡：了结注（closedNote 单源）+ 成交均价/股数副行。
      expect(text).toContain('已有成交 200 股')
      expect(text).toContain('成交均价 1,685.50')
      // 竞价条（09:20 时段内，found=true）：涨停家数/竞价额/样本/均涨幅/未匹配净买/截面时点。
      const strip = wrapper.get('.auction-strip')
      expect(strip.text()).toContain('涨停 12 家')
      expect(strip.text()).toContain('竞价额 52.00 亿')
      expect(strip.text()).toContain('样本 3 只')
      expect(strip.text()).toContain('均涨幅 8.00%')
      expect(strip.text()).toContain('未匹配净买 0.70 万股')
      expect(strip.text()).toContain('截面 2026-11-03 09:25:01')
    } finally {
      wrapper.unmount()
    }
  })

  it('FE-O5：竞价接口未应答写缺口文案且不拖主表错误；非竞价时段整条不渲', async () => {
    clockCtl.now = Date.parse('2026-11-03T09:20:00+08:00')
    mockDeskRequests({ auction: 'fail' })
    const wrapper = mountDesk()
    try {
      await flushPromises()
      expect(wrapper.get('.auction-strip').text()).toContain('竞价接口未应答')
      // 竞价是辅助条：失败不进主表错误（独立成败语义）。
      expect(wrapper.text()).not.toContain('竞价服务未应答')
    } finally {
      wrapper.unmount()
    }

    // 时段外（10:20 上午连续竞价）：同一接口数据整条不渲。
    clockCtl.now = Date.parse('2026-11-03T10:20:00+08:00')
    const offSession = mountDesk()
    try {
      await flushPromises()
      expect(offSession.find('.auction-strip').exists()).toBe(false)
    } finally {
      offSession.unmount()
    }
  })

  it('FE-O1/O3：四卡重排（权益/系统退场）；今日风控缺数据显「—」；漏选按原因分组出计数头', async () => {
    clockCtl.now = Date.parse('2026-11-03T10:20:00+08:00')
    mockDeskRequests({
      candidates: [
        { symbol: '000001.SZ', name: '平安银行', stage: 'scored', miss_reason: 'below_threshold', rule_score: 60 },
        { symbol: '000002.SZ', name: '万科A', stage: 'scored', miss_reason: 'below_threshold', rule_score: 58 },
        { symbol: '600001.SH', name: '浦发银行', stage: 'scored', miss_reason: 'pool_filtered', rule_score: 80 },
      ],
    })
    // 卡面文案要进断言：NtStatCard 解桩渲染；NtBadge 照 mountDesk 惯例解桩（徽注行要读文本）。
    const wrapper = shallowMount(OverviewView, {
      global: { plugins: [createPinia()], stubs: { NtBadge: false, NtStatCard: false, SymbolName: SYMBOL_STUB } },
    })
    try {
      await flushPromises()
      const text = wrapper.text()
      // FE-O1：四卡=当日盈亏/可交易状态/情绪/今日风控；权益与系统标签退场。
      const cardLabels = wrapper.findAllComponents({ name: 'NtStatCard' }).map((card) => card.props('label'))
      expect(cardLabels).toEqual(['当日盈亏', '可交易状态', '情绪', '今日风控'])
      expect(text).not.toContain('权益')
      // 情绪卡收编涨/跌家数与炸板率（mock 情绪 2100/1800，炸板率未给显「—」）。
      expect(text).toContain('涨 2100 / 跌 1800')
      expect(text).toContain('炸板率 —')
      // 今日风控：state 未给 buysToday/参数册 → 「—」不猜数；日报未 mock 路径 → fulfilled 空报，拦截「—」。
      expect(text).toContain('买入 —/—')
      expect(text).toContain('拦截 —')
      // 链路徽注行承接原系统卡的实时性与模式。
      expect(wrapper.get('.link-strip').text()).toContain('模式 L1')
      // FE-O3：组头=中文口径+聚合计数，条数多的组在前；三行漏选全量渲染不截断。
      const heads = wrapper.findAll('.miss-group h4')
      expect(heads).toHaveLength(2)
      expect(heads[0]?.text()).toContain('综合分低于 75')
      expect(heads[0]?.text()).toContain('2')
      expect(wrapper.findAll('button.miss-row')).toHaveLength(3)
    } finally {
      wrapper.unmount()
    }
  })
})
