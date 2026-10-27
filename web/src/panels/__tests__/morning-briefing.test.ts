/**
 * 晨报面板（W2b.b 报告卡接线销号）：决策=头版结论+大盘判断理由并入卡、质量=三档核对 omitted
 * 字段名去重登记、情景/风险计划晨报未声明→缺位（null 落卡自治灰显）；
 * mock 只到 http 层，schemaVersion 走真实归一链（缺链不伪造 1）。
 */
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import MorningBriefing from '../brain/morning-briefing.panel.vue'
import NtReportCard, { type ReportCardData } from '@/display/NtReportCard.vue'

const mock = vi.hoisted(() => ({ req: vi.fn() }))
vi.mock('@/api/http', () => ({ request: mock.req }))

const RAW = {
  date: '2026-10-27',
  degraded: false,
  headline: '情绪修复延续，关注高位分歧',
  market_view: '外围企稳，两市缩量整固。\n题材轮动加快，高低切换明显。',
  risks: ['高位股分歧加大'],
  watchlist: [],
  positions: [],
  news: [],
  reason: '',
  evidence: [
    { field: '涨跌家数', span: '3214 家上涨', verdict: 'omitted', match: '' },
    { field: '涨跌家数', span: '1876 家下跌', verdict: 'omitted', match: '' },
    { field: '两市成交额', span: '9234 亿', verdict: 'sourced', match: 'tushare' },
  ],
  omitted_count: 2,
  schema_version: 1,
  created_at: '2026-10-27T08:35:00+08:00',
}

let wrapper: VueWrapper | undefined

async function mountPanel() {
  wrapper = shallowMount(MorningBriefing, { global: { renderStubDefaultSlot: true } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  mock.req.mockResolvedValue({ ...RAW })
})

/** 挂卡并取 report props（窄化非空——四段映射的断言对象）。 */
function reportOf(): ReportCardData {
  const card = wrapper!.findComponent(NtReportCard)
  expect(card.exists()).toBe(true)
  return card.props('report') as ReportCardData
}

describe('晨报面板（W2b.b 报告卡接线）', () => {
  it('决策段=头版+大盘判断真字段并入，质量缺失=omitted 字段去重登记，情景/风险计划缺位灰显', async () => {
    await mountPanel()
    const report = reportOf()
    // 真实归一链：后端 schema_version=1 → schemaVersion 供契约版徽。
    expect(report.schemaVersion).toBe(1)
    expect(report.decision).toEqual({ decision: RAW.headline, rationale: RAW.market_view })
    // 质量：总分无出处→null 缺位；缺失指标=omitted 字段名去重（sourced 不混入，同字段两行不重复）。
    expect(report.quality).toEqual({ overall: null, missing: ['涨跌家数'] })
    // 晨报无情景/价格计划→整段缺位（缺口灰显由卡自治），不硬塞风险清单冒充情景。
    expect(report.scenarios).toBeNull()
    expect(report.riskPlan).toBeNull()
    expect(wrapper!.findComponent(NtReportCard).props('title')).toContain('晨报决策')
    // 头版/大盘判断已并入卡内决策段，不再单独成节重复渲染。
    expect(wrapper!.text()).not.toContain('头版 ·')
  })

  it('404=这一天还没有晨报：不挂卡、不报错引导', async () => {
    mock.req.mockRejectedValue(Object.assign(new Error('not found'), { status: 404 }))
    await mountPanel()
    expect(wrapper!.findComponent(NtReportCard).exists()).toBe(false)
    expect(wrapper!.text()).toContain('这一天还没有晨报')
  })

  it('旧契约缺链（无 schema_version）：版号缺位不伪造 1，走卡的降级提示位；无 omitted=空登记', async () => {
    const { schema_version: _omit, ...legacy } = RAW
    mock.req.mockResolvedValue({ ...legacy, evidence: [], omitted_count: 0 })
    await mountPanel()
    const report = reportOf()
    expect(report.schemaVersion).toBeUndefined()
    expect(report.quality).toEqual({ overall: null, missing: [] })
  })

  it('头版/大盘判断全空：决策段 null 走「未返回」缺位，不渲染空壳决策行', async () => {
    mock.req.mockResolvedValue({ ...RAW, headline: '', market_view: '' })
    await mountPanel()
    const report = reportOf()
    expect(report.decision).toBeNull()
  })
})
