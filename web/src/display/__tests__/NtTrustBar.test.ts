/**
 * NtTrustBar 组件（W8b；UI 宪法 R-UI-04/20/23）合同测试：
 * - 12 判据位固定槽序全出（数据纪律：缺位徽注词全取自状态册，组件不自封文案）；
 * - 数值/口径/凭据格式化口径锁定（bps 一位小数、count 带单位、mono 截断槽 title 全文）；
 * - 判据位正反面：verified=true 才渲「已验证」success；false/缺 bar 一律 NtMissing「未验证」，
 *   零值绝不伪造绿勾（W8a 契约契约锚 service trustbar_test）；
 * - tradeCount=0 → 「样本不足」（proto 契约：0 笔=样本不足结论位，不是 0% 胜率）；
 * - bar 整包缺省 → 12 槽全缺位（R-UI-23 缺步灰显）。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtTrustBar from '../NtTrustBar.vue'
import type { TrustBar } from '@/api/types/backtest'

const fullBar: TrustBar = {
  barsTotal: 120,
  tradingDays: 100,
  tradeCount: 42,
  windowStart: '2026-01-01',
  windowEnd: '2026-06-01',
  costModel: '佣金+冲击成本',
  feeBps: 2.5,
  slippageBps: 10,
  blindVerified: true,
  pboVerified: false,
  datasetManifestId: 'm-1',
  datasetHash: 'h-abcdef0123456789',
}

function slot(wrapper: ReturnType<typeof mount>, key: string) {
  return wrapper.find(`[data-slot="${key}"]`)
}

describe('NtTrustBar 槽位结构', () => {
  it('12 槽固定顺序全出，键序即槽序', () => {
    const wrapper = mount(NtTrustBar, { props: { bar: fullBar } })
    expect(wrapper.findAll('[data-slot]').map((el) => el.attributes('data-slot'))).toEqual([
      'barsTotal',
      'tradingDays',
      'tradeCount',
      'windowStart',
      'windowEnd',
      'costModel',
      'feeBps',
      'slippageBps',
      'blindVerified',
      'pboVerified',
      'datasetManifestId',
      'datasetHash',
    ])
  })

  it('槽标签一字锁定（12 词）', () => {
    const wrapper = mount(NtTrustBar, { props: { bar: null } })
    expect(wrapper.findAll('.nt-trust__label').map((el) => el.text())).toEqual([
      'K 线样本',
      '交易日',
      '成交笔数',
      '窗口起',
      '窗口止',
      '成本口径',
      '佣金',
      '滑点',
      '盲测判据',
      '过拟合判据',
      '冻结数据集',
      '数据哈希',
    ])
  })
})

describe('NtTrustBar 有值渲染口径', () => {
  it('count 带单位、bps 一位小数、日期/口径/凭据原文', () => {
    const wrapper = mount(NtTrustBar, { props: { bar: fullBar } })
    expect(slot(wrapper, 'barsTotal').find('.nt-trust__value').text()).toBe('120')
    expect(slot(wrapper, 'barsTotal').find('.nt-trust__unit').text()).toBe('根')
    expect(slot(wrapper, 'tradeCount').find('.nt-trust__value').text()).toBe('42')
    expect(slot(wrapper, 'feeBps').find('.nt-trust__value').text()).toBe('2.5')
    expect(slot(wrapper, 'slippageBps').find('.nt-trust__value').text()).toBe('10.0')
    expect(slot(wrapper, 'windowStart').find('.nt-trust__value').text()).toBe('2026-01-01')
    expect(slot(wrapper, 'costModel').find('.nt-trust__value').text()).toBe('佣金+冲击成本')
    expect(slot(wrapper, 'datasetManifestId').find('.nt-trust__value').text()).toBe('m-1')
  })

  it('mono 凭据槽 title 给全文（截断后仍可对账），非 mono 槽无 title', () => {
    const wrapper = mount(NtTrustBar, { props: { bar: fullBar } })
    expect(slot(wrapper, 'datasetHash').find('.nt-trust__value').attributes('title')).toBe('h-abcdef0123456789')
    expect(slot(wrapper, 'barsTotal').find('.nt-trust__value').attributes('title')).toBeUndefined()
  })
})

describe('NtTrustBar 判据位正反面（零值绝不伪造绿）', () => {
  it('双通过 → 两枚「已验证」success；半通过 → 未过侧 NtMissing「未验证」', () => {
    const both = mount(NtTrustBar, { props: { bar: { ...fullBar, blindVerified: true, pboVerified: true } } })
    expect(both.findAll('.nt-trust__pass').map((el) => el.text())).toEqual(['已验证', '已验证'])
    const half = mount(NtTrustBar, { props: { bar: fullBar } })
    expect(slot(half, 'blindVerified').find('.nt-trust__pass').text()).toBe('已验证')
    expect(slot(half, 'blindVerified').find('.nt-missing-inline').exists()).toBe(false)
    expect(slot(half, 'pboVerified').find('.nt-trust__pass').exists()).toBe(false)
    expect(slot(half, 'pboVerified').find('.nt-missing-inline').text()).toBe('未验证')
  })
})

describe('NtTrustBar 缺位纪律（没取到≠0）', () => {
  it('tradeCount=0 → 「样本不足」结论位（有信息的事实，不是空）', () => {
    const wrapper = mount(NtTrustBar, { props: { bar: { ...fullBar, tradeCount: 0 } } })
    expect(slot(wrapper, 'tradeCount').find('.nt-trust__value').exists()).toBe(false)
    expect(slot(wrapper, 'tradeCount').find('.nt-missing-inline').text()).toBe('样本不足')
  })

  it('适配层零值折缺后用册词徽注：null 计数→没取到，空声明→未声明', () => {
    const wrapper = mount(NtTrustBar, {
      props: { bar: { ...fullBar, barsTotal: null, costModel: '', feeBps: null, datasetHash: '' } },
    })
    expect(slot(wrapper, 'barsTotal').find('.nt-missing-inline').text()).toBe('没取到')
    expect(slot(wrapper, 'costModel').find('.nt-missing-inline').text()).toBe('未声明')
    expect(slot(wrapper, 'feeBps').find('.nt-missing-inline').text()).toBe('未声明')
    expect(slot(wrapper, 'datasetHash').find('.nt-missing-inline').text()).toBe('未声明')
    // 有值槽不受影响
    expect(slot(wrapper, 'tradingDays').find('.nt-trust__value').text()).toBe('100')
  })

  it('bar 整包缺省 → 12 槽全缺位、零绿勾（缺步灰显）', () => {
    const wrapper = mount(NtTrustBar, { props: { bar: null } })
    expect(wrapper.findAll('.nt-missing-inline')).toHaveLength(12)
    expect(wrapper.findAll('.nt-trust__pass')).toHaveLength(0)
    expect(wrapper.findAll('.nt-trust__value')).toHaveLength(0)
  })
})
