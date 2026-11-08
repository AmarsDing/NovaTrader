/**
 * KPI 六格行契约：涨/平/跌三色拼接、炸板率 warn 档、最高连板与情绪阶段 accent 档、
 * 评分系数副行、stale 快照过期警示；sentiment=null 全格「—」（数据纪律不冒充 0）。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { Sentiment } from '@/api/market'
import NtKpiRow from '@/display/NtKpiRow.vue'

function sentiment(patch: Partial<Sentiment> = {}): Sentiment {
  return {
    tradeDate: '2026-11-08',
    asOf: '',
    upCount: 45,
    downCount: 3,
    brokenCount: 12,
    brokenRate: 0.21,
    maxHeight: 5,
    profitEffect: 0.62,
    advance: 3100,
    decline: 1800,
    flat: 210,
    amount: 8.9e11,
    phase: 'WARM',
    riskLevel: 1,
    scoreCoef: 0.85,
    positionScale: 0.6,
    stale: false,
    ...patch,
  }
}

describe('NtKpiRow', () => {
  it('六格齐：三色拼接 / 涨跌停 / 炸板率 warn / 最高连板 accent / 成交额 / 阶段', () => {
    const wrapper = mount(NtKpiRow, { props: { sentiment: sentiment() } })
    const cells = wrapper.findAll('.kpi-cell')
    expect(cells).toHaveLength(6)

    const tri = cells[0]!
    expect(tri.find('.up').text()).toBe('3100')
    expect(tri.find('.flat').text()).toBe('210')
    expect(tri.find('.down').text()).toBe('1800')

    const limits = cells[1]!
    expect(limits.find('.up').text()).toBe('45')
    expect(limits.find('.down').text()).toBe('3')

    const broken = cells[2]!
    expect(broken.find('.kpi-cell__v').classes()).toContain('warn')
    expect(broken.text()).toContain('21.00%')
    expect(broken.text()).toContain('12 只炸板')

    const height = cells[3]!
    expect(height.find('.kpi-cell__v').classes()).toContain('accent')
    expect(height.text()).toContain('5 板')

    expect(cells[4]!.text()).toContain('8900.00 亿')

    const phase = cells[5]!
    expect(phase.text()).toContain('升温')
    expect(phase.text()).toContain('评分系数 ×0.85')
  })

  it('stale 快照：阶段副行显「快照过期」warn', () => {
    const wrapper = mount(NtKpiRow, { props: { sentiment: sentiment({ stale: true }) } })
    const sub = wrapper.findAll('.kpi-cell')[5]!.find('.kpi-cell__s')
    expect(sub.text()).toBe('快照过期')
    expect(sub.classes()).toContain('kpi-cell__s--warn')
  })

  it('sentiment=null：六格全「—」，不冒充 0', () => {
    const wrapper = mount(NtKpiRow, { props: { sentiment: null } })
    const cells = wrapper.findAll('.kpi-cell')
    expect(cells).toHaveLength(6)
    expect(cells[0]!.find('.up').text()).toBe('—')
    expect(cells[2]!.find('.kpi-cell__v').text()).toBe('—')
    expect(cells[3]!.find('.kpi-cell__v').text()).toBe('—')
    expect(cells[4]!.find('.kpi-cell__v').text()).toBe('—')
    expect(cells[5]!.find('.kpi-cell__v').text()).toBe('—')
  })
})
