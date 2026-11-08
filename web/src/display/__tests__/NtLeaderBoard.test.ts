/**
 * 龙头榜契约：板块 / 龙头 / 理由 / 均涨幅四段渲染、理由空串显 —、
 * 点击行 emit select(龙头代码)、ready=false 显 missing、空榜显「暂无龙头数据」。
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import NtLeaderBoard, { type LeaderRow } from '@/display/NtLeaderBoard.vue'

const ROWS: LeaderRow[] = [
  { sectorCode: 'BK001', sectorName: '半导体', avgPct: 2.1, leader: '688981', leaderReason: '中芯国际放量领涨' },
  { sectorCode: 'BK003', sectorName: '房地产', avgPct: -1.4, leader: '600048', leaderReason: '' },
]

beforeEach(() => setActivePinia(createPinia()))

describe('NtLeaderBoard', () => {
  it('四段渲染：名次 / 板块 / 理由 / 均涨幅 class；理由空串显 —', () => {
    const wrapper = mount(NtLeaderBoard, { props: { rows: ROWS } })
    const rows = wrapper.findAll('.lboard__row')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.find('.lboard__rank').text()).toBe('1')
    expect(rows[0]!.find('.lboard__sector').text()).toBe('半导体')
    expect(rows[0]!.find('.lboard__reason').text()).toBe('中芯国际放量领涨')
    expect(rows[0]!.find('.lboard__pct').classes()).toContain('up')
    expect(rows[0]!.find('.lboard__pct').text()).toBe('2.10%')
    expect(rows[1]!.find('.lboard__reason').text()).toBe('—')
    expect(rows[1]!.find('.lboard__pct').classes()).toContain('down')
  })

  it('点击行 emit select(龙头代码)', async () => {
    const wrapper = mount(NtLeaderBoard, { props: { rows: ROWS } })
    await wrapper.findAll('.lboard__row')[0]!.trigger('click')
    const events = wrapper.emitted('select')
    expect(events).toHaveLength(1)
    expect(events![0]).toEqual(['688981'])
  })

  it('ready=false 显 missing；空榜显「暂无龙头数据」', () => {
    const notReady = mount(NtLeaderBoard, { props: { rows: [], ready: false } })
    expect(notReady.text()).toContain('板块快照未返回')
    const empty = mount(NtLeaderBoard, { props: { rows: [] } })
    expect(empty.text()).toContain('暂无龙头数据')
  })
})
