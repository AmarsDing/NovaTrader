/**
 * 数据源治理面板（W4b 三段式销号）：NtAdminScaffold 承接 + 双表瘦身（源表 detail / 质量表 message 出列）
 * + 行点击开 320 抽屉（源=画像+lastError 全文+采集副本钮；质量=对照源+message 全文）。
 * P3-10c 守卫区原样：successRate null 显「—」不参与告警（没取到≠0）。
 * mock 只到 http 层。
 */
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import DatasourceHealth from '../datahub/datasource-health.panel.vue'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'

const mock = vi.hoisted(() => ({ req: vi.fn() }))
vi.mock('@/api/http', () => ({ request: mock.req }))

const SOURCES = [
  {
    id: 'daily_bar:tushare',
    name: 'tushare',
    kind: 'daily_bar',
    enabled: true,
    status: 'closed',
    detail: '',
    last_success_at: '2026-10-27T09:00:00+08:00',
    success_rate: 0.97,
    latency_ms: 120,
    consecutive_failures: 0,
    success_count: 32,
    failure_count: 1,
  },
  {
    id: 'news:cls',
    name: 'cls',
    kind: 'news',
    enabled: true,
    status: 'open',
    detail: '熔断打开',
    last_success_at: '2026-10-27T08:00:00+08:00',
    success_rate: null, // 统计窗口无采样 → 「—」不冒充满分
    latency_ms: null,
    consecutive_failures: 5,
    success_count: 0,
    failure_count: 5,
    last_error: '连接被拒绝（10 次重试失败）',
  },
]

const QUALITY = [
  {
    id: 'q1',
    domain: 'daily_bar',
    check_name: 'missing',
    symbol: '600519.SH',
    trade_date: '2026-10-26',
    severity: 'high',
    source: 'tushare',
    other_source: 'tdx',
    message: '日线缺失：双源对照 tdx 有数据、tushare 无。',
    hits: 3,
    last_seen: '2026-10-27T08:30:00+08:00',
  },
]

function route(url: string) {
  if (url.includes('/api/datahub/sources')) return { items: SOURCES }
  if (url.includes('/api/datahub/quality')) return { items: QUALITY }
  return {}
}

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = mount(DatasourceHealth)
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  mock.req.mockImplementation((url: string) => Promise.resolve(route(url)))
})

describe('数据源治理面板（W4b 三段式）', () => {
  it('NtAdminScaffold 承接：标题=数据源治理、drawer 320 档、初始抽屉关', async () => {
    await mountPanel()
    const scaffold = wrapper!.findComponent(NtAdminScaffold)
    expect(scaffold.exists()).toBe(true)
    expect(scaffold.props('title')).toBe('数据源治理')
    expect(scaffold.props('drawerWidth')).toBe(320)
    expect(scaffold.props('drawerOpen')).toBe(false)
  })

  it('双表瘦身：源表 8 列无 detail、质量表 8 列无 message（长文出列进抽屉）', async () => {
    await mountPanel()
    const tables = wrapper!.findAllComponents(NtTable)
    expect(tables.length).toBe(2)
    /* NtTable 泛型 props 在 mount 包装下拿不到推断——props() 整取后窄化，不逐键取（TS2345 坑）。 */
    const srcKeys = (tables[0].props() as { columns: NtTableColumn<Record<string, unknown>>[] }).columns.map((c) => c.key)
    const qKeys = (tables[1].props() as { columns: NtTableColumn<Record<string, unknown>>[] }).columns.map((c) => c.key)
    expect(srcKeys).not.toContain('detail')
    expect(qKeys).not.toContain('message')
    expect(srcKeys).toContain('successRate')
    expect(qKeys).toContain('severity')
  })

  it('P3-10c 守卫区：successRate null 显「—」不参与告警，0.97 正常显百分比', async () => {
    await mountPanel()
    const text = wrapper!.text()
    expect(text).toContain('97%')
    expect(text).toContain('—')
    // cls 源 successRate=null：不冒充满分（旧病 (?? 1) < 0.9 告警静默消失）
    expect(text).not.toContain('100%')
  })

  it('行点击开抽屉：源行→selectedSource 双态（质量互斥清空）；质量行→selectedQuality', async () => {
    await mountPanel()
    const tables = wrapper!.findAllComponents(NtTable)
    /* W0-3b：行形参=NtSourceTable 归一行（以 key 为身份），不再透原始 DataSource。 */
    tables[0].vm.$emit('row-click', { key: 'news:cls' })
    await flushPromises()
    expect(wrapper!.findComponent(NtAdminScaffold).props('drawerOpen')).toBe(true)
  })
})
