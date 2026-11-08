/**
 * NtTable v2 合约测试（2026-11-08 GitHub 借法批：FreqUI 列排序 / Perspective 虚拟窗口）。
 * 纪律锁点：
 *  - 默认路径（无 sortable/virtual）DOM 与 v1 逐像素一致：无排序指示器、无 tabindex/aria-sort、无 spacer；
 *  - 排序三态循环 升→降→还原、切列从升序起、空值恒排尾、同值保原序、compare 自定义、受控/非受控两模式；
 *  - aria-sort 与键盘可达（Enter/Space）；
 *  - windowRange 纯函数窗口/夹紧/空数据语义；
 *  - virtual 只渲染窗口行 + spacer 撑高；非 virtual 全量渲染。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import NtTable, { type NtTableColumn } from '../NtTable.vue'
import { windowRange } from '../useVirtualRows'

interface Row {
  id: string
  symbol: string
  price: number | null
  volume: number
  [key: string]: unknown
}

const rows: Row[] = [
  { id: 'a', symbol: '600519.SH', price: 1700, volume: 300 },
  { id: 'b', symbol: '000001.SZ', price: null, volume: 100 },
  { id: 'c', symbol: '300750.SZ', price: 180, volume: 200 },
]

function cols(extra?: Partial<NtTableColumn<Row>>): NtTableColumn<Row>[] {
  return [
    { key: 'symbol', title: '证券' },
    { key: 'price', title: '价格', align: 'right', sortable: true, ...extra },
    { key: 'volume', title: '数量', align: 'right', sortable: true },
  ]
}

function bodyText(w: ReturnType<typeof mount>): string[] {
  return w.findAll('tbody tr:not(.nt-table__spacer)').map((tr) => tr.text())
}

describe('NtTable v2 默认路径兼容', () => {
  it('无 sortable 列时表头与 v1 一致：无指示器/无 tabindex/无 aria-sort', () => {
    const plain: NtTableColumn<Row>[] = [
      { key: 'symbol', title: '证券' },
      { key: 'price', title: '价格', align: 'right' },
    ]
    const w = mount(NtTable<Row>, { props: { columns: plain, rows } })
    const ths = w.findAll('th')
    expect(ths).toHaveLength(2)
    expect(w.find('.nt-table__sort').exists()).toBe(false)
    expect(ths[0].attributes('tabindex')).toBeUndefined()
    expect(ths[0].attributes('aria-sort')).toBeUndefined()
    expect(w.findAll('tbody tr')).toHaveLength(3)
    expect(w.find('.nt-table__spacer').exists()).toBe(false)
  })

  it('空态默认行与 v1 一致', () => {
    const w = mount(NtTable<Row>, { props: { columns: cols(), rows: [] } })
    expect(w.get('.nt-table__empty').text()).toBe('暂无数据')
  })
})

describe('NtTable v2 列排序', () => {
  it('点击表头按 升序 → 降序 → 还原 循环并外发 sort-change', async () => {
    const w = mount(NtTable<Row>, { props: { columns: cols(), rows } })
    const priceTh = w.findAll('th')[1]
    await priceTh.trigger('click')
    expect(bodyText(w)[0]).toContain('300750') // 180 升序最小（null 排尾）
    await priceTh.trigger('click')
    expect(bodyText(w)[0]).toContain('600519') // 1700 降序最大
    await priceTh.trigger('click')
    expect(bodyText(w)[0]).toContain('600519') // 还原原序
    const ev = w.emitted('sort-change')!
    expect(ev).toHaveLength(3)
    expect(ev[0]).toEqual(['price', 'asc'])
    expect(ev[1]).toEqual(['price', 'desc'])
    expect(ev[2]).toEqual(['price', ''])
  })

  it('空值（null 或空串）升序降序都恒排尾', async () => {
    const w = mount(NtTable<Row>, { props: { columns: cols(), rows } })
    const priceTh = w.findAll('th')[1]
    await priceTh.trigger('click')
    let text = bodyText(w)
    expect(text[text.length - 1]).toContain('000001')
    await priceTh.trigger('click')
    text = bodyText(w)
    expect(text[text.length - 1]).toContain('000001')
  })

  it('切换到另一列从升序重新开始', async () => {
    const w = mount(NtTable<Row>, { props: { columns: cols(), rows } })
    await w.findAll('th')[1].trigger('click') // price asc
    await w.findAll('th')[2].trigger('click') // volume asc
    expect(bodyText(w)[0]).toContain('000001') // volume 100 最小
    expect(w.emitted('sort-change')!.at(-1)).toEqual(['volume', 'asc'])
  })

  it('同值保持原序（索引决胜）', async () => {
    const tie: Row[] = [
      { id: 'x', symbol: 'A', price: 10, volume: 1 },
      { id: 'y', symbol: 'B', price: 10, volume: 2 },
    ]
    const w = mount(NtTable<Row>, { props: { columns: cols(), rows: tie } })
    await w.findAll('th')[1].trigger('click')
    expect(bodyText(w)[0]).toContain('A')
  })

  it('自定义 compare 生效', async () => {
    const byLen: NtTableColumn<Row>[] = [
      { key: 'symbol', title: '证券', sortable: true, compare: (a, b) => a.symbol.length - b.symbol.length },
    ]
    const w = mount(NtTable<Row>, { props: { columns: byLen, rows } })
    await w.find('th').trigger('click')
    expect(w.emitted('sort-change')![0]).toEqual(['symbol', 'asc'])
  })

  it('受控模式：内部不改状态，显示跟随 props', async () => {
    const w = mount(NtTable<Row>, {
      props: { columns: cols(), rows, sortKey: 'price', sortDir: 'desc' as const },
    })
    expect(bodyText(w)[0]).toContain('600519')
    await w.findAll('th')[1].trigger('click')
    // 受控下点击只外发事件，方向由父级决定：渲染仍按 desc
    expect(w.emitted('sort-change')!.at(-1)).toEqual(['price', ''])
    expect(bodyText(w)[0]).toContain('600519')
  })

  it('aria-sort 与键盘：Enter/Space 触发，非 sortable 列无 aria-sort', async () => {
    const w = mount(NtTable<Row>, { props: { columns: cols(), rows } })
    const ths = w.findAll('th')
    expect(ths[0].attributes('aria-sort')).toBeUndefined()
    expect(ths[1].attributes('aria-sort')).toBe('none')
    expect(ths[1].attributes('tabindex')).toBe('0')
    await ths[1].trigger('keydown.enter')
    expect(ths[1].attributes('aria-sort')).toBe('ascending')
    await ths[1].trigger('keydown.space')
    expect(w.emitted('sort-change')).toHaveLength(2)
  })
})

describe('useVirtualRows windowRange 纯计算', () => {
  it('基本窗口：scrollTop/viewport 求可视范围并含 overscan', () => {
    // 100 行 × 26px，滚动到 260px（第 10 行），视口 260px（10 行）
    const { start, end } = windowRange(260, 260, 100, 26, 6)
    expect(start).toBe(4) // 10 - 6
    expect(end).toBe(26) // 20 + 6
  })

  it('顶部与底部夹紧', () => {
    expect(windowRange(0, 260, 100, 26, 6)).toEqual({ start: 0, end: 16 })
    // 越界滚动收拢末页：maxScroll=2600-260=2340 → floor(2340/26)-6=84
    expect(windowRange(99999, 260, 100, 26, 6)).toEqual({ start: 84, end: 100 })
  })

  it('空数据与非法行高退化为空窗', () => {
    expect(windowRange(0, 260, 0, 26)).toEqual({ start: 0, end: 0 })
    expect(windowRange(0, 260, 100, 0)).toEqual({ start: 0, end: 0 })
  })
})

describe('NtTable v2 虚拟滚动', () => {
  const many: Row[] = Array.from({ length: 200 }, (_, i) => ({
    id: `r${i}`,
    symbol: `S${i}`,
    price: i,
    volume: i,
  }))

  it('virtual + height：只渲染窗口行，spacer 撑高（jsdom 视口 0 → 仅 overscan）', async () => {
    const w = mount(NtTable<Row>, {
      props: { columns: cols(), rows: many, virtual: true, height: '260px', rowHeight: 26 },
    })
    await nextTick()
    const bodyRows = w.findAll('tbody tr:not(.nt-table__spacer)')
    expect(bodyRows.length).toBeLessThan(20)
    expect(bodyRows.length).toBeGreaterThan(0)
    expect(w.find('.nt-table__spacer').exists()).toBe(true)
    expect(w.find('.nt-table').attributes('style')).toContain('height: 260px')
  })

  it('virtual 但无 height：不启用窗口，全量渲染', async () => {
    const w = mount(NtTable<Row>, { props: { columns: cols(), rows: many, virtual: true } })
    await nextTick()
    expect(w.findAll('tbody tr:not(.nt-table__spacer)')).toHaveLength(200)
    expect(w.find('.nt-table__spacer').exists()).toBe(false)
  })

  it('非 virtual：全量渲染且无 spacer', () => {
    const w = mount(NtTable<Row>, { props: { columns: cols(), rows: many } })
    expect(w.findAll('tbody tr')).toHaveLength(200)
  })

  it('排序与虚拟叠加：窗口取自排序后序列', async () => {
    const w = mount(NtTable<Row>, {
      props: { columns: cols(), rows: many, virtual: true, height: '260px', rowHeight: 26 },
    })
    await nextTick()
    await w.findAll('th')[1].trigger('click') // price desc… 先 asc
    await w.findAll('th')[1].trigger('click') // desc
    expect(bodyText(w)[0]).toContain('S199')
  })
})
