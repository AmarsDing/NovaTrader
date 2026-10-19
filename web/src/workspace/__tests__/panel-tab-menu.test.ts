/**
 * G2 面板菜单纯层钉死：项序形态 / 帮助事件通道 / 弹出白名单纪律 / 关闭内建透传。
 * 挂载面（dockview 自营菜单渲染、jsdom 不起 dockview 实例）不测 DOM，只测菜单数据与 action 副作用。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildPanelTabMenu } from '../panel-tab-menu'

const popoutPanel = vi.hoisted(() => vi.fn())
vi.mock('../inject', () => ({ getWorkbench: () => ({ popoutPanel }) }))

/** 最小 params 替身（纯层只读 panel.id，其余字段本单不消费）。 */
function fakeParams(id: string): Parameters<typeof buildPanelTabMenu>[0] {
  return { panel: { id } } as unknown as Parameters<typeof buildPanelTabMenu>[0]
}

/** 把菜单项压成可读形态（配置项取 label/禁用位，内建项原样）。 */
function shape(items: ReturnType<typeof buildPanelTabMenu>) {
  return items.map((item) =>
    typeof item === 'string' ? item : { label: item.label, disabled: item.disabled === true },
  )
}

afterEach(() => {
  vi.restoreAllMocks()
  popoutPanel.mockClear()
})

describe('buildPanelTabMenu 项序与形态', () => {
  it('功能卡 → 分隔 → 弹出 → 分隔 → 关闭（全自配项收尾）', () => {
    expect(shape(buildPanelTabMenu(fakeParams('market')))).toEqual([
      { label: '功能卡（F1）', disabled: false },
      'separator',
      { label: '弹出为独立窗口', disabled: false },
      'separator',
      { label: '关闭面板', disabled: false },
    ])
  })

  it('刻意不回退内建 popout/close 项：popout 会绕开 POPOUT_ALLOWED 白名单，close 一族标签渲染英文（中文菜单不夹英文词）', () => {
    for (const item of buildPanelTabMenu(fakeParams('market'))) {
      expect(item).not.toBe('popout')
      expect(item).not.toBe('close')
    }
  })
})

describe('帮助入口：nt:open-help 事件通道', () => {
  it('action 派 nt:open-help 且 detail.panelId 随 tab 面板（与 palette 帮助入口同一通道）', () => {
    const spy = vi.spyOn(window, 'dispatchEvent')
    // 刻意用非 market 面板：panelId 透传若被硬编码（变异实验证明此类改动可静默存活）此处立刻红。
    const items = buildPanelTabMenu(fakeParams('signals'))
    const help = items[0]
    expect(typeof help).not.toBe('string')
    if (typeof help !== 'string') help.action?.()
    expect(spy).toHaveBeenCalledTimes(1)
    const event = spy.mock.calls[0]?.[0]
    expect(event).toBeInstanceOf(CustomEvent)
    expect((event as CustomEvent).type).toBe('nt:open-help')
    expect((event as CustomEvent<{ panelId?: string }>).detail.panelId).toBe('signals')
  })
})

describe('弹出项白名单纪律', () => {
  it('白名单面板（risk-report，刻意避开 market 防恒等盲区）可用且走 workbench.popoutPanel 统一把关', () => {
    const items = buildPanelTabMenu(fakeParams('risk-report'))
    const popout = items[2]
    if (typeof popout === 'string') throw new Error('期望配置项')
    expect(popout.disabled).not.toBe(true)
    popout.action?.()
    expect(popoutPanel).toHaveBeenCalledWith('risk-report')
  })

  it('禁弹面板（trade 等写入面）disabled——第一道防线；内层 popoutPanel 另有 canPopout 短路（DockHost.vue:184）', () => {
    const items = buildPanelTabMenu(fakeParams('trade'))
    const popout = items[2]
    if (typeof popout === 'string') throw new Error('期望配置项')
    expect(popout.disabled).toBe(true)
    // 纯层 action 无条件委托（disabled 的拦截在 dockview 渲染层，内层短路在 workbench 实现）；
    // 本层钉死的只有「菜单数据如实给出禁用位」，不越层扮演 dockview 的点击拦截。
  })

  it('未知面板按禁弹处理（不杜撰可达性）', () => {
    const items = buildPanelTabMenu(fakeParams('no-such-panel'))
    const popout = items[2]
    if (typeof popout === 'string') throw new Error('期望配置项')
    expect(popout.disabled).toBe(true)
  })
})
