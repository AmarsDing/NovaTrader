import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import OverviewView from '../OverviewView.vue'
import LimitLadder from '@/panels/market/limit-ladder.panel.vue'
import NtButton from '@/ui/NtButton.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import SymbolName from '@/display/SymbolName.vue'
import { request, type RequestOptions } from '@/api/http'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
vi.mock('@/ws/hub', () => ({ isLive: () => false, onPush: () => () => {}, onResync: () => () => {}, subscribe: () => () => {} }))

let offline = false
let populated = false
const failed = new Set<string>()
const wrappers: VueWrapper[] = []
beforeEach(() => {
  offline = false
  populated = false
  failed.clear()
  vi.mocked(request).mockImplementation(async <T>(path: string, options?: RequestOptions): Promise<T> => {
    if (offline || failed.has(path)) throw new Error('接口未连接')
    let result: unknown = { items: [] }
    if (path === '/api/trade/account') result = { account: 'SIM', cash: 100000, equity: 100000 }
    if (path === '/api/risk/state') result = { mode: 'L1' }
    if (path === '/api/market/sentiment') result = {
      asOf: new Date().toISOString(), phase: 'WARM', advance: 100, decline: 80,
      upCount: populated ? 1 : 0, downCount: 0, brokenRate: 0, maxHeight: populated ? 3 : 0,
    }
    if (populated && path === '/api/strategy/signals') result = { items: [{ id: 's1', symbol: '600111.SH', name: '当前信号', status: 'approved', finalScore: 88 }] }
    if (populated && path === '/api/strategy/candidates') result = { items: [{ id: 'c1', symbol: '600222.SH', name: '候选甲', stage: 'ai_scored', missReason: 'below_threshold' }] }
    if (path === '/api/market/limits') result = { boards: populated && options?.query?.direction === 'UP' ? [{ symbol: '600333.SH', status: 'SEALED', consecutive: 3, sealAmount: 10000 }] : [] }
    return result as T
  })
})
afterEach(() => { wrappers.splice(0).forEach((wrapper) => wrapper.unmount()) })

function mountOverview() {
  const wrapper = shallowMount(OverviewView, { global: { plugins: [createPinia()] } })
  wrappers.push(wrapper)
  return wrapper
}
function mountLadder() {
  const wrapper = shallowMount(LimitLadder, { global: { stubs: { NtLadderTiers: false, NtTable: false } } })
  wrappers.push(wrapper)
  return wrapper
}
async function refresh(wrapper: VueWrapper) {
  wrapper.findComponent(NtButton).vm.$emit('click')
  await flushPromises()
}
function stat(wrapper: VueWrapper, label: string) {
  return wrapper.findAllComponents(NtStatCard).find((card) => card.props('label') === label)!
}
/** 梯队/决策台的证券行现在统一走 SymbolName（浅挂载下为桩）：按其 code 断言，顶替旧裸文本断言。 */
function symbolCodes(wrapper: VueWrapper): string[] {
  return wrapper.findAllComponents(SymbolName).map((item) => String(item.props('code')))
}

describe('决策台读取真实性', () => {
  it('首次离线将信号与候选计数显示为未知', async () => {
    offline = true
    const wrapper = mountOverview()
    await flushPromises()
    expect(wrapper.findAll('.lane h3').map((row) => row.text())).toEqual(['待执行 —', '已执行 —', '漏选 —'])
    expect(wrapper.text()).toContain('候选 — · 入选 —')
    expect(wrapper.text()).toContain('信号未返回，待执行数量未知')
    expect(wrapper.text()).not.toContain('没有待执行信号')
    expect(wrapper.text()).not.toContain('没有漏选记录')
  })
  it('成功空集合允许显示零和无记录', async () => {
    const wrapper = mountOverview()
    await flushPromises()
    expect(wrapper.findAll('.lane h3').map((row) => row.text())).toEqual(['待执行 0', '已执行 0', '漏选 0'])
    expect(wrapper.text()).toContain('候选 0 · 入选 0')
    expect(wrapper.text()).toContain('没有待执行信号')
    expect(wrapper.text()).toContain('没有漏选记录')
  })
  it('成功后失败遮住旧信号票和候选', async () => {
    populated = true
    const wrapper = mountOverview()
    await flushPromises()
    // lane 已改 SymbolName(inline)：桩不渲染 code 文本，按组件 prop 断言（旧裸文本断言取消的原因）
    expect(symbolCodes(wrapper)).toContain('600111.SH')
    expect(symbolCodes(wrapper)).toContain('600222.SH')
    offline = true
    await refresh(wrapper)
    expect(symbolCodes(wrapper)).not.toContain('600111.SH')
    expect(symbolCodes(wrapper)).not.toContain('600222.SH')
    expect(wrapper.findAll('.lane h3').every((row) => row.text().includes('—'))).toBe(true)
  })
})

describe('涨停梯队读取真实性', () => {
  it('首次离线不虚构零涨停、零炸板、零板或无名单', async () => {
    offline = true
    const wrapper = mountLadder()
    await flushPromises()
    for (const label of ['涨停', '跌停', '炸板率', '连板高度', '封单总额']) expect(stat(wrapper, label).props('value')).toBe('—')
    expect(stat(wrapper, '炸板率').props('sub')).toBe('炸板数量未返回')
    expect(wrapper.text()).toContain('涨停名单未返回，梯队未知')
    expect(wrapper.text()).not.toContain('今日无涨停梯队')
    expect(wrapper.text()).not.toContain('今日没有炸板')
  })
  it('成功空集合显示观测到的零与空名单', async () => {
    const wrapper = mountLadder()
    await flushPromises()
    expect(stat(wrapper, '涨停').props('value')).toBe('0')
    expect(stat(wrapper, '连板高度').props('value')).toBe('0 板')
    expect(stat(wrapper, '炸板率').props('sub')).toBe('0 只炸板')
    expect(wrapper.text()).toContain('今日无涨停梯队')
    expect(wrapper.text()).toContain('今日没有炸板')
  })
  it('重读失败清除之前的名单和统计值', async () => {
    populated = true
    const wrapper = mountLadder()
    await flushPromises()
    expect(symbolCodes(wrapper)).toContain('600333.SH')
    expect(stat(wrapper, '连板高度').props('value')).toBe('3 板')
    offline = true
    await refresh(wrapper)
    expect(symbolCodes(wrapper)).not.toContain('600333.SH')
    expect(stat(wrapper, '涨停').props('value')).toBe('—')
    expect(stat(wrapper, '连板高度').props('value')).toBe('—')
  })
  it('情绪汇总成功不掩盖名单接口失败', async () => {
    populated = true
    failed.add('/api/market/limits')
    const wrapper = mountLadder()
    await flushPromises()
    expect(stat(wrapper, '涨停').props('value')).toBe('1')
    expect(wrapper.text()).toContain('涨停名单未返回，梯队未知')
    expect(wrapper.text()).toContain('跌停名单未返回')
    expect(wrapper.text()).not.toContain('今日没有炸板')
  })
})
