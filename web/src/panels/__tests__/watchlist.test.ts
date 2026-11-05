/**
 * 自选/扫描面板命中表（W1/FE-P1，§7 评审 R1 修订）：
 * 量比占位列撤除（GetQuote 无该口径，留 BE-04）——改接 Quote 已有的 振幅/换手/涨·跌停 三列；
 * 振幅/涨跌停派生总闸门=prevClose>0，无涨跌幅限制显「无涨跌幅」不编价（没取到≠0）。
 */
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import WatchlistPanel from '../market/watchlist.panel.vue'
import NtTable from '@/ui/NtTable.vue'

const mock = vi.hoisted(() => ({
  scan: vi.fn(),
  listSignals: vi.fn(),
  getQuote: vi.fn(),
  getWatchlist: vi.fn(),
  setWatchlist: vi.fn(),
  alert: vi.fn(),
}))
vi.mock('@/api/strategy', () => ({ scan: mock.scan }))
vi.mock('@/api/signals', () => ({ listSignals: mock.listSignals }))
vi.mock('@/api/market', () => ({
  getQuote: mock.getQuote,
  getWatchlist: mock.getWatchlist,
  setWatchlist: mock.setWatchlist,
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isViewer: () => false, operatorName: 'tester' }) }))
vi.mock('@/workspace/inject', () => ({ getWorkbench: () => null }))
vi.mock('@/utils/notify', () => ({ raiseAlert: mock.alert }))

const QUOTE = {
  symbol: '600519.SH', name: '贵州茅台', market: 'SH', tradeStatus: 'TRADE',
  last: 1705, chg: -5, pctChg: -0.29, open: 1708, high: 1712, low: 1700, prevClose: 1710,
  volume: 21000, amount: 3.58e7, turnoverRate: 1.2, amplitude: 0.7,
  bid: [], ask: [], peTtm: 0, pb: 0, circMv: 0, totalMv: 0,
  limitUp: 1881, limitDown: 1539, noLimit: false,
  high52w: null, low52w: null, source: 'snap', asOf: '2026-11-02T10:30:00+08:00', stale: false, found: true,
}

let wrapper: VueWrapper | undefined

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  mock.getWatchlist.mockResolvedValue([])
  mock.setWatchlist.mockResolvedValue('2026-11-02T11:00:00+08:00')
  mock.scan.mockResolvedValue({
    traceId: 't1', universe: 10, matched: 1, ranked: 1, aiCalls: 1, degraded: false,
    signalIds: ['s1'], sellSignalIds: [], dropped: {}, misses: {}, killSwitch: false, elapsedMs: 12,
  })
  mock.listSignals.mockResolvedValue([{ id: 's1', symbol: '600519.SH' }])
})

describe('watchlist 命中表（W1/FE-P1）', () => {
  it('振幅/换手/涨·跌停 三列渲染 Quote 真值；量比占位列已撤（留 BE-04）', async () => {
    mock.getQuote.mockResolvedValue({ ...QUOTE })
    wrapper = mount(WatchlistPanel)
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text().includes('立即扫描'))!.trigger('click')
    await flushPromises()

    const table = wrapper.findComponent(NtTable)
    // NtTable 泛型 props 在 mount 包装下拿不到推断——props() 整取后窄化（同 audit-trail 测试先例）。
    const keys = ((table.props() as { columns: { key: string }[] }).columns).map((c) => c.key)
    expect(keys).toContain('amplitude')
    expect(keys).toContain('turnoverRate')
    expect(keys).toContain('limit')
    // R1：量比 GetQuote 未携带盘中口径——撤恒「—」占位列，不冒充。
    expect(keys).not.toContain('volRatio')

    const text = table.text()
    expect(text).toContain('0.70%') // 振幅（百分点口径 ppText）
    expect(text).toContain('1.20%') // 换手
    expect(text).toContain('1,881.00 / 1,539.00') // 涨/跌停价同格
  })

  it('无涨跌幅限制显「无涨跌幅」；prevClose=0 时振幅显「—」不编数', async () => {
    mock.getQuote.mockResolvedValue({ ...QUOTE, prevClose: 0, amplitude: 0, limitUp: 0, limitDown: 0, noLimit: true })
    wrapper = mount(WatchlistPanel)
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text().includes('立即扫描'))!.trigger('click')
    await flushPromises()
    const table = wrapper.findComponent(NtTable)
    const limitCell = table.find('td:nth-child(7)')
    expect(limitCell.text()).toContain('无涨跌幅')
    const ampCell = table.find('td:nth-child(5)')
    expect(ampCell.text()).toBe('—')
  })
})
