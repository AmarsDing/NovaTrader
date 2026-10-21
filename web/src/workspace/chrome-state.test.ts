import { createPinia, setActivePinia } from 'pinia'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useAccountStore } from '@/stores/account'
import { useSignalsStore } from '@/stores/signals'
import { useRiskStore } from '@/stores/risk'
import { useNotifyStore } from '@/stores/notify'
import { link } from '@/ws/hub'
import CommandBar from './CommandBar.vue'
import StatusBar from './StatusBar.vue'

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('./inject', () => ({ getWorkbench: () => null }))
const wrappers: VueWrapper[] = []

beforeEach(() => {
  vi.useFakeTimers()
  setActivePinia(createPinia())
  Object.assign(link, { health: 'unknown', ws: 'idle', sync: 'idle', lastAt: null, error: '' })
  vi.spyOn(useSignalsStore(), 'refresh').mockResolvedValue(false)
  vi.spyOn(useNotifyStore(), 'start').mockImplementation(() => {})
})

afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  vi.restoreAllMocks()
  vi.useRealTimers()
})

it('账户读取失败不显示旧权益/盈亏，待执行信号未同步不显示假零', async () => {
  const account = useAccountStore()
  account.equity = 100000
  account.unrealized = 1000
  account.accountOk = false
  const wrapper = mount(CommandBar, { global: { stubs: { KillSwitchBar: true, NovaStatusPulse: true } } })
  wrappers.push(wrapper)
  expect(wrapper.findAll('.metric .value').map((item) => item.text())).toEqual(['—', '—', '—'])
  expect(wrapper.text()).toContain('待核对')
  expect(wrapper.text()).toContain('待同步')
  account.accountOk = true
  account.checkedAt = Date.now()
  await nextTick()
  expect(wrapper.text()).toContain('非实时')
  expect(wrapper.find('.metric.up').exists()).toBe(false)
  Object.assign(link, { health: 'ok', ws: 'open', sync: 'ready' })
  useSignalsStore().ready = true
  await nextTick()
  expect(wrapper.text()).toContain('已核对')
  expect(wrapper.findAll('.metric .value')[2].text()).toBe('0')
})

it('连接成功但未同步不显示实时或Kill Switch未触发，失败原因可见', async () => {
  const risk = useRiskStore()
  risk.mode = 'L1'
  risk.ready = false
  const wrapper = mount(StatusBar)
  wrappers.push(wrapper)
  // 链路五态册（§7.1）无「待检测」键：health unknown 归 off 档，文案取册词「关闭」（禁自封散值）。
  expect(wrapper.text()).toContain('关闭')
  expect(wrapper.text()).toContain('Kill Switch 状态待核对')
  expect(wrapper.text()).not.toContain('Kill Switch 未触发')
  Object.assign(link, { health: 'ok', ws: 'open', sync: 'syncing' })
  await nextTick()
  expect(wrapper.text()).toContain('权威状态同步中')
  expect(wrapper.text()).toContain('不是实时')
  Object.assign(link, { sync: 'required', error: '恢复 position 失败：账户响应未核对' })
  await nextTick()
  expect(wrapper.get('[role="status"]').text()).toContain('账户响应未核对')
  expect(wrapper.text()).toContain('待恢复')
  Object.assign(link, { sync: 'ready', error: '' })
  risk.ready = true
  useAccountStore().accountOk = true
  await nextTick()
  expect(wrapper.text()).toContain('数据实时')
  expect(wrapper.text()).toContain('账户已核对')
  expect(wrapper.text()).toContain('模式 L1')
  expect(wrapper.text()).toContain('Kill Switch 未触发')
})
