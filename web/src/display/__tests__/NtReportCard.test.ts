/**
 * NtReportCard 组件（W2b；UI 宪法 R-UI-20/23/25）合同测试：
 * - 四段结构全出（决策/数据质量/情景/风险计划），data-section 键序锁定；
 * - 全量数据按口径渲（置信/质量 0..1→%、情景目标价两位、风险段复用 NtRiskPlanGrid 透传）；
 * - 缺口灰显保位（R-UI-23）：任一段缺省显 NtMissing 占位，段不蒸发；
 * - 契约治理（R-UI-25）：schemaVersion 缺省/0 → 旧格式降级提示行且不显版本徽；v1 反向；
 * - 置信/质量越界与非有限同待（不伪造 0%/100%）。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtReportCard, { type ReportCardData } from '../NtReportCard.vue'
import NtRiskPlanGrid from '../NtRiskPlanGrid.vue'

const full: ReportCardData = {
  schemaVersion: 1,
  decision: { bias: '偏多', decision: '参与', confidence: 0.72, rationale: '量能配合，梯队完整。' },
  quality: { overall: 0.85, missing: ['tier_red_rate'] },
  scenarios: [
    { name: '乐观', target: 12.5, trigger: '放量过前高', invalidation: '跌破 20 日线' },
    { name: '基准', target: 11.2 },
    { name: '悲观', target: null },
  ],
  riskPlan: { entry: { value: 10.5 }, stopLoss: { value: 10 } },
}

describe('NtReportCard 四段结构', () => {
  it('四段全出且顺序锁定', () => {
    const wrapper = mount(NtReportCard, { props: { report: full } })
    expect(wrapper.findAll('[data-section]').map((el) => el.attributes('data-section'))).toEqual([
      'decision',
      'quality',
      'scenarios',
      'riskPlan',
    ])
  })

  it('全量数据按口径渲：结论/方向/置信/质量总分/情景目标价', () => {
    const wrapper = mount(NtReportCard, { props: { report: full } })
    expect(wrapper.find('.nt-report__verdict').text()).toBe('参与')
    expect(wrapper.find('.nt-report__bias').text()).toBe('偏多')
    expect(wrapper.find('.nt-report__conf').text()).toBe('置信 72%')
    expect(wrapper.find('.nt-report__score').text()).toBe('85%')
    const targets = wrapper.findAll('.nt-report__scenario-target').map((el) => el.text())
    expect(targets).toEqual(['12.50', '11.20'])
    expect(wrapper.find('.nt-report__missing-chip').text()).toBe('tier_red_rate')
  })

  it('风险段复用 NtRiskPlanGrid 且 plan 透传', () => {
    const wrapper = mount(NtReportCard, { props: { report: full } })
    const grid = wrapper.findComponent(NtRiskPlanGrid)
    expect(grid.exists()).toBe(true)
    expect(grid.props('plan')).toEqual(full.riskPlan)
  })
})

describe('NtReportCard 缺口灰显（R-UI-23 段不蒸发）', () => {
  it('决策段缺省 → 占位 NtMissing，其余三段照出', () => {
    const wrapper = mount(NtReportCard, { props: { report: { schemaVersion: 1, quality: { overall: 0.5 } } } })
    const decision = wrapper.find('[data-section="decision"]')
    expect(decision.find('.nt-missing-inline').exists()).toBe(true)
    expect(wrapper.find('[data-section="quality"] .nt-report__score').text()).toBe('50%')
    expect(wrapper.find('[data-section="scenarios"] .nt-missing-inline').exists()).toBe(true)
    expect(wrapper.findComponent(NtRiskPlanGrid).exists()).toBe(true)
  })

  it('置信缺省/越界/非有限 → 缺位（不伪造 0%/100%）', () => {
    const mk = (confidence: number | null) =>
      mount(NtReportCard, { props: { report: { schemaVersion: 1, decision: { decision: '观望', confidence } } } })
    for (const c of [null, -0.1, 1.2, Number.NaN]) {
      const wrapper = mk(c)
      expect(wrapper.find('.nt-report__conf').exists()).toBe(false)
      expect(wrapper.find('[data-section="decision"] .nt-missing-inline').exists()).toBe(true)
    }
  })

  it('质量的缺失指标空缺 → 显「无缺失登记」而非空表', () => {
    const wrapper = mount(NtReportCard, { props: { report: { schemaVersion: 1, quality: { overall: 1, missing: [] } } } })
    expect(wrapper.find('.nt-report__missing-none').text()).toBe('无缺失登记')
    expect(wrapper.findAll('.nt-report__missing-chip')).toHaveLength(0)
  })
})

describe('NtReportCard 契约版号治理（R-UI-25）', () => {
  it('schemaVersion 缺省/0 → 旧格式降级提示行，无版本徽', () => {
    const legacy = mount(NtReportCard, { props: { report: { decision: { decision: '旧' } } } })
    expect(legacy.find('.nt-report__legacy').exists()).toBe(true)
    expect(legacy.find('.nt-report__ver').exists()).toBe(false)
    const zero = mount(NtReportCard, { props: { report: { schemaVersion: 0 } } })
    expect(zero.find('.nt-report__legacy').exists()).toBe(true)
  })

  it('v1 → 显版本徽，无降级行；report 整包缺省按旧契约处理不崩', () => {
    const wrapper = mount(NtReportCard, { props: { report: full } })
    expect(wrapper.find('.nt-report__ver').text()).toBe('v1')
    expect(wrapper.find('.nt-report__legacy').exists()).toBe(false)
    const empty = mount(NtReportCard, { props: { report: null } })
    expect(empty.find('.nt-report__legacy').exists()).toBe(true)
    expect(empty.findAll('[data-section]')).toHaveLength(4)
  })
})
