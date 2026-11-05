/**
 * 涨停梯队 FE-L3 契约：情绪轨道下沉为头部细条（阶段/切换时点/仓位系数），独立大组件已删。
 * - 细条 = 五段轨道当前高亮 + 阶段文案 + 当日切换时点（GetEmotionPath 盘面内推）+ 仓位系数；
 * - 路径未取到/截面不足 → 切换段显「—」不冒充（没取到≠0）；全天同阶段明说「全天未切换」。
 * §2.4 三页签收口 + FE-L2：默认高度梯队（行内首封时间+排序选项）/ 封板时间线（首封升序横道，
 * 缺时点排末段显「—」）/ 溢价与晋级；梯队/时间线行点击聚焦侧栏由 SymbolName 默认点击承接。
 */
import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'
import { request } from '@/api/http'
import LimitLadder from '@/panels/market/limit-ladder.panel.vue'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
vi.mock('@/ws/hub', () => ({ isLive: () => false, onPush: () => () => {}, onResync: () => () => {}, subscribe: () => () => {} }))

const sentiment = {
  phase: 'RECOVER',
  scoreCoef: 0.95,
  positionScale: 0.6,
  riskLevel: 2,
  upCount: 40,
  downCount: 5,
  brokenCount: 3,
  brokenRate: 0.07,
  maxHeight: 3,
  advance: 2800,
  decline: 1900,
  profitEffect: 0.012,
  asOf: '2026-11-02T10:30:00+08:00',
}

function point(time: string, phase: string) {
  return { time, auction: false, up: 30, down: 2, broken: 1, advance: 0, decline: 0, maxHeight: 0, brokenRate: 0, amount: 1, phase, scoreCoef: 0.95, stale: false }
}

const pathWithSwitch = {
  date: '2026-11-02',
  auctionFound: false,
  auctionTotalAmount: 0,
  auctionLimitUpCount: 0,
  prevTierAuctionPct: null,
  prevTierAuctionSample: 0,
  points: [point('2026-11-02T09:30:00+08:00', 'ICE'), point('2026-11-02T10:00:00+08:00', 'RECOVER')],
  partial: false,
  missing: [],
}

function mockApi(emotionPath: unknown = pathWithSwitch) {
  vi.mocked(request).mockImplementation(async <T>(path: string): Promise<T> => {
    if (path === '/api/market/emotion-path') return emotionPath as T
    if (path === '/api/market/sentiment') return sentiment as T
    return { items: [], total: [], found: false } as T
  })
}

/* 页签/排序偏好写 localStorage（script setup 每实例读档一档）——用例间互染须每例清档。 */
beforeEach(() => localStorage.clear())

it('FE-L3：细条显阶段/切换时点/仓位系数，切换时点取当日最近阶段变换', async () => {
  mockApi()
  const wrapper = shallowMount(LimitLadder)
  await flushPromises()
  const s = wrapper.find('.tape-strip')
  expect(s.exists()).toBe(true)
  expect(s.text()).toContain('修复 RECOVER')
  expect(s.text()).toContain('切换 10:00 转 修复')
  expect(s.text()).toContain('仓位系数 ×0.60')
  // 五段轨道：当前段（RECOVER）恰一段高亮。
  expect(s.findAll('.tape-strip__seg')).toHaveLength(5)
  expect(s.findAll('.tape-strip__seg.on')).toHaveLength(1)
})

it('FE-L3：全天同阶段明说「全天未切换」；路径未取到切换段显「—」不冒充', async () => {
  mockApi({ ...pathWithSwitch, points: [point('2026-11-02T09:30:00+08:00', 'WARM'), point('2026-11-02T10:00:00+08:00', 'WARM')] })
  let wrapper = shallowMount(LimitLadder)
  await flushPromises()
  expect(wrapper.find('.tape-strip').text()).toContain('切换 全天未切换')
  wrapper.unmount()

  // 路径请求失败：细条其余段照常，切换段显「—」。
  vi.mocked(request).mockImplementation(async <T>(path: string): Promise<T> => {
    if (path === '/api/market/emotion-path') throw new Error('路径服务不可用')
    if (path === '/api/market/sentiment') return sentiment as T
    return { items: [], total: [], found: false } as T
  })
  wrapper = shallowMount(LimitLadder)
  await flushPromises()
  const s = wrapper.find('.tape-strip')
  expect(s.exists()).toBe(true)
  expect(s.text()).toContain('切换 —')
  expect(s.text()).toContain('修复 RECOVER')
  wrapper.unmount()
})

/* ---------- §2.4 三页签 + FE-L2 ---------- */

function board(symbol: string, consecutive: number, firstSealAt: string, sealAmount: number, openCount = 0, status = 'SEALED') {
  return {
    symbol,
    trade_date: '2026-11-02',
    direction: 'UP',
    status,
    limit_price: 10,
    first_seal_at: firstSealAt,
    last_seal_at: firstSealAt,
    open_count: openCount,
    seal_amount: sealAmount,
    consecutive,
    as_of: '2026-11-02T10:30:00+08:00',
  }
}

const upBoards = [
  // 时间序≠金额序（600001 后封但封单更大）——两种排序口径才可区分。
  board('600001', 3, '2026-11-02T09:35:00+08:00', 9.8e8),
  board('600002', 3, '2026-11-02T09:31:00+08:00', 5.2e8),
  board('600003', 1, '2026-11-02T10:05:00+08:00', 1.1e8, 2),
  board('600004', 1, '', 0.6e8, 1, 'BROKEN'),
]

function mockBoards() {
  vi.mocked(request).mockImplementation(async <T>(path: string, init?: { query?: Record<string, unknown> }): Promise<T> => {
    if (path === '/api/market/emotion-path') return { ...pathWithSwitch } as T
    if (path === '/api/market/sentiment') return sentiment as T
    if (path === '/api/market/limits') return { items: init?.query?.direction === 'DOWN' ? [] : upBoards } as T
    return { items: [], total: [], found: false } as T
  })
}

/* shallowMount 会把 NtTabs/NtSegmented/NtLadderTiers 打成桩：页签内部、梯队分层都渲不出，
   须解桩；SymbolName 反向打文字桩（名称字典在测试环境无源，渲染 code 即可断言行归属）。 */
const DEEP_STUBS = {
  NtTabs: false,
  NtSegmented: false,
  NtLadderTiers: false,
  // 文字桩须显式声明 props，否则模板取不到 code 属性。
  SymbolName: { props: ['code'], template: '<span class="sym">{{ code }}</span>' },
}

it('三页签：默认高度梯队带行内首封时间，封板时间线按首封升序、缺时点排末段显「—」', async () => {
  mockBoards()
  const wrapper = shallowMount(LimitLadder, { global: { stubs: DEEP_STUBS } })
  await flushPromises()

  // 默认页签=高度梯队：3 板梯队内按首封时间带队（09:31 的 600002 先于 09:35 的 600001）；
  // 行内首封时间弱档随封单额同行出（FE-L2 行内封板时间）。
  const tiers = wrapper.findAll('.nt-ladder__tier')
  expect(tiers).toHaveLength(2)
  const firstTier = tiers[0]!
  expect(firstTier.find('.nt-ladder__head').text()).toContain('3 板')
  const firstTierStocks = firstTier.findAll('.nt-ladder__stock')
  expect(firstTierStocks[0]!.text()).toContain('600002')
  expect(firstTierStocks[1]!.text()).toContain('600001')
  expect(firstTierStocks[0]!.text()).toContain('09:31')
  // 溢价与晋级页签未激活时不渲。
  expect(wrapper.text()).not.toContain('昨日涨停 · 今日溢价')

  // 切封板时间线：首封时间升序，缺时点的 600004 排末段且时点显「—」、炸板弱档标记。
  const tabs = wrapper.findAll('.nt-tabs__item')
  expect(tabs.map((t) => t.text())).toEqual(['高度梯队 3', '封板时间线 4', '溢价与晋级'])
  await tabs[1]!.trigger('click')
  const rows = wrapper.findAll('.timeline li')
  expect(rows).toHaveLength(4)
  expect(rows[0]!.text()).toContain('09:31')
  expect(rows[0]!.text()).toContain('600002')
  expect(rows[1]!.text()).toContain('600001')
  expect(rows[2]!.text()).toContain('再封 ×2')
  const last = rows[3]!
  expect(last.text()).toContain('600004')
  expect(last.find('.timeline__time').text()).toBe('—')
  expect(last.text()).toContain('炸板')
  // FE-L2 行聚焦：时间线行内 SymbolName 默认点击即聚焦 quote-sidebar（SymbolName.vue 承接），此处断言行内挂载。
  expect(rows[0]!.find('.sym').text()).toBe('600002')
  wrapper.unmount()
})

it('FE-L2 排序选项：切「按封单额」梯队内按金额带队', async () => {
  mockBoards()
  const wrapper = shallowMount(LimitLadder, { global: { stubs: DEEP_STUBS } })
  await flushPromises()
  const sortButtons = wrapper.findAll('.nt-seg__item')
  expect(sortButtons.map((b) => b.text())).toEqual(['按首封时间', '按封单额'])
  await sortButtons[1]!.trigger('click')
  const tierList = wrapper.findAll('.nt-ladder__tier')
  const stocks = tierList[0]!.findAll('.nt-ladder__stock')
  // 梯队只收 SEALED（炸板单列在页签区外表格）；切金额序后 9.8 亿的 600001 反超先封的 600002 带队。
  expect(stocks[0]!.text()).toContain('600001')
  expect(stocks[1]!.text()).toContain('600002')
  // 首板梯队只剩 600003（600004 是炸板不进梯队）。
  expect(tierList[1]!.findAll('.nt-ladder__stock')).toHaveLength(1)
  wrapper.unmount()
})
