/**
 * deeplink 深链解析器（W3b.b）契约：
 * /config → nt:open-config 事件（带 category 锚）；已登记路径 → 工作台面板；
 * 未登记路径返回 false 不做任何动作（宁可露码不指错门）。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { openDeeplink } from '@/utils/deeplink'

const mock = vi.hoisted(() => ({ workbench: vi.fn(), openPanel: vi.fn() }))
vi.mock('@/workspace/inject', () => ({ getWorkbench: mock.workbench }))

/** 捕获 nt:open-config 派发；返回所收 category 序列（缺省记 ''）。 */
function watchConfigEvent(): { seen: string[]; stop: () => void } {
  const seen: string[] = []
  const listener = (e: Event) => seen.push((e as CustomEvent<{ category?: string }>).detail?.category ?? '')
  window.addEventListener('nt:open-config', listener)
  return { seen, stop: () => window.removeEventListener('nt:open-config', listener) }
}

beforeEach(() => {
  vi.resetAllMocks()
  mock.workbench.mockReturnValue({ openPanel: mock.openPanel })
})

describe('deeplink 深链解析器（W3b.b）', () => {
  it("'/config#models' 派发 nt:open-config（category=models），不开面板", () => {
    const { seen, stop } = watchConfigEvent()
    try {
      expect(openDeeplink('/config#models')).toBe(true)
      expect(seen).toEqual(['models'])
      expect(mock.workbench).not.toHaveBeenCalled()
      expect(mock.openPanel).not.toHaveBeenCalled()
    } finally {
      stop()
    }
  })

  it("'/config' 无锚同样派发事件（category 缺省由 ConfigCenter 回落默认分组）", () => {
    const { seen, stop } = watchConfigEvent()
    try {
      expect(openDeeplink('/config')).toBe(true)
      expect(seen).toEqual([''])
    } finally {
      stop()
    }
  })

  it("'/risk#killswitch' → openPanel('risk-chain')，不派配置事件", () => {
    const { seen, stop } = watchConfigEvent()
    try {
      expect(openDeeplink('/risk#killswitch')).toBe(true)
      expect(mock.openPanel).toHaveBeenCalledWith('risk-chain')
      expect(seen).toEqual([])
    } finally {
      stop()
    }
  })

  it("'/ops#services' → ops；'/trade#broker' → trade；'/datahub#sources' → datasource-health", () => {
    expect(openDeeplink('/ops#services')).toBe(true)
    expect(openDeeplink('/trade#broker')).toBe(true)
    expect(openDeeplink('/datahub#sources')).toBe(true)
    expect(mock.openPanel.mock.calls.map((c) => c[0])).toEqual(['ops', 'trade', 'datasource-health'])
  })

  it('未登记路径不做任何动作并返回 false（不静默跳错门）', () => {
    const { seen, stop } = watchConfigEvent()
    try {
      expect(openDeeplink('/strategy#pool')).toBe(false)
      expect(mock.workbench).not.toHaveBeenCalled()
      expect(mock.openPanel).not.toHaveBeenCalled()
      expect(seen).toEqual([])
    } finally {
      stop()
    }
  })

  it('workbench 未注入（面板孤儿场景）不炸，仍按已登记回报 true', () => {
    mock.workbench.mockReturnValue(null)
    expect(openDeeplink('/trade#broker')).toBe(true)
    expect(mock.openPanel).not.toHaveBeenCalled()
  })
})
