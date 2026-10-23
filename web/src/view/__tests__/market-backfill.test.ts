import { describe, expect, it } from 'vitest'
import type { Bar } from '@/api/types/market'
import { BACKFILL_LIMIT, cmpTime, mergeGapRows } from '../market-backfill'

function bar(time: string, close = 10): Bar {
  return { time, open: close, high: close, low: close, close, volume: 100, amount: 1000 }
}

describe('cmpTime', () => {
  it('双方可解析按毫秒数值比', () => {
    expect(cmpTime('2026-10-19T09:30:00+08:00', '2026-10-19T09:31:00+08:00')).toBe(-1)
    expect(cmpTime('2026-10-19T09:31:00+08:00', '2026-10-19T09:30:00+08:00')).toBe(1)
    expect(cmpTime('2026-10-19T09:30:00+08:00', '2026-10-19T01:30:00Z')).toBe(0)
  })

  it('回退字符串比较：双方均不可解析时按字典序定先后', () => {
    expect(cmpTime('bad-time', 'bad-time')).toBe(0)
    expect(cmpTime('bad-a', 'bad-b')).toBe(-1)
    expect(cmpTime('bad-b', 'bad-a')).toBe(1)
  })

  it('一侧不可解析：可解析侧按字典序参与（与 applyLiveBar 同口径）', () => {
    expect(cmpTime('2026-10-20', 'zzzz')).toBe(-1)
    expect(cmpTime('zzzz', '2026-10-20')).toBe(1)
  })
})

describe('mergeGapRows', () => {
  it('空 fetched：无需合并，视为已覆盖', () => {
    const existing = [bar('2026-10-19T09:30:00+08:00')]
    const merged = mergeGapRows(existing, [])
    expect(merged.covered).toBe(true)
    expect(merged.applied).toEqual([])
    expect(existing).toHaveLength(1)
  })

  it('标准窗口：首行同根替换末根，其后依次追加，applied 保持升序', () => {
    const existing = [
      bar('2026-10-19T09:30:00+08:00', 10),
      bar('2026-10-19T09:31:00+08:00', 11),
    ]
    const fetched = [
      bar('2026-10-19T09:31:00+08:00', 11.5),
      bar('2026-10-19T09:32:00+08:00', 12),
      bar('2026-10-19T09:33:00+08:00', 12.5),
    ]
    const merged = mergeGapRows(existing, fetched)
    expect(merged.covered).toBe(true)
    expect(merged.replaced).toBe(1)
    expect(merged.appended).toBe(2)
    expect(merged.applied.map((b) => b.time)).toEqual([
      '2026-10-19T09:31:00+08:00',
      '2026-10-19T09:32:00+08:00',
      '2026-10-19T09:33:00+08:00',
    ])
    expect(existing.map((b) => b.time)).toEqual([
      '2026-10-19T09:30:00+08:00',
      '2026-10-19T09:31:00+08:00',
      '2026-10-19T09:32:00+08:00',
      '2026-10-19T09:33:00+08:00',
    ])
    expect(existing[1].close).toBe(11.5)
  })

  it('够不到末根（缺口宽于回填上限）：covered=false 且不动 existing', () => {
    const existing = [bar('2026-10-19T09:30:00+08:00')]
    const fetched = [bar('2026-10-19T11:29:00+08:00'), bar('2026-10-19T11:30:00+08:00')]
    const merged = mergeGapRows(existing, fetched)
    expect(merged.covered).toBe(false)
    expect(merged.applied).toEqual([])
    expect(existing).toHaveLength(1)
  })

  it('fetched 乱序输入：先排序再合并，早于末根的行跳过不插入', () => {
    const existing = [bar('2026-10-19T09:30:00+08:00'), bar('2026-10-19T09:31:00+08:00')]
    const fetched = [
      bar('2026-10-19T09:32:00+08:00', 12),
      bar('2026-10-19T09:29:00+08:00', 9.9),
      bar('2026-10-19T09:31:00+08:00', 11.5),
    ]
    const merged = mergeGapRows(existing, fetched)
    expect(merged.covered).toBe(true)
    expect(merged.replaced).toBe(1)
    expect(merged.appended).toBe(1)
    expect(existing.map((b) => b.time)).toEqual([
      '2026-10-19T09:30:00+08:00',
      '2026-10-19T09:31:00+08:00',
      '2026-10-19T09:32:00+08:00',
    ])
  })

  it('fetched 全部缺时间：视为无需合并', () => {
    const existing = [bar('2026-10-19T09:30:00+08:00')]
    const merged = mergeGapRows(existing, [{ ...bar('', 1) }])
    expect(merged.covered).toBe(true)
    expect(merged.applied).toEqual([])
    expect(existing).toHaveLength(1)
  })

  it('日线口径（YYYY-MM-DD）同根相等，正确替换', () => {
    const existing = [bar('2026-10-16', 20), bar('2026-10-19', 21)]
    const merged = mergeGapRows(existing, [bar('2026-10-19', 21.5)])
    expect(merged.covered).toBe(true)
    expect(merged.replaced).toBe(1)
    expect(existing[1].close).toBe(21.5)
  })

  it('existing 为空：不覆盖，交给整载兜底', () => {
    const merged = mergeGapRows([], [bar('2026-10-19T09:30:00+08:00')])
    expect(merged.covered).toBe(false)
  })

  it('合并后超过 cap：overflow=true 且 existing 保持原样（由调用方整载对齐）', () => {
    const existing = [bar('2026-10-19T09:30:00+08:00'), bar('2026-10-19T09:31:00+08:00')]
    const fetched = [
      bar('2026-10-19T09:31:00+08:00'),
      bar('2026-10-19T09:32:00+08:00'),
      bar('2026-10-19T09:33:00+08:00'),
    ]
    const merged = mergeGapRows(existing, fetched, 3)
    expect(merged.covered).toBe(true)
    expect(merged.overflow).toBe(true)
    expect(existing).toHaveLength(2)
    expect(existing[1].close).toBe(10)
  })

  it('BACKFILL_LIMIT 默认与视图 LIVE_CAP 对齐（800）', () => {
    expect(BACKFILL_LIMIT).toBe(800)
  })
})

describe('mergeGapRows day 粒度（1d）', () => {
  it('同日混合尾根：1m 污染的 RFC3339 末根与当日日线行一并换掉（finding #1 repro）', () => {
    const existing = [
      bar('2026-10-16', 20),
      bar('2026-10-19', 21),
      bar('2026-10-19T14:59:00+08:00', 21.3),
    ]
    const fetched = [bar('2026-10-19', 21.8), bar('2026-10-20', 22)]
    const merged = mergeGapRows(existing, fetched, 800, 'day')
    expect(merged.covered).toBe(true)
    expect(merged.overflow).toBe(false)
    expect(merged.replaced).toBe(2)
    expect(merged.appended).toBe(1)
    expect(existing.map((b) => b.time)).toEqual(['2026-10-16', '2026-10-19', '2026-10-20'])
    expect(existing[1].close).toBe(21.8)
    expect(existing[2].close).toBe(22)
    expect(merged.applied.map((b) => b.time)).toEqual(['2026-10-19', '2026-10-20'])
  })

  it('干净日线尾根：同日行替换、更晚日追加', () => {
    const existing = [bar('2026-10-17', 20), bar('2026-10-20', 21)]
    const fetched = [bar('2026-10-20', 21.5), bar('2026-10-21', 22)]
    const merged = mergeGapRows(existing, fetched, 800, 'day')
    expect(merged.covered).toBe(true)
    expect(merged.replaced).toBe(1)
    expect(merged.appended).toBe(1)
    expect(existing.map((b) => b.time)).toEqual(['2026-10-17', '2026-10-20', '2026-10-21'])
    expect(existing[1].close).toBe(21.5)
  })

  it('窗口首日超过末根日：covered=false 且不动 existing', () => {
    const existing = [bar('2026-10-17'), bar('2026-10-20')]
    const merged = mergeGapRows(existing, [bar('2026-10-21'), bar('2026-10-22')], 800, 'day')
    expect(merged.covered).toBe(false)
    expect(existing).toHaveLength(2)
  })

  it('末根时间串不可解析：回退 ms 口径，同根比较不崩', () => {
    const existing = [bar('2026-10-17', 20), bar('bad-time', 99)]
    const fetched = [bar('2026-10-20', 21)]
    const merged = mergeGapRows(existing, fetched, 800, 'day')
    // dayKey(null) 触发 ms 兜底：cmpTime('2026-10-20','bad-time') 字典序 fetched 更晚 → covered=false
    expect(merged.covered).toBe(false)
    expect(existing).toHaveLength(2)
  })
})
