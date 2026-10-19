import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TradeView from '../TradeView.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable from '@/ui/NtTable.vue'

const mock = vi.hoisted(() => ({
  account: vi.fn(), positions: vi.fn(), orders: vi.fn(), fills: vi.fn(), risk: vi.fn(),
  place: vi.fn(), getOrder: vi.fn(), signal: vi.fn(), confirm: vi.fn(), preview: vi.fn(), channel: vi.fn(),
  accountStore: { book: 'SIM', cash: 100_000, refresh: vi.fn() },
  params: null as null | ((params: Record<string, unknown>) => void),
  push: null as null | ((message: { topic: string; data?: unknown }) => void | Promise<void>),
}))
vi.mock('@/api/trade', () => ({
  getAccount: mock.account, listPositions: mock.positions, listOrders: mock.orders, listFills: mock.fills,
  placeOrder: mock.place, getOrder: mock.getOrder, cancelOrder: vi.fn(), getChannelStatus: mock.channel,
}))
vi.mock('@/api/risk', () => ({ getRiskState: mock.risk, previewOrder: mock.preview }))
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
  // G7 干跑试算默认放行；各用例按需要覆盖为拦截/缩量/不可用。
  mock.preview.mockResolvedValue({ approved: true, blockRule: '', reason: '', volume: 100, adjusted: false, amount: 1000, fee: 5.05, cashDelta: -1005.05, hits: [] })
  mock.signal.mockResolvedValue({ id: '42', strategyVersionId: '9', book: 'paper', symbol: '600519.SH', side: 'buy', status: 'approved', validUntil: signalParams.validUntil, entryLow: 9.9, entryHigh: 10.1 })
  mock.place.mockImplementation(async (draft: { clientOrderId: string }) => ({ id: draft.clientOrderId, status: 'submitted', reason: '', ...draft }))
  mock.confirm.mockResolvedValue('ok')
  // G7 通道状态默认正常；各用例按需要覆盖为降级/读取失败。
  mock.channel.mockResolvedValue([{ channel: 'sim', status: 'connected', since: '2026-10-14T09:30:00+08:00' }])
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

  it('风控试算拦截时禁提交，摘要中标出拦截规则', async () => {
    mock.preview.mockResolvedValue({ approved: false, blockRule: 'R04', reason: '单票仓位超限', volume: 0, adjusted: false, amount: 0, fee: 0, cashDelta: 0, hits: [{ code: 'R04', name: '单票仓位', pass: false, detail: '单票仓位超限' }] })
    await mountView()
    await prepareSignal()
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.text()).toContain('风控试算未放行（R04）')
    expect(wrapper!.text()).toContain('单票仓位超限')
    const text = wrapper!.text()
    expect(text).toContain('风控试算拦截 R04')
    expect(text).toContain('未过规则')
    // 试算拦截留在面板状态里，提交按钮持续禁用。
    const submitButton = wrapper!.findAllComponents({ name: 'NtButton' }).find((button) => button.text().includes('提交委托'))
    expect(submitButton!.props('disabled')).toBe(true)
  })

  it('试算接口失败按未放行处理，不落单', async () => {
    mock.preview.mockRejectedValue(new Error('risk offline'))
    await mountView()
    await prepareSignal()
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.text()).toContain('风控试算未返回，暂不能提交')
  })

  it('缩量放行在摘要中可见，委托按原口径风控缩量提交', async () => {
    mock.preview.mockResolvedValue({ approved: true, blockRule: '', reason: '', volume: 400, adjusted: true, amount: 4000, fee: 5.05, cashDelta: -4005.05, hits: [] })
    await mountView()
    await prepareSignal()
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    expect(mock.place).toHaveBeenCalledWith(expect.objectContaining({ volume: 100 }))
    expect(wrapper!.text()).toContain('缩量放行 400 股')
  })

  it('表单停笔后自动预演一次，表单再改动丢弃过期响应', async () => {
    await mountView()
    await prepareSignal()
    expect(mock.preview).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(mock.preview).toHaveBeenCalledTimes(1)
    expect(mock.preview.mock.calls[0]![0]).toMatchObject({ account_type: 'SIM', symbol: '600519.SH', side: 'buy', price: 10, volume: 100, source: 'manual' })
    expect(String(mock.preview.mock.calls[0]![0].client_order_id)).toMatch(/^preview-/)
    expect(wrapper!.text()).toContain('风控试算可下')
    expect(wrapper!.text()).toContain('现金影响')
    // 停笔期内连续改动只保留最后一次预演。
    wrapper!.findAllComponents(NtInput)[2]!.vm.$emit('update:modelValue', '200')
    await vi.advanceTimersByTimeAsync(100)
    wrapper!.findAllComponents(NtInput)[2]!.vm.$emit('update:modelValue', '300')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(mock.preview).toHaveBeenCalledTimes(2)
    expect(mock.preview.mock.calls[1]![0]).toMatchObject({ volume: 300 })
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

  it('模拟通道徽标呈现服务端状态', async () => {
    await mountView()
    const pill = wrapper!.find('.channel-pill')
    expect(pill.text()).toBe('模拟通道：正常')
    expect(pill.classes()).toContain('ok')
  })

  it('模拟通道降级时徽标警示（信息性，不接管提交闸门）', async () => {
    mock.channel.mockResolvedValue([{ channel: 'sim', status: 'degraded', since: '2026-10-14T09:59:00+08:00' }])
    await mountView()
    const pill = wrapper!.find('.channel-pill')
    expect(pill.text()).toBe('模拟通道：降级')
    expect(pill.classes()).toContain('warn')
    // tooltip 本地化（Asia/Shanghai）且不直显 RFC3339 原文；文案不虚报「恢复中」。
    const title = pill.attributes('title') || ''
    expect(title).toContain('未判定断开')
    expect(title).toContain('2026-10-14 09:59:00')
    expect(title).not.toContain('T09:59:00+08:00')
  })

  it('模拟通道 idle 徽标未连接（显式分支，不落未知）', async () => {
    mock.channel.mockResolvedValue([{ channel: 'sim', status: 'idle', since: '2026-10-14T09:30:00+08:00' }])
    await mountView()
    const pill = wrapper!.find('.channel-pill')
    expect(pill.text()).toBe('模拟通道：未连接')
    expect(pill.classes()).toContain('dim')
  })

  it('未识别的通道状态显示状态未知，不误报未连接', async () => {
    mock.channel.mockResolvedValue([{ channel: 'sim', status: 'melted', since: '2026-10-14T09:30:00+08:00' }])
    await mountView()
    const pill = wrapper!.find('.channel-pill')
    expect(pill.text()).toBe('模拟通道：状态未知')
    expect(pill.classes()).toContain('unknown')
  })

  it('通道清单为空显示状态未知', async () => {
    mock.channel.mockResolvedValue([])
    await mountView()
    const pill = wrapper!.find('.channel-pill')
    expect(pill.text()).toBe('模拟通道：状态未知')
    expect(pill.classes()).toContain('unknown')
  })

  it('通道状态读取失败显示状态未知，不沿用旧值虚报', async () => {
    mock.channel.mockRejectedValue(new Error('boom'))
    await mountView()
    const pill = wrapper!.find('.channel-pill')
    expect(pill.text()).toBe('模拟通道：状态未知')
    expect(pill.classes()).toContain('unknown')
  })

  it('先成功后失败两拍下徽标回落状态未知，不沿用上一拍的正常', async () => {
    await mountView()
    expect(wrapper!.find('.channel-pill').text()).toBe('模拟通道：正常')
    mock.channel.mockRejectedValue(new Error('boom'))
    await vi.advanceTimersByTimeAsync(10_000)
    await flushPromises()
    const pill = wrapper!.find('.channel-pill')
    expect(pill.text()).toBe('模拟通道：状态未知')
    expect(pill.classes()).toContain('unknown')
  })
})
