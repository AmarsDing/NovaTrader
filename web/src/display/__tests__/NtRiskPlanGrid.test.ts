/**
 * NtRiskPlanGrid 组件（W2c；UI 宪法 R-UI-20/23）合同测试：
 * - 8 槽固定顺序全出（入场/止损/止盈/毛R/净R/风险预算/建议仓位/往返成本），键序即槽序；
 * - 全量数据按口径格式化（价 2 位/R 两位带 x/百分位把小数×100/bps 一位），单位常驻；
 * - 数据纪律：value 缺失/undefined/NaN 一律走 NtMissing（不显 0 不整格蒸发），
 *   0 是真数据照常显；reason 透传为缺位原因徽注；
 * - plan 整包缺省 → 8 槽全缺位（R-UI-23 灰显保位）。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtRiskPlanGrid, { RISK_PLAN_KEYS, type RiskPlanSlotKey } from '../NtRiskPlanGrid.vue'

function slotOrder(wrapper: ReturnType<typeof mount>): string[] {
  return wrapper.findAll('[data-slot]').map((el) => el.attributes('data-slot')!)
}

describe('NtRiskPlanGrid 槽位结构', () => {
  it('8 槽固定顺序全出，键序即槽序', () => {
    const wrapper = mount(NtRiskPlanGrid, { props: { plan: null } })
    expect(slotOrder(wrapper)).toEqual([...RISK_PLAN_KEYS])
    expect(wrapper.findAll('.nt-riskplan__slot')).toHaveLength(8)
  })

  it('标签与单位常驻（单位不省略）', () => {
    const wrapper = mount(NtRiskPlanGrid, { props: { plan: null } })
    const labels = wrapper.findAll('.nt-riskplan__label').map((el) => el.text())
    expect(labels).toEqual(['入场价', '止损价', '止盈价', '毛 R 倍数', '净 R 倍数', '风险预算', '建议仓位', '往返成本'])
    const units = wrapper.findAll('.nt-riskplan__unit').map((el) => el.text())
    expect(units).toEqual(['元', '元', '元', 'R', 'R', '%', '%', 'bps'])
  })
})

describe('NtRiskPlanGrid 有值格式化', () => {
  const plan: Partial<Record<RiskPlanSlotKey, { value: number }>> = {
    entry: { value: 10.5 },
    stopLoss: { value: 10 },
    takeProfit: { value: 11.6 },
    grossR: { value: 2.2 },
    netR: { value: 1.98 },
    riskBudgetPct: { value: 0.01 },
    positionPct: { value: 0.25 },
    costBps: { value: 12.55 },
  }

  it('价 2 位 / R 两位 x / 百分位 ×100 一位 / bps 一位', () => {
    const wrapper = mount(NtRiskPlanGrid, { props: { plan } })
    const byKey = (k: string) => wrapper.find(`[data-slot="${k}"] .nt-riskplan__value`)
    expect(byKey('entry').text()).toBe('10.50')
    expect(byKey('stopLoss').text()).toBe('10.00')
    expect(byKey('takeProfit').text()).toBe('11.60')
    expect(byKey('grossR').text()).toBe('2.20x')
    expect(byKey('netR').text()).toBe('1.98x')
    expect(byKey('riskBudgetPct').text()).toBe('1.0%')
    expect(byKey('positionPct').text()).toBe('25.0%')
    expect(byKey('costBps').text()).toBe('12.6')
  })

  it('0 是真数据照常显（缺位≠0）', () => {
    const wrapper = mount(NtRiskPlanGrid, { props: { plan: { costBps: { value: 0 }, netR: { value: 0 } } } })
    expect(wrapper.find('[data-slot="costBps"] .nt-riskplan__value').text()).toBe('0.0')
    expect(wrapper.find('[data-slot="netR"] .nt-riskplan__value').text()).toBe('0.00x')
  })
})

describe('NtRiskPlanGrid 缺位纪律', () => {
  it('value 缺失/undefined/NaN → NtMissing，不渲数值、不蒸格', () => {
    const wrapper = mount(NtRiskPlanGrid, {
      props: { plan: { entry: {}, stopLoss: { value: null }, takeProfit: { value: Number.NaN } } },
    })
    for (const k of ['entry', 'stopLoss', 'takeProfit']) {
      const slot = wrapper.find(`[data-slot="${k}"]`)
      expect(slot.find('.nt-riskplan__value').exists()).toBe(false)
      expect(slot.find('.nt-missing-inline').exists()).toBe(true)
    }
    expect(wrapper.findAll('.nt-riskplan__missing, .nt-missing-inline')).toHaveLength(8)
  })

  it('reason 透传为缺位原因徽注', () => {
    const wrapper = mount(NtRiskPlanGrid, { props: { plan: { entry: { reason: '源未连接' } } } })
    const miss = wrapper.find('[data-slot="entry"] .nt-missing-inline')
    expect(miss.text()).toContain('源未连接')
    expect(miss.attributes('title')).toBe('源未连接')
  })

  it('plan 整包缺省 → 8 槽全缺位（灰显保位）', () => {
    const wrapper = mount(NtRiskPlanGrid, { props: { plan: null } })
    expect(wrapper.findAll('.nt-missing-inline')).toHaveLength(8)
    expect(wrapper.findAll('.nt-riskplan__value')).toHaveLength(0)
  })
})
