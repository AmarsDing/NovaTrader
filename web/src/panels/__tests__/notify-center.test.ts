/**
 * 通知中心面板（W4b 三段式销号）：NtAdminScaffold 承接 + 三 Select 迁入 filter 槽
 * + NtTimeline 主体 + 320 消息详情抽屉（select 分流=标已读+开抽屉双发，G3 语义保真）
 * + 静默注记走册（第十三批 notify.silenced）。
 * W4c 二维分级：notify.kind.* 册章预取入行模型（kindBadge），抽屉类别行同册徽注。
 * PREFS_KEY（nt.prefs.notify-center.v1）与 NOTIF_READ_KEY（nt.notify-read.v1）不动。
 * mock 只到 http 层 + ws/hub + workspace/inject。
 */
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import NotifyCenter from '../notify/notify-center.panel.vue'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtTimeline from '@/display/NtTimeline.vue'
import NtBadge from '@/ui/NtBadge.vue'

const mock = vi.hoisted(() => ({ req: vi.fn() }))
vi.mock('@/api/http', () => ({ request: mock.req }))
vi.mock('@/ws/hub', () => ({ onPush: () => vi.fn() }))
vi.mock('@/workspace/inject', () => ({ getWorkbench: () => null }))

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
  },
]

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = mount(NotifyCenter)
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  vi.resetAllMocks()
  mock.req.mockResolvedValue({ items: MESSAGES })
})

describe('通知中心面板（W4b 三段式）', () => {
  it('NtAdminScaffold 承接：标题=通知中心、drawer 320 档、初始抽屉关', async () => {
    await mountPanel()
    const scaffold = wrapper!.findComponent(NtAdminScaffold)
    expect(scaffold.exists()).toBe(true)
    expect(scaffold.props('title')).toBe('通知中心')
    expect(scaffold.props('drawerWidth')).toBe(320)
    expect(scaffold.props('drawerOpen')).toBe(false)
  })

  it('NtTimeline 主体：归一合并行入列（未读 1 条），时间线 selectable', async () => {
    await mountPanel()
    const timeline = wrapper!.findComponent(NtTimeline)
    expect(timeline.exists()).toBe(true)
    expect(timeline.props('selectable')).toBe(true)
    const items = timeline.props('items') as { id: string; read: boolean }[]
    expect(items.map((i) => i.id)).toContain('m1')
    expect(items.map((i) => i.id)).toContain('m2')
    expect(wrapper!.text()).toContain('未读 1')
  })

  it('select 分流=标已读+开抽屉双发：G3 已读语义保真（localStorage 落账）', async () => {
    await mountPanel()
    const timeline = wrapper!.findComponent(NtTimeline)
    const items = timeline.props('items') as { id: string }[]
    const m1 = items.find((i) => i.id === 'm1')!
    timeline.vm.$emit('select', m1)
    await flushPromises()
    // 双发之一：抽屉开
    expect(wrapper!.findComponent(NtAdminScaffold).props('drawerOpen')).toBe(true)
    // 双发之二：已读落账（G3 语义——localStorage 读档键不动）
    expect(localStorage.getItem('nt.notify-read.v1')).toContain('m1')
    // 重挂后未读数归 0
    const items2 = wrapper!.findComponent(NtTimeline).props('items') as { id: string; read: boolean }[]
    expect(items2.find((i) => i.id === 'm1')!.read).toBe(true)
  })

  it('抽屉详情：选中行全文（标题/正文/级别/类别）进 320 抽屉', async () => {
    await mountPanel()
    const timeline = wrapper!.findComponent(NtTimeline)
    const items = timeline.props('items') as { id: string }[]
    timeline.vm.$emit('select', items.find((i) => i.id === 'm2')!)
    await flushPromises()
    const scaffold = wrapper!.findComponent(NtAdminScaffold)
    expect(scaffold.props('drawerOpen')).toBe(true)
    // 抽屉全量挂载：正文/字段行可见（不再走 stub 透传）
    const text = wrapper!.text()
    expect(text).toContain('晨报已生成')
    expect(text).toContain('消息详情')
    expect(text).toContain('2026-10-27 晨报已生成：情绪修复延续，关注高位分歧。')
    /* W4c：抽屉类别行走册徽注——briefing → 晨报 ai 章 + 裸码并显（断言收窄在抽屉域内，不被列表行章代过）。 */
    const drawer = wrapper!.find('.nc-drawer')
    /* W4c review F2：断言收窄在抽屉域内；findAllComponents 泛型 props 整取后显式窄化（W4b F7 同款模式）。 */
    const badges = drawer.findAllComponents(NtBadge) as Array<{
      text: () => string
      props: () => { tone?: string }
    }>
    expect(badges.some((b) => b.text() === '晨报' && b.props().tone === 'ai')).toBe(true)
    expect(drawer.text()).toContain('（briefing）')
  })

  it('W4c 类别徽注：册内 kind 预取 kindBadge 入行模型（risk→风控 warn / briefing→晨报 ai）', async () => {
    await mountPanel()
    const items = wrapper!.findComponent(NtTimeline).props('items') as {
      id: string
      kindBadge?: { text: string; tone: string }
      tag?: string
    }[]
    /* 册内 kind 打章；裸 kind 仍留 tag 位兜底（册外值才回显） */
    expect(items.find((i) => i.id === 'm1')!.kindBadge).toEqual({ text: '风控', tone: 'warn' })
    expect(items.find((i) => i.id === 'm2')!.kindBadge).toEqual({ text: '晨报', tone: 'ai' })
  })
})
