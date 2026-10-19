/**
 * 下单条（P0，视频设计指引 §8 底条恒随）契约：
 * 停笔预览 + 落单前全新试算两段门闩；LIVE 账本恒禁；回报必带原因；网络异常显「待核」指去交易面板。
 * 速记：Vitest 假定时器推进 250ms 停笔防抖；NtInput/NtTabs 走 stub 的 update:modelValue 派发。
 */
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import TradeBar from '../trade/trade-bar.panel.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTabs from '@/ui/NtTabs.vue'
import { useSymbolStore } from '@/stores/symbol'

const mock = vi.hoisted(() => ({
  preview: vi.fn(),
  confirm: vi.fn(),
  place: vi.fn(),
  notify: vi.fn(),
  toast: { success: vi.fn(), warn: vi.fn(), info: vi.fn(), error: vi.fn() },
  live: true,
  accountStore: {
    book: 'SIM', cash: 100_000, equity: 100_000, openHalted: false, haltReason: '',
    positionsOk: true, positions: [] as unknown[], refresh: vi.fn(),
  },
  riskStore: { ready: true, killActive: false, refresh: vi.fn() },
  tradeStore: {
    orders: [] as { id: string; account: string; symbol: string; side: string; status: string; price: number | null; volume: number | null; filled: number | null; reason: string }[],
    fills: [] as unknown[],
    loading: false, ready: true,
    refresh: vi.fn(), cancel: vi.fn(), confirm: vi.fn(),
  },
}))
vi.mock('@/api/risk', () => ({ previewOrder: mock.preview }))
vi.mock('@/store/confirm', () => ({ askConfirm: mock.confirm }))
vi.mock('@/utils/notify', () => ({ deskNotify: mock.notify }))
vi.mock('@/ui/useToast', () => ({ useToast: () => mock.toast }))
vi.mock('@/ws/hub', () => ({ isLive: () => mock.live }))
vi.mock('@/workspace/inject', () => ({ getWorkbench: () => null }))
vi.mock('@/stores/account', () => ({ useAccountStore: () => mock.accountStore }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isViewer: () => false, operatorName: 'tester' }) }))
vi.mock('@/stores/risk', () => ({ useRiskStore: () => mock.riskStore }))
vi.mock('@/stores/trade', () => ({ useTradeStore: () => ({ ...mock.tradeStore, place: mock.place }) }))

const PREVIEW_OK = { approved: true, blockRule: '', reason: '', volume: 100, adjusted: false, amount: 11_000, fee: 5.5, cashDelta: -11_005.5, hits: [] }

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = shallowMount(TradeBar, { global: { renderStubDefaultSlot: true } })
  await flushPromises()
  return wrapper
}

async function setInput(index: number, value: string) {
  wrapper!.findAllComponents(NtInput)[index]!.vm.$emit('update:modelValue', value)
  await wrapper!.vm.$nextTick()
}

/** 填满合法表单：600519.SH / 110 / 100 → 推进停笔防抖出一次预览。 */
async function fillForm() {
  await setInput(0, '600519.SH')
  await setInput(1, '110')
  await setInput(2, '100')
  await vi.advanceTimersByTimeAsync(300)
  await flushPromises()
}

async function submitForm() {
  await wrapper!.find('form').trigger('submit')
  await flushPromises()
}

function buttonWith(text: string) {
  return wrapper!.findAllComponents({ name: 'NtButton' }).find((b) => b.text() === text)
}

let uuidSeq = 0

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
  vi.resetAllMocks()
  // jsdom 版本保底：面板用 crypto.randomUUID 生成两段编号，缺了就用可控替身。
  if (typeof globalThis.crypto?.randomUUID !== 'function') {
    vi.stubGlobal('crypto', { randomUUID: () => `uuid-test-${++uuidSeq}` })
  }
  mock.live = true
  mock.accountStore.book = 'SIM'
  mock.accountStore.openHalted = false
  mock.tradeStore.orders = []
  mock.tradeStore.fills = []
  mock.tradeStore.ready = true
  mock.preview.mockResolvedValue({ ...PREVIEW_OK })
  mock.place.mockResolvedValue({ id: 'manual-1', status: 'submitted' })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('trade bar（视频落地 P0）', () => {
  it('试算放行 → 提交受理：place 以 manual- 编号落 SIM 委托，落单前试算沿用同一草稿编号', async () => {
    await mountPanel()
    useSymbolStore().setSymbol('600519', 'GP')
    await flushPromises()
    await fillForm()
    expect(mock.preview).toHaveBeenCalled()
    expect(wrapper!.text()).toContain('试算放行')
    await submitForm()
    expect(mock.place).toHaveBeenCalledTimes(1)
    const draft = mock.place.mock.calls[0]![0] as { clientOrderId: string; account: string; symbol: string; side: string; price: number; volume: number; source: string; operator: string }
    expect(draft.clientOrderId.startsWith('manual-')).toBe(true)
    expect(draft).toMatchObject({ account: 'SIM', symbol: '600519.SH', side: 'buy', price: 110, volume: 100, source: 'manual', operator: 'tester' })
    // 提交前那次试算必须使用与草稿一致的编号（不落单不留痕，但编号要可对账）。
    const lastPreview = mock.preview.mock.calls.at(-1)![0] as { client_order_id: string }
    expect(lastPreview.client_order_id).toBe(draft.clientOrderId)
    expect(mock.toast.success).toHaveBeenCalled()
    expect(mock.notify).toHaveBeenCalledWith('模拟委托已受理', expect.stringContaining('600519.SH'), 'success')
    // 受理后切到「当前委托」页签，不留在表单区假积累。
    expect(wrapper!.findComponent(NtTabs).props('modelValue')).toBe('open')
  })

  it('试算拦截 → 不落单，错误区带规则编号与原因', async () => {
    mock.preview.mockResolvedValue({ approved: false, blockRule: 'R12', reason: '单票仓位超限', volume: 100, adjusted: false, amount: 11_000, fee: 5.5, cashDelta: -11_005.5, hits: [] })
    await mountPanel()
    await fillForm()
    expect(wrapper!.text()).toContain('试算拦截 R12')
    // 停笔预览已拦截时提交按钮保持禁用。
    expect(buttonWith('提交委托')!.props('disabled')).toBe(true)
    await submitForm()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.text()).toContain('风控试算未放行（R12）')
    expect(wrapper!.text()).toContain('单票仓位超限')
  })

  it('试算接口不可用 → 预览区明示，落单前重试也算未返回，都不落单', async () => {
    mock.preview.mockRejectedValue(Object.assign(new Error('connection refused'), { status: 0 }))
    await mountPanel()
    await fillForm()
    expect(wrapper!.text()).toContain('试算不可用')
    await submitForm()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.text()).toContain('风控试算未返回，暂不能提交')
  })

  it('落单网络层异常 → 错误显「待核」指去交易面板，桌面通知带原编号', async () => {
    mock.place.mockRejectedValue(Object.assign(new Error('admin 不可达'), { status: 0 }))
    await mountPanel()
    await fillForm()
    await submitForm()
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect(wrapper!.text()).toContain('委托结果未知（网络异常）')
    expect(wrapper!.text()).toContain('交易（JY）面板核对')
    expect(mock.notify).toHaveBeenCalledWith('委托结果待核', expect.stringContaining('manual-'), 'warn')
  })

  it('LIVE 账本：授权徽标转红且提交按钮恒禁，走不到确认与落单', async () => {
    mock.accountStore.book = 'LIVE'
    await mountPanel()
    expect(wrapper!.text()).toContain('LIVE · 未授权')
    await fillForm()
    expect(buttonWith('提交委托')!.props('disabled')).toBe(true)
    expect(mock.place).not.toHaveBeenCalled()
    expect(mock.confirm).not.toHaveBeenCalled()
  })

  it('表单代码随 store 预填且持续跟随；当前委托页签只列未终态，可确认可撤', async () => {
    mock.tradeStore.orders = [
      { id: 'manual-x1', account: 'SIM', symbol: '600519.SH', side: 'buy', status: 'submitted', price: 110, volume: 100, filled: 0, reason: '' },
      { id: 'manual-x2', account: 'SIM', symbol: '000001.SZ', side: 'sell', status: 'pending_confirm', price: 12, volume: 200, filled: 0, reason: '' },
      { id: 'manual-x3', account: 'SIM', symbol: '300750.SZ', side: 'buy', status: 'filled', price: 180, volume: 100, filled: 100, reason: '' },
    ]
    await mountPanel()
    const store = useSymbolStore()
    store.setSymbol('600519', 'GP')
    await flushPromises()
    expect(wrapper!.findAllComponents(NtInput)[0]!.props('modelValue')).toBe('600519.SH')
    // 未手输过 → 继续跟随下一次切换；手输价/量不算代码编辑。
    store.setSymbol('000001', 'QB')
    await flushPromises()
    expect(wrapper!.findAllComponents(NtInput)[0]!.props('modelValue')).toBe('000001.SZ')
    // 切到当前委托：filled 那笔不出现。
    wrapper!.findComponent(NtTabs).vm.$emit('update:modelValue', 'open')
    await wrapper!.vm.$nextTick()
    const text = wrapper!.text()
    expect(text).toContain('manual-x1')
    expect(text).toContain('manual-x2')
    expect(text).not.toContain('manual-x3')
    expect(buttonWith('确认')).toBeDefined()
    expect(buttonWith('撤销')).toBeDefined()
    buttonWith('撤销')!.vm.$emit('click')
    await flushPromises()
    expect(mock.tradeStore.cancel).toHaveBeenCalledWith('SIM', 'manual-x1')
  }, 15_000)
})
