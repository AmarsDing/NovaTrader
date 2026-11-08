/**
 * 盘中异动流契约：kind 徽标文案/tone 只走 statusOf('move.*') 册（封板=up/急拉=warn/开板=neutral）、
 * 行左色条 class、aria-live=polite、点击 emit select；missing / empty 双空态分流。
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import type { IntradayAlert } from '@/api/market'
import NtAlertFeed from '@/display/NtAlertFeed.vue'
import NtBadge from '@/ui/NtBadge.vue'

const ALERTS: IntradayAlert[] = [
  { symbol: '600519', name: '贵州茅台', kind: 'limit_up', time: '2026-11-08T10:01:00+08:00', pct: 10, price: 1800.5 },
  { symbol: '300750', name: '宁德时代', kind: 'surge', time: '2026-11-08T10:03:00+08:00', pct: 2.1, price: 180.2 },
  { symbol: '000001', name: '平安银行', kind: 'break', time: '2026-11-08T10:05:00+08:00', pct: null, price: null },
]

beforeEach(() => setActivePinia(createPinia()))

describe('NtAlertFeed', () => {
  it('徽标文案/色条 class 按 statusOf 册；幅度 class 红涨绿跌；pct null 显 —', () => {
    const wrapper = mount(NtAlertFeed, { props: { alerts: ALERTS } })
    const list = wrapper.find('ol.afeed')
    expect(list.attributes('aria-live')).toBe('polite')

    const rows = wrapper.findAll('.afeed__row')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.classes()).toContain('afeed__row--up')
    expect(rows[1]!.classes()).toContain('afeed__row--warn')
    expect(rows[2]!.classes()).toContain('afeed__row--flat')

    const badges = wrapper.findAllComponents(NtBadge)
    expect(badges.map((b) => b.text())).toEqual(['封板', '急拉', '开板'])
    expect(badges[0]!.props('tone')).toBe('up')
    expect(badges[1]!.props('tone')).toBe('warn')
    expect(badges[2]!.props('tone')).toBe('neutral')

    expect(rows[0]!.find('.afeed__pct').classes()).toContain('up')
    expect(rows[0]!.find('.afeed__pct').text()).toBe('+10.00%')
    expect(rows[2]!.find('.afeed__pct').text()).toBe('—')
    expect(rows[0]!.find('.afeed__time').text()).toContain('10:01')
  })

  it('点击行 emit select(alert)', async () => {
    const wrapper = mount(NtAlertFeed, { props: { alerts: ALERTS } })
    await wrapper.findAll('.afeed__row')[1]!.trigger('click')
    const events = wrapper.emitted('select')
    expect(events).toHaveLength(1)
    expect((events![0] as IntradayAlert[])[0]!.symbol).toBe('300750')
  })

  it('册外 kind 兜底：原样回显 + neutral tone + 无色条，不炸渲染', () => {
    const odd: IntradayAlert = { symbol: '600000', name: '浦发银行', kind: 'limit_down' as IntradayAlert['kind'], time: '2026-11-08T10:07:00+08:00', pct: -10, price: 9.1 }
    const wrapper = mount(NtAlertFeed, { props: { alerts: [odd] } })
    const row = wrapper.find('.afeed__row')
    expect(row.exists()).toBe(true)
    expect(row.classes()).toContain('afeed__row--flat')
    const badge = wrapper.getComponent(NtBadge)
    expect(badge.text()).toBe('limit_down')
    expect(badge.props('tone')).toBe('neutral')
  })

  it('ready=false 显 missing；空列表显「盘中暂无异动」', () => {
    const notReady = mount(NtAlertFeed, { props: { alerts: [], ready: false } })
    expect(notReady.text()).toContain('异动流水未返回')
    const empty = mount(NtAlertFeed, { props: { alerts: [] } })
    expect(empty.text()).toContain('盘中暂无异动')
  })
})
