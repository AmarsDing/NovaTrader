/**
 * 通知中心（本轮重塑）：
 * - 首层行模型：时间/L 级/情绪/单行标题+摘要/真实 symbol/×N/未读态；
 * - 筛选 chips 与计数口径自洽；详情模态 640 档、无股票不放假按钮；
 * - 已读态不用整行 opacity（未读条为绝对定位伪元素）；
 * - WS 同 id 连发只保留一条；hover 摘要 160ms 开 / 140ms 关。
 */
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import NotifyCenter from '../notify/notify-center.panel.vue'
import { useSymbolStore } from '@/stores/symbol'

const mock = vi.hoisted(() => ({ req: vi.fn(), push: null as null | ((msg: unknown) => void), openPanel: vi.fn() }))
vi.mock('@/api/http', () => ({ request: mock.req }))
vi.mock('@/ws/hub', () => ({
  onPush: (fn: (msg: unknown) => void) => {
    mock.push = fn
    return vi.fn()
  },
}))
vi.mock('@/workspace/inject', () => ({ getWorkbench: () => ({ openPanel: mock.openPanel }) }))

const MESSAGES = [
  {
    id: 'm1',
    kind: 'risk',
    level: 'critical',
    title: '风控拦截：仓位超限',
    body: '600519.SH 委托被风控预检拦截：单票仓位超过上限 20%。',
    time: '2026-10-27T09:31:00+08:00',
    read: false,
    priority: 'critical',
    category: 'risk',
    dedup_key: '',
  },
  {
    id: 'm2',
    kind: 'briefing',
    level: 'high',
    title: '晨报已生成',
    body: '2026-10-27 晨报已生成：情绪修复延续，关注高位分歧。',
    time: '2026-10-27T08:35:00+08:00',
    read: true,
    priority: 'high',
    category: 'briefing',
    dedup_key: '',
    symbol: '600519.SH',
  },
]

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = mount(NotifyCenter, { attachTo: document.body })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  vi.resetAllMocks()
  mock.push = null
  mock.req.mockResolvedValue({ items: MESSAGES })
})

afterEach(() => {
  wrapper?.unmount()
  vi.useRealTimers()
})

describe('通知中心（三层密度）', () => {
  it('行模型：每条一行，未读条与已读行同时存在', async () => {
    await mountPanel()
    expect(wrapper!.findAll('.nc-row')).toHaveLength(2)
    expect(wrapper!.findAll('.nc-row.unread')).toHaveLength(1)
    expect(wrapper!.find('.nc-row.unread').classes()).toContain('unread')
    /* 已读行 opacity 必须为 1（不再用整行透明度压对比度）。 */
    const readRow = wrapper!.findAll('.nc-row')[1]
    expect((readRow.element as HTMLElement).style.opacity || '1').toBe('1')
  })

  it('symbol 只透传真实字段：m1 正文里的代码不被识别为关联股票', async () => {
    await mountPanel()
    const rows = wrapper!.findAll('.nc-row')
    expect(rows[0].find('.nc-row__symbol').exists()).toBe(false)
    expect(rows[1].find('.nc-row__symbol').text()).toBe('600519.SH')
  })

  it('筛选 chips 与计数口径：切到"有股票"后只余 1 条', async () => {
    await mountPanel()
    const hasSymbol = wrapper!.findAll('.nc-chip').find((c) => c.text() === '有股票')!
    await hasSymbol.trigger('click')
    expect(wrapper!.findAll('.nc-row')).toHaveLength(1)
    expect(wrapper!.text()).toContain('显示 1 / 筛选后 1 · 未读 0')
  })

  it('详情模态：点行打开、标已读、正文可见；无股票显示"无关联股票"且无跳转按钮', async () => {
    await mountPanel()
    await wrapper!.findAll('.nc-row')[0].trigger('click')
    await flushPromises()
    const modal = document.querySelector<HTMLElement>('.nc-modal')
    expect(modal).not.toBeNull()
    expect(modal!.textContent).toContain('风控拦截：仓位超限')
    expect(modal!.textContent).toContain('无关联股票')
    expect(modal!.querySelectorAll('.nc-stock-action')).toHaveLength(0)
    expect(localStorage.getItem('nt.notify-read.v1')).toContain('m1')
  })

  it('股票主动作：setSymbol(NOTIFY) + 同时打开 market/trade，不产生订单请求', async () => {
    await mountPanel()
    await wrapper!.findAll('.nc-row')[1].trigger('click')
    await flushPromises()
    const action = document.querySelector<HTMLElement>('.nc-stock-action')
    expect(action).not.toBeNull()
    action!.click()
    await flushPromises()
    const store = useSymbolStore()
    expect(store.code).toBe('600519.SH')
    expect(store.source).toBe('NOTIFY')
    expect(mock.openPanel).toHaveBeenCalledWith('market', { symbol: '600519.SH' })
    expect(mock.openPanel).toHaveBeenCalledWith('trade', { symbol: '600519.SH' })
    expect(document.querySelector('.nc-modal')).toBeNull()
  })

  it('WS 同 id 连发两次：列表只保留一条', async () => {
    await mountPanel()
    const before = wrapper!.findAll('.nc-row').length
    const frame = { topic: 'alert', seq: 9, data: { id: 'ws-fixed', title: '重复帧', kind: 'signal', symbol: '600519.SH' } }
    mock.push!(frame)
    await flushPromises()
    mock.push!(frame)
    await flushPromises()
    expect(wrapper!.findAll('.nc-row').length).toBe(before + 1)
    expect(wrapper!.findAll('[data-row-id="ws-fixed"]')).toHaveLength(1)
  })

  it('hover 摘要：159ms 未开、160ms 开、移开后 139ms 仍在、140ms 关闭', async () => {
    vi.useFakeTimers()
    await mountPanel()
    const row = wrapper!.findAll('.nc-row')[0]
    await row.trigger('mouseenter')
    vi.advanceTimersByTime(159)
    await flushPromises()
    expect(document.querySelector('.nc-summary')).toBeNull()
    vi.advanceTimersByTime(1)
    await flushPromises()
    expect(document.querySelector('.nc-summary')).not.toBeNull()
    await row.trigger('mouseleave')
    vi.advanceTimersByTime(139)
    await flushPromises()
    expect(document.querySelector('.nc-summary')).not.toBeNull()
    vi.advanceTimersByTime(1)
    await flushPromises()
    expect(document.querySelector('.nc-summary')).toBeNull()
  })

  it('键盘：Enter 打开详情', async () => {
    await mountPanel()
    await wrapper!.findAll('.nc-row')[0].trigger('keydown.enter')
    await flushPromises()
    expect(document.querySelector('.nc-modal')).not.toBeNull()
  })
})
