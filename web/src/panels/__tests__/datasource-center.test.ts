/**
 * 数据源中心面板（W4b 三段式销号）：NtAdminScaffold 承接 + NtTabs 迁入 filter 槽（页签=唯一分段轴，零伪造筛选维度）
 * + 裸码族入册（第十二批 ds.*：fee/requires/official/override/cred 文案唯一出处）
 * + quality severity 章走册（quality.high/medium/low，反向违规收编）。
 * 凭证区例外：password 输入固定在主体卡写面受控位，不进抽屉；PREFS_KEY 不动（nt.prefs.datasource-center.v1）。
 * mock 只到 http 层。
 */
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import DatasourceCenter from '../datahub/datasource-center.panel.vue'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtTabs from '@/ui/NtTabs.vue'

const mock = vi.hoisted(() => ({ req: vi.fn() }))
vi.mock('@/api/http', () => ({ request: mock.req }))
/* auth 门面 mock（pool-manager 先例）：非 viewer 才有写面（凭证区/链编辑），测「已设置」徽章与 QPS 行需要它。 */
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isViewer: () => false }) }))

const CATALOG = {
  version: 3,
  generated_at: '2026-10-27T08:00:00+08:00',
  services_note: '',
  sources: [
    {
      name: 'tushare',
      registered: true,
      domains: ['daily_bar', 'money_flow'],
      fee_tier: 'freemium',
      fee_note: '积分制，超出需付费',
      official: true,
      requires: 'token',
      via: 'go_direct',
      notes: '',
      risk_note: '',
    },
    {
      name: 'tdx',
      registered: true,
      domains: ['daily_bar', 'tick'],
      fee_tier: 'free',
      fee_note: '',
      official: true,
      requires: 'sidecar',
      via: 'pyworker',
      notes: '',
      risk_note: '',
    },
    {
      name: 'cls',
      registered: false,
      domains: ['news'],
      fee_tier: '',
      fee_note: '',
      official: false,
      requires: '',
      via: 'source_worker',
      notes: '',
      risk_note: '',
    },
  ],
  domains: [{ name: 'daily_bar', default_chain: ['tushare', 'tdx'], candidates: ['tushare', 'tdx'] }],
}

const SNAPSHOT = {
  /* normalizeConfigSnapshot 合同：schema_version!==1 直接 throw（「后端未提供版本化配置合同」），快照即废弃。 */
  schema_version: 1,
  revision: 7,
  values: { 'app.domain.daily_bar.sources': '["tdx","tushare"]' },
  /* secret_set snake 源串经归一链 → secretSet 驼峰；面板读归一后的 secretSet。 */
  secret_set: { 'app.ds.tushare.token': true },
  receipts: [],
  history: [],
  created_at: '2026-10-27T08:00:00+08:00',
  note: '',
  receipt_error: '',
}

function route(url: string, opts?: { method?: string }) {
  if (url.includes('/api/datahub/source-catalog')) return CATALOG
  if (url.includes('/api/datahub/sources')) return { items: [] }
  if (url.includes('/api/datahub/quality')) return { issues: [] }
  if (url === '/api/config') {
    if (opts?.method === 'PUT') return { ...SNAPSHOT, revision: SNAPSHOT.revision + 1 }
    return SNAPSHOT
  }
  if (url.includes('/api/config/preview')) return { base_revision: SNAPSHOT.revision, changes: [], services: [], known_effects: [], unknown_effects: [], invalidates_plans: false }
  return {}
}

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = mount(DatasourceCenter)
  await flushPromises()
  await flushPromises() // 能力探测四个 preview 落定
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  vi.resetAllMocks()
  mock.req.mockImplementation((url: string, opts?: { method?: string }) => Promise.resolve(route(url, opts)))
})

describe('数据源中心面板（W4b 三段式）', () => {
  it('NtAdminScaffold 承接：标题=数据源中心、无抽屉（三页签已分段）、NtTabs 在 filter 槽', async () => {
    await mountPanel()
    const scaffold = wrapper!.findComponent(NtAdminScaffold)
    expect(scaffold.exists()).toBe(true)
    expect(scaffold.props('title')).toBe('数据源中心')
    expect(scaffold.props('drawerWidth')).toBe(0)
    expect(wrapper!.findComponent(NtTabs).exists()).toBe(true)
  })

  it('裸码族入册：fee/req 章词出自 ds.* 册（免费+/需 token/需边车/免凭证/官方/未启用）', async () => {
    await mountPanel()
    const text = wrapper!.text()
    expect(text).toContain('免费+')
    expect(text).toContain('免费')
    expect(text).toContain('需 token')
    expect(text).toContain('需边车')
    expect(text).toContain('官方')
    expect(text).toContain('未启用')
  })

  it('覆盖态入册：daily_bar 运行态覆盖生效（ds.override.active），非 yaml 默认', async () => {
    await mountPanel()
    const tabs = wrapper!.findComponent(NtTabs)
    tabs.vm.$emit('update:modelValue', 'domains')
    await flushPromises()
    const text = wrapper!.text()
    expect(text).toContain('运行态覆盖生效')
  })

  it('凭证区例外：token password 输入固定主体写面，已设置徽章走册（ds.cred.set）', async () => {
    await mountPanel()
    const text = wrapper!.text()
    expect(text).toContain('已设置')
    // 抽屉不存在——凭证永不进抽屉裸显
    expect(wrapper!.find('[data-region="drawer"]').exists()).toBe(false)
  })
})
