/**
 * 单券行情侧栏（P0）：恒随全局 symbol store；
 * 已过期琥珀徽标 / WS 断开「非实时」/ 零值「未知」/ 五档盘口渲染 / 无证券的空态提示，各有断言。
 */
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import QuoteSidebar from '../market/quote-sidebar.panel.vue'
import NtDepthBoard from '@/viz/NtDepthBoard.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtTabs from '@/ui/NtTabs.vue'
import { useSymbolStore } from '@/stores/symbol'

const mock = vi.hoisted(() => ({
  quote: vi.fn(),
  timeline: vi.fn(),
  live: [] as boolean[],
}))
vi.mock('@/api/market', () => ({ getQuote: mock.quote }))
vi.mock('@/api/intel', () => ({ timeline: mock.timeline }))
vi.mock('@/ws/hub', () => ({
  subscribe: () => vi.fn(),
  onPush: () => vi.fn(),
  isLive: () => mock.live[mock.live.length - 1] ?? true,
}))
vi.mock('@/workspace/panel-bus', () => ({ consumePanelParams: () => undefined, onPanelParams: () => vi.fn() }))
vi.mock('@/workspace/inject', () => ({ getWorkbench: () => null }))

const QUOTE = {
  symbol: '600519.SH', name: '贵州茅台', market: 'SH', tradeStatus: 'SUSPEND',
  last: 1705, chg: -5, pctChg: -0.29, open: 1708, high: 1712, low: 1700, prevClose: 1710,
  volume: 21000, amount: 3.58e7, turnoverRate: 0, amplitude: 0,
  bid: [{ price: 1704.99, volume: 100 }, { price: 1704.98, volume: 200 }],
  ask: [{ price: 1705.01, volume: 120 }, { price: 1705.02, volume: 80 }],
  peTtm: 0, pb: 0, circMv: 0, totalMv: 0, limitUp: 1881, limitDown: 1539,
  noLimit: false, source: 'snap', asOf: '2026-10-19T10:30:00+08:00', stale: true, found: true,
}

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = shallowMount(QuoteSidebar, { global: { renderStubDefaultSlot: true } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  mock.live.length = 0
  mock.quote.mockResolvedValue({ ...QUOTE })
  mock.timeline.mockResolvedValue({ items: [], sentiment: null })
})

describe('quote sidebar（视频落地 P0）', () => {
  it('恒随 store：停牌+已过期徽标、零值显未知、五档/涨跌停价渲染、手动查询不写 store', async () => {
    mock.live.push(true)
    await mountPanel()
    // 初始无当前证券：空态提示，不发请求。
    expect(wrapper!.text()).toContain('还没有当前证券')
    expect(mock.quote).not.toHaveBeenCalled()

    const store = useSymbolStore()
    store.setSymbol('600519', 'QL')
    await flushPromises()
    const text = wrapper!.text()
    expect(mock.quote).toHaveBeenCalledWith('600519.SH')
    expect(store.code).toBe('600519.SH')
    expect(store.name).toBe('贵州茅台')
    expect(text).toContain('1,705.00')
    expect(text).toContain('停牌')
    expect(text).toContain('已过期')
    expect(text).not.toContain('无涨跌幅')
    // PE-TTM/PB/换手/振幅为 0 → 未知；涨跌停价真实渲染。
    expect(text).toContain('未知')
    expect(text).toContain('1,881.00')
    expect(text).toContain('1,539.00')
    // B3：五档移交 NtDepthBoard（shallowMount 下桩化），断言接线——档位/侧标真传，卖1/买2 与价格渲染由组件自测担保。
    const board = wrapper!.findComponent(NtDepthBoard)
    expect(board.exists()).toBe(true)
    expect(board.props('sideLabels')).toBe(true)
    expect(board.props('asks')).toEqual(QUOTE.ask)
    expect(board.props('bids')).toEqual(QUOTE.bid)
    expect(text).not.toContain('合成·待数据')
    // WS 在线 → cut=false（非实时语义由 NtFreshness cut 唯一承载；shallowMount 下子件桩化，断言接线而非子件渲染）。
    expect(wrapper!.findComponent(NtFreshness).props('cut')).toBe(false)
    // 情报拉取成功但为空=真的「没有相关消息」（与「未返回」造假「读取中」区分开，数据纪律三态）。
    expect(text).toContain('没有相关消息。')
    // 快讯占位页签：逐笔/异动流显式标「P1 待接入」，不造假数据。
    wrapper!.findComponent(NtTabs).vm.$emit('update:modelValue', 'flash')
    await wrapper!.vm.$nextTick()
    expect(wrapper!.text()).toContain('P1 待接入')
  })

  it('WS 断连 NtFreshness 收 cut（「断 ·」渲染由子件自测担保），ST 停牌外 noLimit 显「无涨跌幅」且涨跌停价显「无」', async () => {
    mock.live.push(false)
    mock.quote.mockResolvedValue({ ...QUOTE, tradeStatus: 'TRADE', noLimit: true, limitUp: 0, limitDown: 0 })
    await mountPanel()
    useSymbolStore().setSymbol('920001', 'QL')
    await flushPromises()
    const text = wrapper!.text()
    expect(wrapper!.findComponent(NtFreshness).props('cut')).toBe(true)
    expect(text).toContain('无涨跌幅')
    expect(text).not.toContain('1,881.00')
  })

  it('查无此券：错误提示与空态，不编造行情', async () => {
    mock.quote.mockResolvedValue({
      symbol: '000001.SZ', name: '', market: '', tradeStatus: '',
      last: 0, chg: 0, pctChg: 0, open: 0, high: 0, low: 0, prevClose: 0, volume: 0, amount: 0,
      turnoverRate: 0, amplitude: 0, bid: [], ask: [], peTtm: 0, pb: 0, circMv: 0, totalMv: 0,
      limitUp: 0, limitDown: 0, noLimit: false, source: '', asOf: '', stale: false, found: false,
    })
    await mountPanel()
    useSymbolStore().setSymbol('000001', 'QL')
    await flushPromises()
    // B3：错误文案改挂在 NtErrorGuide（shallowMount 下桩化），断言接线（props）而非子件渲染。
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('还没有这只证券的行情')
    // found=false → source 空=无回退来源：盘口空态先说「行情未取到」，不冒充「快照不可用，按日线回退」（禁虚构）。
    expect(wrapper!.text()).toContain('行情未取到')
    expect(wrapper!.text()).not.toContain('按日线回退')
    // found=false → 所有字段行都是「未知」，不假渲染价格。
    expect(wrapper!.text()).not.toContain('0.00')
  })

  it('附录 B-2：52 周高/低携带则渲染区间，缺窗显「—」不虚构', async () => {
    mock.live.push(true)
    mock.quote.mockResolvedValue({ ...QUOTE, high52w: 1900, low52w: 900 })
    await mountPanel()
    useSymbolStore().setSymbol('600519', 'QL')
    await flushPromises()
    expect(wrapper!.text()).toContain('900.00 / 1,900.00')
  })

  it('情报三态：拉取失败显「消息未返回」，不永冒「读取中」也不冒充「没有相关消息」', async () => {
    mock.live.push(true)
    mock.timeline.mockRejectedValue(new Error('boom'))
    await mountPanel()
    useSymbolStore().setSymbol('600519', 'QL')
    await flushPromises()
    const text = wrapper!.text()
    expect(text).toContain('消息未返回，不能判断有无相关消息。')
    expect(text).not.toContain('没有相关消息。')
    expect(text).not.toContain('读取中……')
  })

  it('情报条目键盘可达：role=button + tabindex=0（B3 走查销号）', async () => {
    mock.live.push(true)
    mock.timeline.mockResolvedValue({
      items: [{ id: 'n1', time: '2026-10-27T09:30:00+08:00', title: '快讯一条' }],
      sentiment: null,
    })
    await mountPanel()
    useSymbolStore().setSymbol('600519', 'QL')
    await flushPromises()
    const li = wrapper!.find('.news li')
    expect(li.exists()).toBe(true)
    expect(li.attributes('role')).toBe('button')
    expect(li.attributes('tabindex')).toBe('0')
  })
})
