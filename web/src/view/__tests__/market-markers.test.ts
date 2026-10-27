/**
 * 买卖标记双色制钉向测试：买→up、卖→down、未知→text3 一字锁定。
 * 改了映射就是改了全站买卖表达口径，必须连本测试一起过评审。
 * W6b 接线批次：aggregateKlineMarkers 采样抽稀 + withFillsCostLine 成本线入 cost 槽。
 */
import { describe, expect, it } from 'vitest'
import { aggregateKlineMarkers, fillSideTone, KLINE_MARKER_CAP, withFillsCostLine } from '../market-markers'
import type { KlineMarker } from '@/viz/NtKline.vue'

describe('买卖标记双色制（红买绿卖，一字锁定）', () => {
  it('买→up 红', () => {
    expect(fillSideTone('buy')).toBe('up')
  })

  it('卖→down 绿', () => {
    expect(fillSideTone('sell')).toBe('down')
  })

  it('方向未知（空串/undefined/其他值）→ text3 中性，不默认红', () => {
    expect(fillSideTone('')).toBe('text3')
    expect(fillSideTone(undefined)).toBe('text3')
    expect(fillSideTone(null)).toBe('text3')
    expect(fillSideTone('hold')).toBe('text3')
  })
})

describe('aggregateKlineMarkers（W6b 密集成交采样）', () => {
  const m = (time: number | string, shape: KlineMarker['shape'], color = 'x'): KlineMarker => ({ time, shape, color })

  it('条数 ≤ cap 原样返回', () => {
    const rows = [m(1, 'arrowUp'), m(2, 'arrowDown'), m(3, 'circle')]
    expect(aggregateKlineMarkers(rows)).toEqual(rows)
  })

  it('超出 cap 的成交箭头按桶抽稀（每桶每 side 最早一条），首末恒留', () => {
    // 200 笔成交（买卖交替）+ 5 条消息，远超 cap。
    const fills: KlineMarker[] = Array.from({ length: 200 }, (_, i) =>
      m(1000 + i, i % 2 ? 'arrowDown' : 'arrowUp', 'c'),
    )
    const news = [m(1500, 'circle'), m(1600, 'circle')]
    const rows = [...fills, ...news]
    const out = aggregateKlineMarkers(rows)
    // 语义上限=aggregateMarkers 采样产出（cap=80 桶内每桶每 side 一条 + 首末恒留）+ 消息原样随行。
    // 200 条 fills 与 2 条 news，理论最坏=80*2+2+2=164；恰上确界不算失败。
    expect(out.length).toBeLessThanOrEqual(KLINE_MARKER_CAP * 2 + 4)
    expect(out.length).toBeGreaterThan(0)
    // 首末成交恒留。
    const first = fills[0]!
    const last = fills[fills.length - 1]!
    expect(out.some((r) => r.time === first.time && r.shape === first.shape)).toBe(true)
    expect(out.some((r) => r.time === last.time && r.shape === last.shape)).toBe(true)
    // 消息（circle）零抽稀全数保留。
    expect(out.filter((r) => r.shape === 'circle')).toHaveLength(2)
    // 输出按 time 升序（epoch 概判：本用例 time 即 epoch 数值）。
    for (let i = 1; i < out.length; i++) expect(Number(out[i]!.time)).toBeGreaterThanOrEqual(Number(out[i - 1]!.time))
  })

  it('RFC3339 时间串不坍缩：桶索引 = Date.parse/1000，128 笔 UTC+8 填充按升序分桶成败各半', () => {
    // 后端 desk.go:99 用 RFC3339——这是真实证法路径；如果错误用 Number()，所有 time 都 NaN 桶。
    const iso = (s: string) => `2026-10-28T09:${s}:00+08:00`
    const fills: KlineMarker[] = Array.from({ length: 128 }, (_, i) =>
      m(iso(String(i).padStart(2, '0')), i % 2 ? 'arrowDown' : 'arrowUp'),
    )
    const out = aggregateKlineMarkers(fills)
    // 不超 cap*2+2（首末）——不得坍缩到 4 条。
    expect(out.length).toBeLessThanOrEqual(KLINE_MARKER_CAP * 2 + 2)
    expect(out.length).toBeGreaterThan(10)
    // 首末恒留：原 time 形态（RFC3339 串）不被覆写。
    expect(out.some((r) => r.time === iso('00'))).toBe(true)
    expect(out.some((r) => r.time === iso('127'))).toBe(true)
  })

  it('逆序输入不静默放过：按 epoch 升序排序后再分桶', () => {
    // 服务端 /fills 供 Desc created_at——最新在前；采样必须重排，否则桶索引恶化成全保留。
    // 200 笔逆序分布在 10 秒窗口（每 side 100 笔）：未排序时 aggregateMarkers 恒产全保留，
    // 排序后采样率显著下降（本用例 200 → ≤ cap*2+2+其它）。
    const fills: KlineMarker[] = Array.from({ length: 200 }, (_, i) =>
      m(4851 + Math.floor(i / 20), i % 2 ? 'arrowDown' : 'arrowUp'),
    ).reverse()
    const out = aggregateKlineMarkers(fills)
    // 采样显著收敛（远小于输入 200）。
    expect(out.length).toBeLessThan(50)
    // 真实首末（时间上）恒在：最早=4851，最新=4860。
    expect(out.some((r) => r.time === 4851)).toBe(true)
    expect(out.some((r) => r.time === 4860)).toBe(true)
  })

  it('时间缺（invalid/NaN）的 marker 不参与采样、不丢失，原样接过', () => {
    const valid: KlineMarker[] = Array.from({ length: 150 }, (_, i) => m(1000 + i, i % 2 ? 'arrowDown' : 'arrowUp'))
    const unknownTimes: KlineMarker[] = [m('not-a-date', 'arrowUp'), m(NaN, 'arrowDown'), m('', 'arrowUp')]
    const out = aggregateKlineMarkers([...valid, ...unknownTimes])
    // 三笔时间残缺的 arrow marker 不进入采样（无 epoch），但必须原样保留，不被静默丢。
    expect(out.filter((r) => r.time === 'not-a-date' || r.time === '' || Number.isNaN(r.time as number))).toHaveLength(3)
  })

  it('shape=square（合法但非 buy/sell 词义）不被采样吞掉，全数保留', () => {
    const dense: KlineMarker[] = Array.from({ length: 150 }, (_, i) => m(1000 + i, i % 2 ? 'arrowDown' : 'arrowUp'))
    const squares = [m(1333, 'square'), m(2040, 'square')]
    const out = aggregateKlineMarkers([...dense, ...squares])
    const returnedSquares = out.filter((r) => r.shape === 'square')
    expect(returnedSquares).toHaveLength(2)
  })

  it('空输入返回空', () => {
    expect(aggregateKlineMarkers([])).toEqual([])
  })
})

describe('withFillsCostLine（W6b 摊薄成本入 cost 槽）', () => {
  const f = (sym: string, side: string, qty: number | null, price: number | null, amount: number | null = null) => ({ symbol: sym, side, qty, price, amount })

  it('无持仓（无对应 fills）→ cost=null，levels 原样', () => {
    const levels = [{ price: 100, title: '止损' }]
    const r = withFillsCostLine(levels, [], '600519.SH')
    expect(r.cost).toBeNull()
    expect(r.levels).toEqual(levels)
  })

  it('净持仓>0 → cost={price:摊薄价,title:"摊薄成本"}，levels 由 withCostLine 排在首', () => {
    // 买 100 股 @10（1000 元）+ 买 100 股 @12（1200 元）= 200 股共 2200 → 摊薄 11
    const fills = [f('600519.SH', 'buy', 100, 10), f('600519.SH', 'buy', 100, 12)]
    const r = withFillsCostLine([], fills, '600519.SH')
    expect(r.cost).toEqual({ price: 11, title: '摊薄成本' })
    expect(r.levels).toEqual([{ price: 11, title: '摊薄成本', lineStyle: 'solid', color: undefined }])
  })

  it('净额 ≤0（卖出全额回笼）→ 不给 cost（防 0/负数成本伪造）', () => {
    const fills = [f('600519.SH', 'buy', 100, 10), f('600519.SH', 'sell', 100, 15)]
    const r = withFillsCostLine([], fills, '600519.SH')
    expect(r.cost).toBeNull()
  })

  it('非法数据（qty null/0、price null）跳过', () => {
    const fills = [f('600519.SH', 'buy', null, 10), f('600519.SH', 'buy', 0, 10), f('600519.SH', 'buy', 100, null)]
    const r = withFillsCostLine([], fills, '600519.SH')
    expect(r.cost).toBeNull()
  })

  it('code 为空 → 不算成本', () => {
    const fills = [f('600519.SH', 'buy', 100, 10)]
    const r = withFillsCostLine([], fills, '')
    expect(r.cost).toBeNull()
  })

  it('未知方向（side 非 buy/sell）跳过不计，不伪造空方', () => {
    // side=''/'hold'/未知 → 不进入成本账；只 buy 100 @10 入帐 → 摊薄 10
    const fills = [
      f('600519.SH', 'buy', 100, 10),
      f('600519.SH', '', 50, 10),
      f('600519.SH', 'hold', 50, 10),
      f('600519.SH', 'anything', 100, 20),
    ]
    const r = withFillsCostLine([], fills, '600519.SH')
    expect(r.cost).toEqual({ price: 10, title: '摊薄成本' })
  })

  it('未知方向纯集合（无 buy/sell）→ 不算成本不伪造 0', () => {
    const fills = [f('600519.SH', '', 100, 10), f('600519.SH', 'hold', 50, 12)]
    const r = withFillsCostLine([], fills, '600519.SH')
    expect(r.cost).toBeNull()
  })
})
