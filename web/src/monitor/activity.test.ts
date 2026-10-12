import { describe, expect, it } from 'vitest'
import { ACTIVITY_CELLS, ActivityLog, demoActivityCells } from './activity'

const T0 = Date.parse('2026-10-12T10:00:00')

function sec(base: number, offset: number): number {
  return base + offset * 1000
}

describe('ActivityLog.observe', () => {
  it('第一次观测只建档，不算活动', () => {
    const log = new ActivityLog()
    expect(log.observe('a', 'v1', T0)).toBe(false)
    expect(log.darkFor('a', T0)).toBe(0)
    expect(log.observedFor('a', sec(T0, 30))).toBe(30)
  })

  it('签名变化点亮当前秒', () => {
    const log = new ActivityLog()
    log.observe('a', 'v1', T0)
    expect(log.observe('a', 'v2', sec(T0, 5))).toBe(true)
    const cells = log.cells('a', sec(T0, 59))
    expect(cells).toHaveLength(ACTIVITY_CELLS)
    /* 点亮的是 T0+5 那一格，其余没动过的是静默 */
    expect(cells[5]).toBe(true)
    expect(cells[6]).toBe(false)
  })

  it('签名没变就不算活动', () => {
    const log = new ActivityLog()
    log.observe('a', 'v1', T0)
    expect(log.observe('a', 'v1', sec(T0, 10))).toBe(false)
    expect(log.darkFor('a', sec(T0, 10))).toBe(10)
  })

  it('自带时间戳前进时把跨过的秒都点亮', () => {
    const log = new ActivityLog()
    log.observe('a', 'v1', T0, T0)
    log.observe('a', 'v1', sec(T0, 10), sec(T0, 8))
    const cells = log.cells('a', sec(T0, 10))
    /* T0+1s 到 T0+10s 之间的格子都应有活动（含最后一格） */
    expect(cells[59]).toBe(true)
    expect(cells[58]).toBe(true)
  })

  it('时间戳回退或不动不算活动', () => {
    const log = new ActivityLog()
    log.observe('a', 'v1', T0, T0)
    expect(log.observe('a', 'v1', sec(T0, 5), T0)).toBe(false)
    expect(log.observe('a', 'v1', sec(T0, 6), T0 - 1000)).toBe(false)
  })
})

describe('ActivityLog.cells', () => {
  it('窗口只有 60 格，更早的格子被裁掉', () => {
    const log = new ActivityLog()
    log.observe('a', 'v1', T0)
    log.observe('a', 'v2', T0)
    const cells = log.cells('a', sec(T0, 120))
    expect(cells).toHaveLength(ACTIVITY_CELLS)
    /* T0 的点亮早已滚出窗口 */
    expect(cells.every((cell) => cell !== true)).toBe(true)
  })

  it('建档之前的格子是 null（未知），不是静默', () => {
    const log = new ActivityLog()
    log.observe('a', 'v1', sec(T0, 30))
    const cells = log.cells('a', sec(T0, 59))
    expect(cells[0]).toBe(null)
    expect(cells[59]).toBe(false)
  })
})

describe('ActivityLog.hole', () => {
  it('暂停区间画成未知，静默时钟从 hole 结束重新起算', () => {
    const log = new ActivityLog()
    log.observe('a', 'v1', T0)
    log.hole(sec(T0, 10), sec(T0, 40))
    const cells = log.cells('a', sec(T0, 59))
    expect(cells[10]).toBe(null) /* T0+10 落在 hole 里 */
    expect(cells[45]).toBe(false) /* hole 之后、再次观测之前，是静默不是未知 */
    /* 暂停到 T0+40，T0+50 时只静默了 10 秒，而不是 50 秒 */
    expect(log.darkFor('a', sec(T0, 50))).toBe(10)
  })
})

describe('demoActivityCells', () => {
  it('返回 60 格且含 null（未知）段，供 gallery 静态示例用', () => {
    const cells = demoActivityCells()
    expect(cells).toHaveLength(ACTIVITY_CELLS)
    expect(cells.some((cell) => cell === null)).toBe(true)
    expect(cells.some((cell) => cell === true)).toBe(true)
  })
})
