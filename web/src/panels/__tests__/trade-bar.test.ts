/**
 * 下单条（P0，视频设计指引 §8 底条恒随）契约：
 * 停笔预览 + 落单前全新试算两段门闩；回报必带原因；网络异常显「待核」指去交易面板。
 * FE-T4：LIVE 可选性走 admission 信封总闸（fail-closed），不再前端硬编码恒禁；
 * FE-T1：确认顺序=落单前全新试算→确认（载荷带试算摘要与幂等键）→提交；
 * FE-T3：submitBlock 四闸对齐 TradeView（风控读到/账本读到/新鲜度 30s/账本同源）。
 * W5c：F9/F10 面板热键（window keydown 派发 KeyboardEvent）/ R 倍数联动 / 反手两步显式 /
 * 止盈止损告警联动（createAlertRule mock 到门面，联动失败不阻塞主单）。
 * 速记：Vitest 假定时器推进 250ms 停笔防抖；NtInput/NtTabs 走 stub 的 update:modelValue 派发。
 */
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import TradeBar from '../trade/trade-bar.panel.vue'
import NtBlockerCard from '@/display/NtBlockerCard.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTabs from '@/ui/NtTabs.vue'
import { useSymbolStore } from '@/stores/symbol'
import { stagePanelParams } from '@/workspace/panel-bus'

const mock = vi.hoisted(() => ({
  preview: vi.fn(),
  confirm: vi.fn(),
  place: vi.fn(),
  notify: vi.fn(),
  readiness: vi.fn(),
  workbench: vi.fn(),
  createRule: vi.fn(),
  toast: { success: vi.fn(), warn: vi.fn(), info: vi.fn(), error: vi.fn() },
  live: true,
  viewer: false,
  /* FE-T3：submitBlock 新鲜度/一致性闸读 accountOk/checkedAt/snapshot 与 risk.checkedAt——
     beforeEach 统一给「刚读到且同源」基线，单闸用例各自覆写。 */
  accountStore: {
    book: 'SIM', cash: 100_000, equity: 100_000, openHalted: false, haltReason: '',
    accountOk: true, positionsOk: true, checkedAt: 0, snapshot: null as { account: string } | null,
    positions: [] as unknown[], refresh: vi.fn(),
  },
  riskStore: { ready: true, killActive: false, checkedAt: 0, refresh: vi.fn() },
  admissionStore: { liveEnabled: false, ensureFresh: vi.fn(), startPolling: vi.fn() },
  tradeStore: {
    orders: [] as { id: string; account: string; symbol: string; side: string; status: string; price: number | null; volume: number | null; filled: number | null; reason: string }[],
    fills: [] as unknown[],
    loading: false, ready: true,
    refresh: vi.fn(), cancel: vi.fn(), confirm: vi.fn(),
  },
}))
vi.mock('@/api/risk', () => ({ previewOrder: mock.preview }))
vi.mock('@/api/ops', () => ({ getOpsReadiness: mock.readiness }))
vi.mock('@/api/alert-rules', () => ({ createAlertRule: mock.createRule }))
vi.mock('@/stores/confirm', () => ({ askConfirm: mock.confirm }))
vi.mock('@/utils/notify', () => ({ deskNotify: mock.notify }))
vi.mock('@/ui/useToast', () => ({ useToast: () => mock.toast }))
vi.mock('@/ws/hub', () => ({ isLive: () => mock.live }))
vi.mock('@/workspace/inject', () => ({ getWorkbench: mock.workbench }))
vi.mock('@/stores/account', () => ({ useAccountStore: () => mock.accountStore }))
vi.mock('@/stores/admission', () => ({ useAdmissionStore: () => mock.admissionStore }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isViewer: () => mock.viewer, operatorName: 'tester' }) }))
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
  // W3-2 偏好持久化：用例间清档，防前一用例的页签存档影响挂载初值（断言本身不改）。
  localStorage.clear()
  // jsdom 版本保底：面板用 crypto.randomUUID 生成两段编号，缺了就用可控替身。
  if (typeof globalThis.crypto?.randomUUID !== 'function') {
    vi.stubGlobal('crypto', { randomUUID: () => `uuid-test-${++uuidSeq}` })
  }
  mock.live = true
  mock.viewer = false
  /* FE-T3 闸门用例在挂载后改写 store 读数——plain 对象进不了 computed 依赖，submitBlock 会吃缓存。
     每用例重建 reactive 版让闸门随覆写重估（FE-T3 基线：假时钟下 serverNow≈Date.now，「刚读到且同源」）。 */
  mock.accountStore = reactive({
    book: 'SIM', cash: 100_000, equity: 100_000, openHalted: false, haltReason: '',
    accountOk: true, positionsOk: true, checkedAt: Date.now(), snapshot: { account: 'SIM' } as { account: string } | null,
    positions: [] as unknown[], refresh: vi.fn(),
  })
  mock.riskStore = reactive({ ready: true, killActive: false, checkedAt: Date.now(), refresh: vi.fn() })
  // FE-T4：LIVE 可选性走 admission 信封——默认未授权（单测各自翻闸验放开路径）。
  mock.admissionStore = reactive({ liveEnabled: false, ensureFresh: vi.fn(), startPolling: vi.fn() })
  mock.tradeStore.orders = []
  mock.tradeStore.fills = []
  mock.tradeStore.ready = true
  mock.preview.mockResolvedValue({ ...PREVIEW_OK })
  // FE-T1 确认弹层默认放行（LIVE 链路用例覆写拒绝/超时路径）。
  mock.confirm.mockResolvedValue('ok')
  mock.place.mockResolvedValue({ id: 'manual-1', status: 'submitted' })
  mock.createRule.mockResolvedValue([])
  // W3b.b 拦截位：默认全就绪不现身卡；单测各自覆写未就绪场景。
  mock.readiness.mockResolvedValue({ asOf: '', ready: true, gates: [], blockers: [] })
  mock.workbench.mockReturnValue(null)
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
    // B3：提交错误改挂 NtErrorGuide（shallowMount 下桩化），断言接线（props）而非子件渲染。
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('风控试算未放行（R12）')
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('单票仓位超限')
  })

  it('试算接口不可用 → 预览区明示，落单前重试也算未返回，都不落单', async () => {
    mock.preview.mockRejectedValue(Object.assign(new Error('connection refused'), { status: 0 }))
    await mountPanel()
    await fillForm()
    expect(wrapper!.text()).toContain('试算不可用')
    await submitForm()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('风控试算未返回，暂不能提交')
  })

  it('落单网络层异常 → 错误显「待核」指去交易面板，桌面通知带原编号', async () => {
    mock.place.mockRejectedValue(Object.assign(new Error('admin 不可达'), { status: 0 }))
    await mountPanel()
    await fillForm()
    await submitForm()
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('委托结果未知（网络异常）')
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('交易（JY）面板核对')
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

  it('挂载前 stage 的 XD 参数（P0-2）：consume 预填卖向与裸码，预览体带出 sell + 裸码已归一', async () => {
    stagePanelParams('trade-bar', { side: 'sell', symbol: '000001' })
    await mountPanel()
    // 代码进表即归一（总线进站归一），且回写全局 store 记 source=trade-bar。
    expect(wrapper!.findAllComponents(NtInput)[0]!.props('modelValue')).toBe('000001.SZ')
    expect(useSymbolStore().code).toBe('000001.SZ')
    expect(useSymbolStore().source).toBe('trade-bar')
    await setInput(1, '12')
    await setInput(2, '200')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    const body = mock.preview.mock.calls.at(-1)![0] as { symbol: string; side: string }
    expect(body.symbol).toBe('000001.SZ')
    expect(body.side).toBe('sell')
  })

  it('已挂载时 XD 直达（推送路径）：buy 预填方向、代码落表；非法 side 忽略不动表', async () => {
    await mountPanel()
    stagePanelParams('trade-bar', { side: 'buy', symbol: '300750' })
    await flushPromises()
    expect(wrapper!.findAllComponents(NtInput)[0]!.props('modelValue')).toBe('300750.SZ')
    stagePanelParams('trade-bar', { side: 'hold' })
    await flushPromises()
    await setInput(1, '180')
    await setInput(2, '100')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    const body = mock.preview.mock.calls.at(-1)![0] as { symbol: string; side: string }
    expect(body.symbol).toBe('300750.SZ')
    expect(body.side).toBe('buy')
  })

  it('W3b.b：执行链闸门未就绪 → 下单页显「下单前置检查」卡（扫描链闸门不外溢），去处理跳到风控链面板', async () => {
    const openPanel = vi.fn()
    mock.workbench.mockReturnValue({ openPanel })
    mock.readiness.mockResolvedValue({
      asOf: '2026-10-27T09:00:00Z',
      ready: false,
      gates: [],
      blockers: [
        { gate: 'risk_killswitch', state: 'warn', severity: 'danger', why: '急停未复位', deeplink: '/risk#killswitch' },
        { gate: 'llm_config', state: 'warn', why: '模型未配', deeplink: '/config#models' },
      ],
    })
    await mountPanel()
    const card = wrapper!.findComponent(NtBlockerCard)
    expect(card.exists()).toBe(true)
    expect(card.props('title')).toBe('下单前置检查')
    // 股池闸门 llm_config 属扫描链，被 TRADE_GATES 过滤，不外溢到下单条。
    const gates = (card.props('blockers') as { gate: string }[]).map((b) => b.gate)
    expect(gates).toEqual(['risk_killswitch'])
    card.vm.$emit('navigate', '/risk#killswitch')
    expect(openPanel).toHaveBeenCalledWith('risk-chain')
  })

  it('W3b.b：就绪口不可达 → fail-silent 不现身卡，下单主体照常可用', async () => {
    mock.readiness.mockRejectedValue(new Error('admin 不可达'))
    await mountPanel()
    expect(wrapper!.findComponent(NtBlockerCard).exists()).toBe(false)
    await fillForm()
    expect(mock.preview).toHaveBeenCalled()
  })

  it('FE-T3：账本读数超 30 秒未刷新 → 提交禁用且回车路径同样被拦（闸门对齐 TradeView）', async () => {
    await mountPanel()
    await fillForm()
    mock.accountStore.checkedAt = Date.now() - 31_000
    await wrapper!.vm.$nextTick()
    expect(buttonWith('提交委托')!.props('disabled')).toBe(true)
    await submitForm()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('账本读数超过 30 秒未刷新')
  })

  it('FE-T3：账户响应账本不一致 / 风控未读到 → 不落单（对齐 TradeView 完整闸门口径）', async () => {
    await mountPanel()
    await fillForm()
    mock.accountStore.snapshot = { account: 'LIVE' } // 当前 SIM 账本，响应却回 LIVE
    await wrapper!.vm.$nextTick()
    expect(buttonWith('提交委托')!.props('disabled')).toBe(true)
    await submitForm()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('账户响应与当前账本不一致')
    // 风控读数失效同样拦（刷新 mock 不回填，闸保持拦态）
    mock.accountStore.snapshot = { account: 'SIM' }
    mock.riskStore.ready = false
    await wrapper!.vm.$nextTick()
    expect(buttonWith('提交委托')!.props('disabled')).toBe(true)
    await submitForm()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('风控状态未读到最新结果')
  })

  it('FE-T1/T4：LIVE 信封放开后——试算先于确认，确认载荷带试算摘要与幂等键，确认后才落单', async () => {
    mock.admissionStore.liveEnabled = true
    mock.accountStore.book = 'LIVE'
    mock.accountStore.snapshot = { account: 'LIVE' }
    await mountPanel()
    expect(wrapper!.text()).toContain('实盘LIVE·授权')
    await fillForm()
    await submitForm()
    // 顺序：停笔预览之外，落单前那次全新试算必须先于确认。
    expect(mock.confirm).toHaveBeenCalledTimes(1)
    const lastPreview = mock.preview.mock.invocationCallOrder.at(-1)!
    expect(mock.confirm.mock.invocationCallOrder[0]!).toBeGreaterThan(lastPreview)
    expect(mock.confirm.mock.calls[0]![0]).toMatchObject({
      clientOrderId: expect.stringMatching(/^manual-/),
      previewAmount: 11_000,
      previewFee: 5.5,
      previewCashDelta: -11_005.5,
    })
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect((mock.place.mock.calls[0]![0] as { account: string }).account).toBe('LIVE')
  })
})

/** 派发一个 F9/F10（target=window，非可编辑控件——登记表 isEditableTarget 守卫不拦）。
 *  注意 KeyboardEventInit 没有 target 字段，dispatchEvent 的 target 恒为派发对象（此处 window）。 */
function pressHotkey(key: string) {
  window.dispatchEvent(new KeyboardEvent('keydown', { key }))
}

describe('trade bar W5c（F9/F10 热键 · R 倍数联动 · 反手 · 止盈止损联动）', () => {
  it('F9 热键：表单合法即提交买单（side 切买 + 走完整试算门闩），LIVE 恒禁热键不落单', async () => {
    await mountPanel()
    await fillForm()
    await flushPromises()
    pressHotkey('F9')
    await flushPromises()
    // 走完整提交链：place 落单 + 受理后跳「当前委托」。
    expect(mock.place).toHaveBeenCalledTimes(1)
    const draft = mock.place.mock.calls[0]![0] as { side: string; symbol: string; price: number; volume: number }
    expect(draft).toMatchObject({ side: 'buy', symbol: '600519.SH', price: 110, volume: 100 })
    expect(wrapper!.findComponent(NtTabs).props('modelValue')).toBe('open')
  })

  it('F9 热键在 LIVE 账本恒禁（热键与提交钮同 submitBlock 闸，不落单）', async () => {
    mock.accountStore.book = 'LIVE'
    await mountPanel()
    await fillForm()
    await flushPromises()
    pressHotkey('F9')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
  })

  it('F9/F10 仅下单页生效：受理跳「当前委托」后再按热键不落单（表单不可见不后台提交）', async () => {
    await mountPanel()
    await fillForm()
    await flushPromises()
    pressHotkey('F9')
    await flushPromises()
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect(wrapper!.findComponent(NtTabs).props('modelValue')).toBe('open')
    // 表单状态仍在但页签已切走——热键不得用不可见表单后台落单。
    pressHotkey('F9')
    pressHotkey('F10')
    await flushPromises()
    expect(mock.place).toHaveBeenCalledTimes(1)
  })

  it('F10 热键：方向切卖 + 提交卖单；viewer 角色按 F9/F10 不触发', async () => {
    await mountPanel()
    await fillForm()
    await flushPromises()
    pressHotkey('F10')
    await flushPromises()
    expect(mock.place).toHaveBeenCalledTimes(1)
    const draft = mock.place.mock.calls[0]![0] as { side: string }
    expect(draft.side).toBe('sell')
  })

  it('viewer 角色按 F9/F10 不触发落单（热键与提交钮同闸）', async () => {
    mock.viewer = true
    await mountPanel()
    await fillForm()
    await flushPromises()
    pressHotkey('F9')
    pressHotkey('F10')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
  })

  it('R 倍数：勾选止盈/止损后按百分比联动，R<1 落警告类（赔率倒挂）', async () => {
    await mountPanel()
    await fillForm()
    await flushPromises()
    // R 区仅买单显（模板 v-if side==='buy'，默认 buy）。
    const rrGroup = () => wrapper!.find('.ticket__rr')
    expect(rrGroup().exists()).toBe(true)
    // 勾止盈+止损（组件 v-if 出 NtInput stub，update:modelValue 走 v-model）。
    const tpCheck = wrapper!.findAll('.rr-cell input[type="checkbox"]')[0]!
    const slCheck = wrapper!.findAll('.rr-cell input[type="checkbox"]')[1]!
    await tpCheck.setValue(true)
    await slCheck.setValue(true)
    await wrapper!.vm.$nextTick()
    // 止盈 10%、止损 20% → R=|10/20|=0.5 <1 → warn。
    const tpInput = wrapper!.findAllComponents(NtInput).filter((c) => c.classes().includes('rr-input'))[0]!
    const slInput = wrapper!.findAllComponents(NtInput).filter((c) => c.classes().includes('rr-input'))[1]!
    tpInput.vm.$emit('update:modelValue', '10')
    slInput.vm.$emit('update:modelValue', '20')
    await wrapper!.vm.$nextTick()
    const rrVal = wrapper!.find('.rr-value')
    expect(rrVal.exists()).toBe(true)
    expect(rrVal.text()).toContain('R 0.5')
    expect(rrVal.classes()).toContain('rr-value--warn')
    // 反向：止盈 20%、止损 10% → R=2.0 不警告。
    tpInput.vm.$emit('update:modelValue', '20')
    slInput.vm.$emit('update:modelValue', '10')
    await wrapper!.vm.$nextTick()
    const rrVal2 = wrapper!.find('.rr-value')
    expect(rrVal2.text()).toContain('R 2.0')
    expect(rrVal2.classes()).not.toContain('rr-value--warn')
    // 卖单侧 R 区整组消失（shallowMount 下原生 button 可直接触发）。
    await wrapper!.findAll('.tb-side')[1]!.trigger('click')
    await wrapper!.vm.$nextTick()
    expect(rrGroup().exists()).toBe(false)
  })

  it('反手：持仓票点「反手」先卖全可卖（现价锚优先），受理后表单预填买同价同量', async () => {
    // 持仓：600519.SH 可卖 200 股，现价 118.5。
    mock.accountStore.positions = [{ symbol: '600519.SH', quantity: 200, available: 200, avgCost: 100, lastPrice: 118.5 }]
    await mountPanel()
    await fillForm()
    await flushPromises()
    // 反手钮仅在有可卖持仓时显。
    const reverseBtn = buttonWith('反手')
    expect(reverseBtn).toBeDefined()
    expect(reverseBtn!.props('disabled')).toBe(false)
    reverseBtn!.vm.$emit('click')
    await flushPromises()
    // 第一步：卖单立即提交（reverse-sell- 前缀，现价锚 118.5 优先于表单 110）。
    expect(mock.place).toHaveBeenCalledTimes(1)
    const sellDraft = mock.place.mock.calls[0]![0] as { clientOrderId: string; side: string; price: number; volume: number; symbol: string }
    expect(sellDraft.clientOrderId.startsWith('reverse-sell-')).toBe(true)
    expect(sellDraft).toMatchObject({ side: 'sell', price: 118.5, volume: 200, symbol: '600519.SH' })
    expect(mock.toast.success).toHaveBeenCalledWith('反手卖单已受理', expect.stringContaining('200'))
    // 第二步：表单预填买同价同量，submitHint 引导核对。
    const inputs = wrapper!.findAllComponents(NtInput)
    expect(inputs[0]!.props('modelValue')).toBe('600519.SH')
    expect(inputs[1]!.props('modelValue')).toBe('118.50')
    expect(inputs[2]!.props('modelValue')).toBe('200')
    expect(wrapper!.text()).toContain('反手卖单已受理；买单已预填（同价同量），请核对后提交')
    // 卖单失败 → 不落预填，错误进 submitError。
    mock.place.mockRejectedValueOnce(new Error('柜台拒单'))
    const reverseBtn2 = buttonWith('反手')!
    reverseBtn2.vm.$emit('click')
    await flushPromises()
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('反手卖单未受理')
  })

  it('反手先过风控试算门闩（与 submit 同口径）：试算拦截不落单并显规则原因', async () => {
    mock.accountStore.positions = [{ symbol: '600519.SH', quantity: 200, available: 200, avgCost: 100, lastPrice: 118.5 }]
    mock.preview.mockResolvedValue({ ...PREVIEW_OK, approved: false, blockRule: 'R12', reason: '单票仓位超限' })
    await mountPanel()
    await fillForm()
    await flushPromises()
    const btn = buttonWith('反手')!
    btn.vm.$emit('click')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('反手风控试算未放行（R12）')
  })

  it('反手缺价格证据（无现价锚且未填限价）：不落单并给中文原因，不静默空操作', async () => {
    mock.accountStore.positions = [{ symbol: '600519.SH', quantity: 200, available: 200, avgCost: 100, lastPrice: null }]
    await mountPanel()
    await setInput(0, '600519.SH')
    const btn = buttonWith('反手')
    expect(btn).toBeDefined()
    btn!.vm.$emit('click')
    await flushPromises()
    expect(mock.place).not.toHaveBeenCalled()
    expect(wrapper!.findComponent(NtErrorGuide).props('error')).toContain('反手缺价格证据')
  })

  it('止盈止损联动：买单受理后按勾选建 above/below 告警规则（几何 price×(1±p%)）', async () => {
    await mountPanel()
    await fillForm()
    await flushPromises()
    // 勾止盈 10% + 止损 5%。
    const tpCheck = wrapper!.findAll('.rr-cell input[type="checkbox"]')[0]!
    const slCheck = wrapper!.findAll('.rr-cell input[type="checkbox"]')[1]!
    await tpCheck.setValue(true)
    await slCheck.setValue(true)
    await wrapper!.vm.$nextTick()
    const tpInput = wrapper!.findAllComponents(NtInput).filter((c) => c.classes().includes('rr-input'))[0]!
    const slInput = wrapper!.findAllComponents(NtInput).filter((c) => c.classes().includes('rr-input'))[1]!
    tpInput.vm.$emit('update:modelValue', '10')
    slInput.vm.$emit('update:modelValue', '5')
    await wrapper!.vm.$nextTick()
    await submitForm()
    expect(mock.place).toHaveBeenCalledTimes(1)
    // 联动两条：止盈 above 110*1.1=121.000，止损 below 110*0.95=104.500。
    expect(mock.createRule).toHaveBeenCalledTimes(2)
    const tpCall = mock.createRule.mock.calls.find((c) => (c[0] as { condition: string }).condition === 'above')!
    const slCall = mock.createRule.mock.calls.find((c) => (c[0] as { condition: string }).condition === 'below')!
    expect(tpCall[0]).toMatchObject({ symbol: '600519.SH', condition: 'above', targetPrice: '121.000' })
    expect(slCall[0]).toMatchObject({ symbol: '600519.SH', condition: 'below', targetPrice: '104.500' })
    expect(tpCall[1]).toBe('tester')
  })

  it('止盈止损联动：卖单不建规则（止盈止损语义不适用于卖单，反手键接管）', async () => {
    await mountPanel()
    await fillForm()
    await flushPromises()
    // 买单先勾（R 区可见）再切卖——卖单提交不应建规则。
    const tpCheck = wrapper!.findAll('.rr-cell input[type="checkbox"]')[0]!
    await tpCheck.setValue(true)
    await wrapper!.vm.$nextTick()
    const tpInput = wrapper!.findAllComponents(NtInput).filter((c) => c.classes().includes('rr-input'))[0]!
    tpInput.vm.$emit('update:modelValue', '10')
    await wrapper!.vm.$nextTick()
    await wrapper!.findAll('.tb-side')[1]!.trigger('click')
    await wrapper!.vm.$nextTick()
    await submitForm()
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect(mock.createRule).not.toHaveBeenCalled()
  })

  it('止盈止损联动失败不阻塞主单：createAlertRule 拒一次，主单仍受理 + toast 明示未布防', async () => {
    mock.createRule.mockRejectedValue(new Error('规则库不可达'))
    await mountPanel()
    await fillForm()
    await flushPromises()
    const tpCheck = wrapper!.findAll('.rr-cell input[type="checkbox"]')[0]!
    await tpCheck.setValue(true)
    await wrapper!.vm.$nextTick()
    const tpInput = wrapper!.findAllComponents(NtInput).filter((c) => c.classes().includes('rr-input'))[0]!
    tpInput.vm.$emit('update:modelValue', '10')
    await wrapper!.vm.$nextTick()
    await submitForm()
    // 主单仍受理 + 跳当前委托页；联动失败不静默——toast 告知「你以为有保护其实没有」。
    expect(mock.place).toHaveBeenCalledTimes(1)
    expect(mock.createRule).toHaveBeenCalledTimes(1)
    expect(wrapper!.findComponent(NtTabs).props('modelValue')).toBe('open')
    expect(wrapper!.findComponent(NtErrorGuide).exists()).toBe(false)
    expect(mock.toast.warn).toHaveBeenCalledWith('止盈告警未建成', expect.stringContaining('600519.SH'))
  })
})
