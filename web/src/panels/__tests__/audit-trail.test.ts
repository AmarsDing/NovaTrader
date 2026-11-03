/**
 * 审计日志面板（W4b 三段式销号）：NtAdminScaffold 承接 + 6 列瘦身表（detail 出列）
 * + 行点击开 320 抽屉（detail 全文+行画像）+ 角色徽标走册（第十一批 role.*，未识别→未知不冒绿）。
 * mock 只到 http 层，PREFS_KEY 不动（nt.prefs.audit-trail.v1）。
 */
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import AuditTrail from '../admin/audit-trail.panel.vue'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'

const mock = vi.hoisted(() => ({ req: vi.fn() }))
vi.mock('@/api/http', () => ({ request: mock.req }))

const ROWS = [
  {
    id: 'a1',
    created_at: '2026-10-27T09:30:00+08:00',
    actor: 'ops-admin',
    role: 'owner',
    action: 'risk.killswitch',
    target: 'global',
    ok: true,
    detail: '触发急停：全盘委托即刻 fail-closed，复盘留痕编号 RS-2026-1027-001。',
  },
  {
    id: 'a2',
    created_at: '2026-10-27T09:31:00+08:00',
    actor: 'trade-bot',
    role: 'operator',
    action: 'trade.order',
    target: '600519.SH',
    ok: false,
    detail: '风控预检拦截：仓位超限。',
  },
  {
    id: 'a3',
    created_at: '2026-10-27T09:32:00+08:00',
    actor: 'guest',
    role: 'auditor', // 册外角色 → role.unknown
    action: 'login',
    target: '',
    ok: true,
    detail: '',
  },
]

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = mount(AuditTrail)
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  vi.resetAllMocks()
  mock.req.mockResolvedValue({ items: ROWS })
})

describe('审计日志面板（W4b 三段式）', () => {
  it('NtAdminScaffold 承接：标题=审计日志、drawer 320 档、初始抽屉关', async () => {
    await mountPanel()
    const scaffold = wrapper!.findComponent(NtAdminScaffold)
    expect(scaffold.exists()).toBe(true)
    expect(scaffold.props('title')).toBe('审计日志')
    expect(scaffold.props('drawerWidth')).toBe(320)
    expect(scaffold.props('drawerOpen')).toBe(false)
  })

  it('主表 6 列瘦身：detail 长文出列（columns 无 detail 键）', async () => {
    await mountPanel()
    /* NtTable 泛型 props 在 mount 包装下拿不到推断——props() 整取后窄化，不逐键取（TS2345 坑）。 */
    const table = wrapper!.findComponent(NtTable)
    expect(table.exists()).toBe(true)
    const props = table.props() as { columns: NtTableColumn<Record<string, unknown>>[]; clickable: boolean }
    const keys = props.columns.map((c) => c.key)
    expect(keys).toEqual(['createdAt', 'actor', 'role', 'action', 'target', 'ok'])
    expect(keys).not.toContain('detail')
    // 行点击行为已挂（clickable + row-click 监听）
    expect(props.clickable).toBe(true)
  })

  it('行点击开抽屉：selected 行画像 + detail 全文进 320 抽屉', async () => {
    await mountPanel()
    const table = wrapper!.findComponent(NtTable)
    // 模拟行点击（NtTable row-click 载荷=行数据）
    table.vm.$emit('row-click', {
      id: 'a1', createdAt: '2026-10-27T09:30:00+08:00', actor: 'ops-admin', role: 'owner',
      action: 'risk.killswitch', target: 'global', ok: true,
      detail: '触发急停：全盘委托即刻 fail-closed，复盘留痕编号 RS-2026-1027-001。',
    })
    await flushPromises()
    const scaffold = wrapper!.findComponent(NtAdminScaffold)
    expect(scaffold.props('drawerOpen')).toBe(true)
  })

  it('角色徽标走册：owner=属主 accent、operator=操作员、册外角色=未知 neutral（不冒绿不冒红）', async () => {
    await mountPanel()
    const text = wrapper!.text()
    expect(text).toContain('属主')
    expect(text).toContain('操作员')
    // auditor 不在册 → role.unknown「未知」，不回显原词冒充已识别角色
    expect(text).toContain('未知')
  })

  it('FE-N4/W1：操作人/结果/时间段读面筛选——只裁已取记录，注记明示口径', async () => {
    await mountPanel()
    // 无筛选：不显示「筛后」注记。
    expect(wrapper!.text()).toContain('3 条记录')
    expect(wrapper!.text()).not.toContain('筛后')

    // 操作人子串筛选（不区分大小写）：仅 trade-bot 行存活。
    await wrapper!.findComponent(NtInput).vm.$emit('update:modelValue', 'trade')
    expect(wrapper!.text()).toContain('筛后 1 / 已取 3 条记录')
    expect(wrapper!.text()).toContain('筛选只作用于已取回的最近')
    await wrapper!.findComponent(NtInput).vm.$emit('update:modelValue', '')

    // 结果筛选：仅失败 → a2 一行。
    const selects = wrapper!.findAllComponents(NtSelect)
    await selects[2]!.vm.$emit('update:modelValue', 'fail')
    expect(wrapper!.text()).toContain('筛后 1 / 已取 3 条记录')

    // 时间段筛选：1h 窗口（样例行均为数日前）→ 0 行，空态承认筛选条件存在。
    await selects[3]!.vm.$emit('update:modelValue', '1h')
    expect(wrapper!.text()).toContain('筛后 0 / 已取 3 条记录')
    expect(wrapper!.text()).toContain('当前筛选条件下没有审计记录')
    // 裁剪不透传重取：审计接口仍只按 action/limit 请求过一次。
    expect(mock.req).toHaveBeenCalledTimes(1)
  })
})
