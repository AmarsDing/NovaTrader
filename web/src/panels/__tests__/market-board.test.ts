/**
 * 市场看板（market-board）契约锁定：
 * - 纯函数：cadenceMs 分档节奏 / splitSectorRanks 领涨领跌拆分（null 与 0 不编方向）/
 *   aggregateLadderMini 梯队聚合（SEALED 口径 + consecutive 分档，照抄 limit-ladder）/
 *   buildLeaderRows 龙头榜（avgPct null 排尾不冒充 0）/ alertKey 去重键；
 * - composable：三路节奏加载落 ref、WS move 帧前置去重（kind 白名单）、REST 重拉权威覆盖、
 *   单点失败留 fastError 且旧值不清屏（TSP placeholderData 原则）；
 * - 面板壳：九区 grid-area 齐整、数据穿透子件、异动行深链走 symbol store + openPanel('market')。
 */
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { IntradayAlert, LimitBoard, SectorRow, Sentiment } from '@/api/market'
import type { IndexQuoteView } from '@/api/indexSnapshot'
import NtAlertFeed from '@/display/NtAlertFeed.vue'
import NtIndexCards from '@/display/NtIndexCards.vue'
import NtKpiRow from '@/display/NtKpiRow.vue'
import NtSectorRank from '@/display/NtSectorRank.vue'
import NtTrendStrength from '@/display/NtTrendStrength.vue'
import MarketBoard from '@/panels/market/market-board.panel.vue'
import {
  aggregateLadderMini,
  alertKey,
  buildLeaderRows,
  cadenceMs,
  splitSectorRanks,
  useMarketBoard,
} from '@/panels/market/useMarketBoard'
import { useSymbolStore } from '@/stores/symbol'
import { registerWorkbench, unregisterWorkbench, type WorkbenchApi } from '@/workspace/inject'

const mocks = vi.hoisted(() => ({
  getSentimentDetail: vi.fn(),
  getIndexSnapshot: vi.fn(),
  listLimits: vi.fn(),
  getLadder: vi.fn(),
  getIntradayAlerts: vi.fn(),
  getEmotionPath: vi.fn(),
  listSectors: vi.fn(),
  getBreadth: vi.fn(),
  pushHandlers: [] as Array<(msg: unknown) => void>,
}))

vi.mock('@/api/market', () => ({
  getSentimentDetail: mocks.getSentimentDetail,
  listSectors: mocks.listSectors,
  listLimits: mocks.listLimits,
  getLadder: mocks.getLadder,
  getBreadth: mocks.getBreadth,
  getEmotionPath: mocks.getEmotionPath,
  getIntradayAlerts: mocks.getIntradayAlerts,
}))
vi.mock('@/api/indexSnapshot', () => ({ getIndexSnapshot: mocks.getIndexSnapshot }))
vi.mock('@/ws/hub', () => ({
  subscribe: () => () => {},
  onPush: (fn: (msg: unknown) => void) => {
    mocks.pushHandlers.push(fn)
    return () => {}
  },
}))

/* ---------- 夹具（字段按 api 类型精确构造） ---------- */

const SENTIMENT: Sentiment = {
  tradeDate: '2026-11-08',
  asOf: '2026-11-08T10:30:00+08:00',
  upCount: 45,
  downCount: 3,
  brokenCount: 12,
  brokenRate: 0.21,
  maxHeight: 5,
  profitEffect: 0.62,
  advance: 3100,
  decline: 1800,
  flat: 210,
  amount: 8.9e11,
  phase: 'WARM',
  riskLevel: 1,
  scoreCoef: 0.85,
  positionScale: 0.6,
  stale: false,
}

const INDICES: IndexQuoteView[] = [
  { code: '000001.SH', name: '上证指数', last: 3052.11, chg: 12.3, pctChg: 0.4, amplitude: 0.8, asOf: '', source: 'snap', stale: false, found: true },
  { code: '399001.SZ', name: '深证成指', last: 9876.54, chg: -23.1, pctChg: -0.23, amplitude: 1.1, asOf: '', source: '1d', stale: true, found: true },
]

function board(symbol: string, status: string, consecutive: number | null): LimitBoard {
  return {
    symbol,
    tradeDate: '2026-11-08',
    direction: 'UP',
    status,
    limitPrice: null,
    firstSealAt: '',
    lastSealAt: '',
    openCount: null,
    sealAmount: null,
    consecutive,
    asOf: '',
  }
}

const BOARDS: LimitBoard[] = [
  board('600519', 'SEALED', 5),
  board('000001', 'SEALED', 3),
  board('300750', 'SEALED', 1),
  board('601318', 'SEALED', 0), // consecutive=0 按首板档归组
  board('002594', 'BROKEN', 2),
]

const LADDER = {
  days: [],
  total: [
    { fromHeight: 1, sample: 20, succeeded: 9, rate: 0.45 },
    { fromHeight: 2, sample: 8, succeeded: 3, rate: 0.375 },
    { fromHeight: 3, sample: 5, succeeded: 2, rate: 0.4 },
    { fromHeight: 4, sample: 0, succeeded: 0, rate: 0 }, // 无样本档剔除
  ],
}

const ALERTS: IntradayAlert[] = [
  { symbol: '600519', name: '贵州茅台', kind: 'limit_up', time: '2026-11-08T10:01:00+08:00', pct: 10, price: 1800.5 },
  { symbol: '300750', name: '宁德时代', kind: 'surge', time: '2026-11-08T10:03:00+08:00', pct: 2.1, price: 180.2 },
]

const PATH = {
  date: '2026-11-08',
  auctionFound: true,
  auctionTotalAmount: 1.2e9,
  auctionLimitUpCount: 12,
  prevTierAuctionPct: 3.4,
  prevTierAuctionSample: 20,
  points: [
    { time: '2026-11-08T09:25:00+08:00', auction: true, up: 12, down: 1, broken: 0, brokenRate: 0, maxHeight: 2, profitEffect: 0, advance: 0, decline: 0, amount: 0, phase: 'RECOVER', scoreCoef: 1, stale: false },
    { time: '2026-11-08T09:35:00+08:00', auction: false, up: 20, down: 2, broken: 1, brokenRate: 0.05, maxHeight: 3, profitEffect: 0.1, advance: 0, decline: 0, amount: 0, phase: 'WARM', scoreCoef: 1, stale: false },
  ],
  partial: true,
  missing: [],
}

function sector(code: string, name: string, avgPct: number | null, leader = '', leaderReason = ''): SectorRow {
  return { code, name, rank: null, heat: null, avgPct, overseas: null, leader, upLimitCount: null, advanceRatio: null, amount: null, memberCount: 10, leaderReason, asOf: '' }
}

const SECTORS: SectorRow[] = [
  sector('BK001', '半导体', 2.1, '688981', '中芯国际放量领涨'),
  sector('BK002', '新能源车', 1.2, '300750', '宁德领涨'),
  sector('BK003', '房地产', -1.4, '600048', '保利领跌'),
  sector('BK004', '医药', -0.6),
  sector('BK005', '银行', 0),
  sector('BK006', '军工', null),
]

const BREADTH = {
  tradeDate: '2026-11-08',
  found: true,
  latest: { tradeDate: '2026-11-08', window: 20, above: 1800, below: 1500, flat: 200, total: 3500, ratio: 0.514, live: true, asOf: '2026-11-08T10:30:00+08:00' },
  days: [
    { tradeDate: '2026-11-07', window: 20, above: 1700, below: 1600, flat: 200, total: 3500, ratio: 0.5, live: false, asOf: '' },
    { tradeDate: '2026-11-06', window: 20, above: 1680, below: 1620, flat: 200, total: 3500, ratio: 0.48, live: false, asOf: '' },
  ],
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

beforeEach(() => {
  setActivePinia(createPinia())
  mocks.pushHandlers.length = 0
  for (const fn of [
    mocks.getSentimentDetail,
    mocks.getIndexSnapshot,
    mocks.listLimits,
    mocks.getLadder,
    mocks.getIntradayAlerts,
    mocks.getEmotionPath,
    mocks.listSectors,
    mocks.getBreadth,
  ]) {
    fn.mockReset()
  }
  mocks.getSentimentDetail.mockResolvedValue(SENTIMENT)
  mocks.getIndexSnapshot.mockResolvedValue({ asOf: '2026-11-08T10:30:00+08:00', indices: INDICES })
  mocks.listLimits.mockResolvedValue(BOARDS)
  mocks.getLadder.mockResolvedValue(LADDER)
  mocks.getIntradayAlerts.mockResolvedValue(ALERTS.map((a) => ({ ...a })))
  mocks.getEmotionPath.mockResolvedValue(PATH)
  mocks.listSectors.mockResolvedValue(SECTORS.map((s) => ({ ...s })))
  mocks.getBreadth.mockResolvedValue(BREADTH)
})

afterEach(() => {
  unregisterWorkbench()
})

/* ---------- 纯函数 ---------- */

describe('cadenceMs 分档节奏', () => {
  it('交易时段 fast 15s / mid 30s / slow 60s，非交易统一 120s', () => {
    expect(cadenceMs('fast', true)).toBe(15000)
    expect(cadenceMs('mid', true)).toBe(30000)
    expect(cadenceMs('slow', true)).toBe(60000)
    expect(cadenceMs('fast', false)).toBe(120000)
    expect(cadenceMs('mid', false)).toBe(120000)
    expect(cadenceMs('slow', false)).toBe(120000)
  })
})

describe('splitSectorRanks 领涨/领跌拆分', () => {
  it('领涨 avgPct>0 降序、领跌 avgPct<0 升序；0 与 null 不进榜', () => {
    const { gainers, losers } = splitSectorRanks(SECTORS, 5)
    expect(gainers.map((s) => s.code)).toEqual(['BK001', 'BK002'])
    expect(losers.map((s) => s.code)).toEqual(['BK003', 'BK004'])
  })

  it('top 截断', () => {
    const { gainers } = splitSectorRanks(SECTORS, 1)
    expect(gainers.map((s) => s.code)).toEqual(['BK001'])
  })
})

describe('aggregateLadderMini 梯队聚合', () => {
  it('SEALED/BROKEN 分家，封板率 = 封/(封+炸)，consecutive=0 归首板档', () => {
    const m = aggregateLadderMini(BOARDS, LADDER)
    expect(m.sealed).toBe(4)
    expect(m.broken).toBe(1)
    expect(m.sealRate).toBeCloseTo(4 / 5)
    expect(m.tiers).toEqual([
      { height: 5, count: 1 },
      { height: 3, count: 1 },
      { height: 1, count: 2 }, // consecutive 1 与 0 同档
    ])
  })

  it('晋级率仅保留 sample>0 档；≥3 板标 4+', () => {
    const m = aggregateLadderMini(BOARDS, LADDER)
    expect(m.steps).toEqual([
      { label: '1 进 2', rate: 0.45, sample: 20 },
      { label: '2 进 3', rate: 0.375, sample: 8 },
      { label: '3 进 4+', rate: 0.4, sample: 5 },
    ])
  })

  it('封炸总数为 0 时 sealRate=null（显 — 不冒充 0%）', () => {
    const m = aggregateLadderMini([], { days: [], total: [] })
    expect(m.sealRate).toBeNull()
    expect(m.tiers).toEqual([])
    expect(m.steps).toEqual([])
  })

  it('ladder 为 null 时只有梯队没有晋级率', () => {
    const m = aggregateLadderMini(BOARDS, null)
    expect(m.tiers.length).toBeGreaterThan(0)
    expect(m.steps).toEqual([])
  })
})

describe('buildLeaderRows 龙头榜', () => {
  it('只收有龙头的板块，avgPct 降序，null 排尾不冒充 0', () => {
    const rows = buildLeaderRows([
      sector('BK0A', '甲', null, '600001'),
      sector('BK0B', '乙', 1.5, '600002'),
      sector('BK0C', '丙', -0.8, '600003'),
      sector('BK0D', '丁', 9.9), // 无龙头不进榜
    ])
    expect(rows.map((r) => r.sectorCode)).toEqual(['BK0B', 'BK0C', 'BK0A'])
  })
})

describe('alertKey 去重键', () => {
  it('symbol|kind|time 三段拼', () => {
    expect(alertKey({ symbol: '600519', kind: 'limit_up', time: 't1' })).toBe('600519|limit_up|t1')
  })
})

/* ---------- composable 集成 ---------- */

describe('useMarketBoard 集成', () => {
  it('start 后三路数据落位并派生视图模型', async () => {
    const board = useMarketBoard()
    board.start()
    await sleep(50)
    expect(board.sentiment.value?.advance).toBe(3100)
    expect(board.indices.value).toHaveLength(2)
    expect(board.alerts.value.map((a) => a.symbol)).toEqual(['600519', '300750'])
    expect(board.emotionPoints.value).toHaveLength(2)
    expect(board.emotionPartial.value).toBe(true)
    expect(board.gainers.value.map((s) => s.code)).toEqual(['BK001', 'BK002'])
    expect(board.losers.value.map((s) => s.code)).toEqual(['BK003', 'BK004'])
    expect(board.ladderMini.value?.sealed).toBe(4)
    expect(board.ladderMini.value?.sealRate).toBeCloseTo(4 / 5)
    expect(board.leaderRows.value.map((r) => r.sectorCode)).toEqual(['BK001', 'BK002', 'BK003'])
    expect(board.breadth.value?.latest?.ratio).toBeCloseTo(0.514)
    expect(board.sentimentAsOfMs.value).toBe(Date.parse('2026-11-08T10:30:00+08:00'))
    expect(board.fastError.value).toBe('')
    board.stop()
  })

  it('WS move 帧前置 + 同键去重 + kind 白名单 + 非 move 忽略', async () => {
    const board = useMarketBoard()
    board.start()
    await sleep(50)
    const push = (msg: unknown) => mocks.pushHandlers.forEach((h) => h(msg))
    push({ topic: 'alert', event: 'move', data: { symbol: '000001', name: '平安银行', kind: 'break', time: '2026-11-08T10:05:00+08:00', pct: -1.2, price: 12.3 } })
    expect(board.alerts.value[0]?.symbol).toBe('000001')
    expect(board.liveCount.value).toBe(1)
    // 同 symbol|kind|time 去重，liveCount 不重复计。
    push({ topic: 'alert', event: 'move', data: { symbol: '000001', name: '平安银行', kind: 'break', time: '2026-11-08T10:05:00+08:00', pct: -1.2, price: 12.3 } })
    expect(board.alerts.value).toHaveLength(3)
    expect(board.liveCount.value).toBe(1)
    // kind 白名单外不前置。
    push({ topic: 'alert', event: 'move', data: { symbol: '000002', name: '万科A', kind: 'freak', time: 't', pct: 1, price: 9 } })
    expect(board.alerts.value).toHaveLength(3)
    // 非 move 事件忽略。
    push({ topic: 'alert', event: 'quote', data: { symbol: '000003', kind: 'surge', time: 't' } })
    expect(board.alerts.value).toHaveLength(3)
    // 无 symbol 帧忽略。
    push({ topic: 'alert', event: 'move', data: { kind: 'surge', time: 't' } })
    expect(board.alerts.value).toHaveLength(3)
    board.stop()
  })

  it('REST 重拉权威覆盖本地前置（seen 重置、整表替换）', async () => {
    const board = useMarketBoard()
    board.start()
    await sleep(50)
    mocks.pushHandlers.forEach((h) =>
      h({ topic: 'alert', event: 'move', data: { symbol: '000001', name: '平安银行', kind: 'break', time: 't9', pct: -1, price: 12 } }),
    )
    expect(board.alerts.value).toHaveLength(3)
    mocks.getIntradayAlerts.mockResolvedValue([
      { symbol: '600000', name: '浦发银行', kind: 'limit_up', time: '2026-11-08T10:02:00+08:00', pct: 9.9, price: 10.1 },
    ])
    await board.refreshAll()
    expect(board.alerts.value.map((a) => a.symbol)).toEqual(['600000'])
    board.stop()
  })

  it('页面隐藏时整轮不拉取（V7：回前台由 visibilitychange 补）', async () => {
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    try {
      const board = useMarketBoard()
      board.start()
      await sleep(50)
      expect(mocks.getSentimentDetail).not.toHaveBeenCalled()
      expect(mocks.getIndexSnapshot).not.toHaveBeenCalled()
      expect(mocks.getBreadth).not.toHaveBeenCalled()
      expect(mocks.listSectors).not.toHaveBeenCalled()
      board.stop()
    } finally {
      hidden.mockRestore()
    }
  })

  it('fast 组全灭：fastError 留文案、旧数据不清屏', async () => {
    const board = useMarketBoard()
    board.start()
    await sleep(50)
    mocks.getSentimentDetail.mockRejectedValue(new Error('boom'))
    mocks.getIndexSnapshot.mockRejectedValue(new Error('boom'))
    mocks.listLimits.mockRejectedValue(new Error('boom'))
    mocks.getLadder.mockRejectedValue(new Error('boom'))
    mocks.getIntradayAlerts.mockRejectedValue(new Error('boom'))
    await board.refreshAll()
    expect(board.fastError.value).toBe('boom')
    // 保留上次有效值（placeholderData 原则）。
    expect(board.sentiment.value?.advance).toBe(3100)
    expect(board.indices.value).toHaveLength(2)
    board.stop()
  })

  it('单源失败：只置该源 failed，ready 不复位、旧值不清屏、旁源不受扰', async () => {
    const board = useMarketBoard()
    board.start()
    await sleep(50)
    expect(board.sectorsReady.value).toBe(true)

    mocks.listSectors.mockRejectedValue(new Error('sector down'))
    await board.refreshAll()

    // 该源：failed 立起、ready 保持（旧数据仍在，不退回骨架）、fastError 不掺和。
    expect(board.sectorsFailed.value).toBe(true)
    expect(board.sectorsReady.value).toBe(true)
    expect(board.gainers.value.map((s) => s.code)).toEqual(['BK001', 'BK002'])
    expect(board.fastError.value).toBe('')
    // 旁源不受扰：情绪/指数仍 ready 且未标 failed。
    expect(board.sentimentReady.value).toBe(true)
    expect(board.sentimentFailed.value).toBe(false)
    expect(board.indicesReady.value).toBe(true)
    expect(board.indicesFailed.value).toBe(false)

    // 恢复后失败旗标自动清除（下次成功即回正常）。
    mocks.listSectors.mockResolvedValue(SECTORS.map((s) => ({ ...s })))
    await board.refreshAll()
    expect(board.sectorsFailed.value).toBe(false)
    board.stop()
  })

  it('首载失败：failed 立起但 ready 仍为 false（骨架让位给子件 missing 态）', async () => {
    mocks.listSectors.mockRejectedValue(new Error('sector down'))
    const board = useMarketBoard()
    board.start()
    await sleep(50)
    expect(board.sectorsReady.value).toBe(false)
    expect(board.sectorsFailed.value).toBe(true)
    // 面板绑定口径：loading = !ready && !failed → false，子件显 missing 而非永久骨架。
    expect(!board.sectorsReady.value && !board.sectorsFailed.value).toBe(false)
    board.stop()
  })
})

/* ---------- 面板壳 ---------- */

describe('market-board 面板壳', () => {
  it('九区 grid-area 齐整 + 数据穿透子件 + 异动行深链', async () => {
    const opened: Array<{ id: string; params?: Record<string, unknown> }> = []
    registerWorkbench({
      openPanel: (id: string, params?: Record<string, unknown>) => opened.push({ id, params }),
    } as unknown as WorkbenchApi)
    const wrapper = shallowMount(MarketBoard)
    await sleep(50)
    await flushPromises()

    const regions = wrapper.findAll('section[role="region"]')
    expect(regions.map((r) => r.attributes('aria-label'))).toEqual([
      '三大指数',
      '市场关键指标',
      '涨跌广度',
      '盘中情绪路径',
      '趋势强度',
      '涨停梯队概览',
      '板块热度榜',
      '盘中异动流',
      '板块龙头榜',
    ])
    for (const cls of ['board__idx', 'board__kpi', 'board__brd', 'board__emo', 'board__trd', 'board__lad', 'board__sec', 'board__alt', 'board__ldr']) {
      expect(wrapper.find(`.${cls}`).exists(), cls).toBe(true)
    }

    expect(wrapper.getComponent(NtKpiRow).props('sentiment')?.advance).toBe(3100)
    expect(wrapper.getComponent(NtIndexCards).props('indices')).toHaveLength(2)
    expect(wrapper.getComponent(NtAlertFeed).props('alerts')).toHaveLength(2)
    // ready/failed 二分绑定：成功路径 ready=true、loading=false（骨架不误显）。
    expect(wrapper.getComponent(NtTrendStrength).props('ready')).toBe(true)
    expect(wrapper.getComponent(NtTrendStrength).props('loading')).toBe(false)

    // 深链（D8）：异动行 select → symbol store 归一切票 + openPanel('market')。
    wrapper.getComponent(NtAlertFeed).vm.$emit('select', ALERTS[0])
    expect(useSymbolStore().code).toBe('600519.SH')
    expect(opened).toEqual([{ id: 'market', params: { symbol: '600519.SH' } }])

    wrapper.unmount()
  })

  it('板块源首载失败：NtSectorRank 收 loading=false / ready=false（骨架让位 missing，非永久骨架）', async () => {
    mocks.listSectors.mockRejectedValue(new Error('sector down'))
    const wrapper = shallowMount(MarketBoard)
    await sleep(50)
    await flushPromises()
    const rank = wrapper.getComponent(NtSectorRank)
    expect(rank.props('ready')).toBe(false)
    expect(rank.props('loading')).toBe(false)
    wrapper.unmount()
  })
})
