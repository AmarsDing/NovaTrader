/**
 * 板块热度双列榜契约：领涨/领跌双列渲染、均涨幅红涨绿跌 class、
 * 点击行 emit select(sector)、空列显提示、ready=false 显 missing。
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import type { SectorRow } from '@/api/market'
import NtSectorRank from '@/display/NtSectorRank.vue'

function sector(code: string, name: string, avgPct: number | null, leader = ''): SectorRow {
  return { code, name, rank: null, heat: null, avgPct, overseas: null, leader, upLimitCount: null, advanceRatio: null, amount: null, memberCount: 120, leaderReason: '', asOf: '' }
}

const GAINERS = [sector('BK001', '半导体', 2.1, '688981'), sector('BK002', '新能源车', 1.2, '300750')]
const LOSERS = [sector('BK003', '房地产', -1.4, '600048')]

beforeEach(() => setActivePinia(createPinia()))

describe('NtSectorRank', () => {
  it('双列渲染：序号 / 板块名 / N 只·龙头 / 均涨幅 class', () => {
    const wrapper = mount(NtSectorRank, { props: { gainers: GAINERS, losers: LOSERS } })
    const cols = wrapper.findAll('.srank__col')
    expect(cols).toHaveLength(2)
    expect(cols[0]!.find('.srank__head').classes()).toContain('up')
    expect(cols[1]!.find('.srank__head').classes()).toContain('down')

    const rows = cols[0]!.findAll('.srank__row')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('半导体')
    expect(rows[0]!.text()).toContain('120 只')
    expect(rows[0]!.text()).toContain('龙头')
    expect(rows[0]!.find('.srank__pct').classes()).toContain('up')
    expect(rows[0]!.find('.srank__pct').text()).toBe('2.10%')
    expect(cols[1]!.find('.srank__pct').classes()).toContain('down')
    expect(cols[1]!.find('.srank__pct').text()).toBe('-1.40%')
  })

  it('点击行 emit select(sector)', async () => {
    const wrapper = mount(NtSectorRank, { props: { gainers: GAINERS, losers: [] } })
    await wrapper.findAll('.srank__row')[1]!.trigger('click')
    const events = wrapper.emitted('select')
    expect(events).toHaveLength(1)
    expect((events![0] as SectorRow[])[0]!.code).toBe('BK002')
  })

  it('无龙头行 disabled 且点击不 emit（行内挡住死点击）', async () => {
    const wrapper = mount(NtSectorRank, { props: { gainers: [sector('BK009', '医药', 0.5)], losers: [] } })
    const row = wrapper.find('.srank__row')
    expect(row.attributes('disabled')).toBeDefined()
    await row.trigger('click')
    expect(wrapper.emitted('select')).toBeUndefined()
  })

  it('空列显「暂无」提示；ready=false 显 missing', () => {
    const wrapper = mount(NtSectorRank, { props: { gainers: [], losers: LOSERS } })
    expect(wrapper.text()).toContain('暂无上涨板块')
    const notReady = mount(NtSectorRank, { props: { gainers: [], losers: [], ready: false } })
    expect(notReady.text()).toContain('板块快照未返回')
  })
})
