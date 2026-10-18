import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TradeView from '../TradeView.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable from '@/ui/NtTable.vue'

const mock = vi.hoisted(() => ({
  account: vi.fn(), positions: vi.fn(), orders: vi.fn(), fills: vi.fn(), risk: vi.fn(),
  place: vi.fn(), getOrder: vi.fn(), signal: vi.fn(), confirm: vi.fn(),
  accountStore: { book: 'SIM', cash: 100_000, refresh: vi.fn() },
  params: null as null | ((params: Record<string, unknown>) => void),
  push: null as null | ((message: { topic: string; data?: unknown }) => void | Promise<void>),
}))
vi.mock('@/api/trade', () => ({
  getAccount: mock.account, listPositions: mock.positions, listOrders: mock.orders, listFills: mock.fills,
  placeOrder: mock.place, getOrder: mock.getOrder, cancelOrder: vi.fn(),
}))
vi.mock('@/api/risk', () => ({ getRiskState: mock.risk }))
vi.mock('@/api/signals', () => ({ getSignal: mock.signal }))
vi.mock('@/store/confirm', () => ({ askConfirm: mock.confirm }))
vi.mock('@/stores/account', () => ({ useAccountStore: () => mock.accountStore }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isViewer: () => false, operatorName: 'tester' }) }))
vi.mock('@/stores/risk', () => ({ useRiskStore: () => ({ buysToday: 0 }) }))
vi.mock('@/router', () => ({ router: { currentRoute: { value: { query: {} } } } }))
vi.mock('@/workspace/inject', () => ({ getWorkbench: () => null }))
vi.mock('@/workspace/panel-bus', () => ({
  consumePanelParams: () => undefined,
  onPanelParams: (_id: string, handler: typeof mock.params) => { mock.params = handler; return vi.fn() },
}))
vi.mock('@/ws/hub', () => ({ onPush: (handler: typeof mock.push) => { mock.push = handler; return vi.fn() }, onResync: () => vi.fn() }))

let wrapper: VueWrapper | undefined
function tableProps(index: number) {
  return wrapper!.findAllComponents(NtTable)[index]!.props() as { rows: unknown[]; empty: string }
}
const signalParams = { signalId: '42', strategyVersionId: '9', book: 'paper', symbol: '600519.SH', side: 'buy', price: 10, validUntil: '2026-10-14T10:01:00+08:00' }
const position = (symbol: string) => ({ symbol, quantity: 100, available: 100, avgCost: 10, lastPrice: 10 })

async function mountView() {
  wrapper = shallowMount(TradeView, { global: { renderStubDefaultSlot: true } })
  await flushPromises()
  return wrapper
}

async function prepareSignal() {
  mock.params?.(signalParams)
  await wrapper!.vm.$nextTick()
  wrapper!.findAllComponents(NtInput)[2]!.vm.$emit('update:modelValue', '100')
  await wrapper!.vm.$nextTick()
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-14T10:00:00+08:00'))
  vi.resetAllMocks()
  localStorage.clear()
  mock.accountStore.book = 'SIM'
  mock.params = null
  mock.push = null
  mock.account.mockImplementation(async (account: string) => ({ account, cash: 100_000, equity: 100_000, paperDays: 27, openHalted: false, haltReason: '' }))
  mock.positions.mockResolvedValue([])
  mock.orders.mockResolvedValue([])
  mock.fills.mockResolvedValue([])
  mock.risk.mockResolvedValue({ mode: 'L1', killActive: false })
  mock.signal.mockResolvedValue({ id: '42', strategyVersionId: '9', book: 'paper', symbol: '600519.SH', side: 'buy', status: 'approved', validUntil: signalParams.validUntil, entryLow: 9.9, entryHigh: 10.1 })
  mock.place.mockImplementation(async (draft: { clientOrderId: string }) => ({ id: draft.clientOrderId, status: 'submitted', reason: '', ...draft }))
  mock.confirm.mockResolvedValue('ok')
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.useRealTimers()
})

describe('trade view contracts', () => {
  it('已知风控引擎或配置未就绪时不提交，旧累计27天不开放LIVE', async () => {
    mock.risk.mockResolvedValue({ mode: 'L1', killActive: false, engineReady: false, configReady: true })
    await mountView()
    await prepareSignal()
    expect(wrapper!.text()).toContain('风控引擎或配置尚未就绪')
    await wrapper!.find('form').trigger('submit'); await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    const options = wrapper!.findAllComponents(NtSelect)[0]!.props('options') as { value: string; disabled?: boolean }[]
    expect(options.find((o) => o.value === 'LIVE')?.disabled).toBe(true)
  })
  it('数值参考价落表，提交保留信号/策略身份，且提交前重取信号', async () => {
    await mountView()
    await prepareSignal()
    expect(wrapper!.findAllComponents(NtInput)[1]!.props('modelValue')).toBe('10')
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.signal).toHaveBeenCalledWith('42')
    expect(mock.place).toHaveBeenCalledWith(expect.objectContaining({ signalId: '42', strategyVersion: '9', account: 'SIM', symbol: '600519.SH', price: 10, volume: 100 }))
    // 已报但信号尚未回写、列表尚未返回时，也不为同一票生成第二个ID。
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect(wrapper!.text()).toContain('此信号已有已核实委托')
  })

  it('窗口内过期或信号已执行，不能把旧票再提交', async () => {
    await mountView()
    await prepareSignal()
    mock.signal.mockResolvedValueOnce({ ...await mock.signal(), status: 'executing' })
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.text()).toContain('不再处于可确认状态')
    vi.setSystemTime(new Date('2026-10-14T10:01:00+08:00'))
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.text()).toContain('信号已过期')
  })

  it('切换证券清空旧价格与数量并解除信号关联', async () => {
    await mountView()
    await prepareSignal()
    wrapper!.findAllComponents(NtInput)[0]!.vm.$emit('update:modelValue', '000001.SZ')
    await wrapper!.vm.$nextTick()
    expect(wrapper!.findAllComponents(NtInput)[1]!.props('modelValue')).toBe('')
    expect(wrapper!.findAllComponents(NtInput)[2]!.props('modelValue')).toBe('')
    expect(wrapper!.text()).toContain('解除原信号关联')
  })

  it('未知响应锁住重复提交，只核对原编号，不生成新委托', async () => {
    await mountView()
    await prepareSignal()
    mock.place.mockRejectedValueOnce(new Error('network timeout'))
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect(wrapper!.text()).toContain('委托结果待核对')
    const draft = mock.place.mock.calls[0]![0] as { clientOrderId: string }
    mock.getOrder.mockResolvedValueOnce({ id: draft.clientOrderId, status: 'filled' })
    const lookup = wrapper!.findAllComponents({ name: 'NtButton' }).find((button) => button.text().includes('核对原委托结果'))
    expect(lookup).toBeDefined()
    lookup!.vm.$emit('click')
    await flushPromises()
    expect(mock.getOrder).toHaveBeenCalledWith(draft.clientOrderId)
    expect(mock.place).toHaveBeenCalledTimes(1)
  })

  it('position推送与兜底轮询更新持仓，卸载后停止读取', async () => {
    await mountView()
    mock.positions.mockResolvedValue([position('600519.SH')])
    await mock.push?.({ topic: 'position' })
    await flushPromises()
    expect(tableProps(0).rows).toHaveLength(1)
    const before = mock.positions.mock.calls.length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(mock.positions.mock.calls.length).toBeGreaterThan(before)
    wrapper!.unmount()
    wrapper = undefined
    const after = mock.positions.mock.calls.length
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mock.positions).toHaveBeenCalledTimes(after)
  })

  it('旧账户慢响应不能覆盖新账户，接口失败不显示成空仓', async () => {
    let release: ((value: unknown[]) => void) | undefined
    mock.positions.mockImplementationOnce(() => new Promise((resolve) => { release = resolve }))
    await mountView()
    mock.positions.mockResolvedValue([position('000001.SZ')])
    wrapper!.findAllComponents(NtSelect)[0]!.vm.$emit('update:modelValue', 'LIVE')
    await wrapper!.vm.$nextTick()
    await flushPromises()
    release?.([position('600519.SH')])
    await flushPromises()
    expect(tableProps(0).rows).toEqual([expect.objectContaining({ symbol: '000001.SZ' })])
    mock.positions.mockRejectedValueOnce(new Error('offline'))
    await expect(mock.push?.({ topic: 'resume' })).rejects.toThrow('账户或风控状态尚未恢复')
    await flushPromises()
    expect(tableProps(0).empty).toContain('不能当作空仓')
  })

  it('未知委托在关闭重开面板后仍按原ID阻止重复提交', async () => {
    await mountView()
    await prepareSignal()
    mock.place.mockRejectedValueOnce(new Error('timeout'))
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    const originalId = mock.place.mock.calls[0]![0].clientOrderId
    wrapper!.unmount()
    await mountView()
    await prepareSignal()
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem('nt.trade.pending.v1.tester')).toContain(originalId)
    expect(wrapper!.text()).toContain('上一笔委托结果待核对')
  })

  it('关闭面板作废尚未发送的预检，不能在异步返回后继续下单', async () => {
    await mountView()
    await prepareSignal()
    let release: ((value: Record<string, unknown>) => void) | undefined
    mock.signal.mockImplementationOnce(() => new Promise((resolve) => { release = resolve }))
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    wrapper!.unmount()
    wrapper = undefined
    release?.({ status: 'approved' })
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(localStorage.length).toBe(0)
  })

  it('信号只读检查失败不会清除另一面板的待核对委托', async () => {
    await mountView()
    await prepareSignal()
    const foreign = { clientOrderId: 'another-panel', account: 'SIM', symbol: '000001.SZ', side: 'buy', price: 10, volume: 100, source: 'manual', operator: 'tester' }
    mock.signal.mockImplementationOnce(async () => {
      localStorage.setItem('nt.trade.pending.v1.tester', JSON.stringify(foreign))
      throw Object.assign(new Error('signal missing'), { status: 404 })
    })
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(JSON.parse(localStorage.getItem('nt.trade.pending.v1.tester')!)).toEqual(foreign)
  })

  it('已核实的委托响应只清除自己的记录', async () => {
    await mountView()
    await prepareSignal()
    mock.place.mockImplementationOnce(async (draft: { clientOrderId: string }) => {
      localStorage.setItem('nt.trade.pending.v1.tester', JSON.stringify({ ...draft, clientOrderId: 'another-panel' }))
      return { id: draft.clientOrderId, status: 'submitted' }
    })
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(localStorage.getItem('nt.trade.pending.v1.tester')).toContain('another-panel')
    expect(wrapper!.text()).toContain('上一笔委托结果待核对')
  })

  it.each([
    { status: 'submitted', filled: 0 },
    { status: 'cancelled', filled: 50 },
    { status: 'filled', filled: 100 },
  ])('重开后同信号已有$status委托时不重复执行', async (state) => {
    mock.orders.mockResolvedValue([{ id: 'original', account: 'SIM', signalId: '42', strategyVersion: '9', symbol: '600519.SH', side: 'buy', ...state }])
    await mountView()
    await prepareSignal()
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.text()).toContain('此信号已有关联委托 original')
  })
})
