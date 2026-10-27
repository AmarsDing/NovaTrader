/**
 * 股池/黑名单面板（W3b.b 扫描链拦截位）契约：
 * 扫描链闸门（数据源/模型）未就绪 → 「扫描链前置检查」卡现身且只列扫描链闸门；
 * 去处理：/config 派 nt:open-config，/datahub 开数据源面板；执行链闸门不外溢；
 * 就绪口不可达 fail-silent，股池主体照常可读。
 */
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import PoolManager from '../strategy/pool-manager.panel.vue'
import NtBlockerCard from '@/display/NtBlockerCard.vue'

const mock = vi.hoisted(() => ({
  listCandidates: vi.fn(),
  addBlacklist: vi.fn(),
  removeBlacklist: vi.fn(),
  readiness: vi.fn(),
  workbench: vi.fn(),
}))
vi.mock('@/api/strategy', () => ({
  listCandidates: mock.listCandidates,
  addBlacklist: mock.addBlacklist,
  removeBlacklist: mock.removeBlacklist,
}))
vi.mock('@/api/ops', () => ({ getOpsReadiness: mock.readiness }))
vi.mock('@/workspace/inject', () => ({ getWorkbench: mock.workbench }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isViewer: () => false, operatorName: 'tester' }) }))

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = shallowMount(PoolManager, { global: { renderStubDefaultSlot: true } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  localStorage.clear()
  mock.listCandidates.mockResolvedValue([])
  mock.readiness.mockResolvedValue({ asOf: '', ready: true, gates: [], blockers: [] })
  mock.workbench.mockReturnValue(null)
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

describe('pool-manager（W3b.b 扫描链拦截位）', () => {
  it('扫描链闸门未就绪 → 卡只列 datahub/llm 两族，执行链闸门被过滤不外溢', async () => {
    mock.readiness.mockResolvedValue({
      asOf: '2026-10-27T09:00:00Z',
      ready: false,
      gates: [],
      blockers: [
        { gate: 'risk_engine', state: 'down', severity: 'danger', why: '引擎未上报', deeplink: '/ops#services' },
        { gate: 'llm_config', state: 'warn', why: '模型未配', deeplink: '/config#models' },
        { gate: 'datahub_sources', state: 'warn', why: '源失联', deeplink: '/datahub#sources' },
      ],
    })
    await mountPanel()
    const card = wrapper!.findComponent(NtBlockerCard)
    expect(card.exists()).toBe(true)
    expect(card.props('title')).toBe('扫描链前置检查')
    const gates = (card.props('blockers') as { gate: string }[]).map((b) => b.gate)
    expect(gates).toEqual(['llm_config', 'datahub_sources'])
  })

  it("'去处理' /config#models → 派发 nt:open-config（category=models），不开面板", async () => {
    mock.readiness.mockResolvedValue({
      asOf: '',
      ready: false,
      gates: [],
      blockers: [{ gate: 'llm_config', state: 'warn', deeplink: '/config#models' }],
    })
    const seen: string[] = []
    const listener = (e: Event) => seen.push((e as CustomEvent<{ category?: string }>).detail?.category ?? '')
    window.addEventListener('nt:open-config', listener)
    try {
      await mountPanel()
      wrapper!.findComponent(NtBlockerCard).vm.$emit('navigate', '/config#models')
      expect(seen).toEqual(['models'])
      expect(mock.workbench).not.toHaveBeenCalled()
    } finally {
      window.removeEventListener('nt:open-config', listener)
    }
  })

  it("'去处理' /datahub#sources → openPanel('datasource-health')", async () => {
    const openPanel = vi.fn()
    mock.workbench.mockReturnValue({ openPanel })
    mock.readiness.mockResolvedValue({
      asOf: '',
      ready: false,
      gates: [],
      blockers: [{ gate: 'datahub_sources', state: 'warn', deeplink: '/datahub#sources' }],
    })
    await mountPanel()
    wrapper!.findComponent(NtBlockerCard).vm.$emit('navigate', '/datahub#sources')
    expect(openPanel).toHaveBeenCalledWith('datasource-health')
  })

  it('全部就绪 → 不现身卡', async () => {
    await mountPanel()
    expect(wrapper!.findComponent(NtBlockerCard).exists()).toBe(false)
  })

  it('就绪口不可达 → fail-silent 不现身卡，股池列表照常可读', async () => {
    mock.readiness.mockRejectedValue(new Error('admin 不可达'))
    mock.listCandidates.mockResolvedValue([{ id: 1, symbol: '600519.SH', pool: 'watch', stage: 'scan' }])
    await mountPanel()
    expect(wrapper!.findComponent(NtBlockerCard).exists()).toBe(false)
    expect(mock.listCandidates).toHaveBeenCalled()
  })
})
