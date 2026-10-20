/**
 * alert-levels（方案 §8 告警分级册）合同测试：
 * - levelOf 七键分档各一例 + critical 优先于 kind 白名单 + 默认 L1；
 * - causeKey：dedupKey 截断 / kind:symbol 回落 / 「系统」兜底；
 * - renderAlertCopy：五段齐全 + 字典命中/缺失 + 无 symbol 写「系统」；
 * - 聚合器：同窗累计 / 跨窗重置 / 异因不并；
 * - cooldownCheck：窗内抑制 / 边界等值放行 / 键隔离；
 * - quietHours：跨午夜 / 级别白名单 / L4 直通。
 */
import { describe, expect, it } from 'vitest'
import {
  causeKey,
  cooldownCheck,
  createCauseAggregator,
  levelOf,
  quietHours,
  renderAlertCopy,
  type AlertLevel,
} from '../alert-levels'

describe('levelOf 分级（§8.1）', () => {
  it.each([
    ['killswitch', 'L4'],
    ['breaker', 'L4'],
    ['reject_rate', 'L4'],
    ['order_unknown', 'L4'],
    ['alert-rule', 'L3'],
    ['surge', 'L3'],
    ['limit_up', 'L3'],
    ['break', 'L3'],
    ['drawdown', 'L3'],
  ] as const)('kind=%s → %s', (kind, level) => {
    expect(levelOf({ kind })).toBe(level)
  })

  it('level=critical 优先于 kind 白名单（L3 kind 也压到 L4）', () => {
    expect(levelOf({ kind: 'surge', level: 'critical' })).toBe('L4')
    expect(levelOf({ kind: 'heartbeat', level: 'critical' })).toBe('L4')
  })

  it('未知 kind + level=warning → L2', () => {
    expect(levelOf({ kind: 'something_else', level: 'warning' })).toBe('L2')
    expect(levelOf({ level: 'warn' })).toBe('L2')
  })

  it('scan_zero_hit / category=datahub / category=quality → L2', () => {
    expect(levelOf({ kind: 'scan_zero_hit' })).toBe('L2')
    expect(levelOf({ category: 'datahub' })).toBe('L2')
    expect(levelOf({ category: 'quality' })).toBe('L2')
  })

  it('其余 → L1（含空帧）', () => {
    expect(levelOf({ kind: 'heartbeat' })).toBe('L1')
    expect(levelOf({})).toBe('L1')
  })
})

describe('causeKey 同因键', () => {
  it('dedupKey 按冒号截前两段（后端去重计数尾段漂移不影响聚合）', () => {
    expect(causeKey({ dedupKey: 'surge:600000:42' })).toBe('surge:600000')
    expect(causeKey({ dedupKey: 'surge:600000:43', kind: 'x', symbol: 'y' })).toBe('surge:600000')
  })

  it('无 dedupKey 回落 kind:symbol', () => {
    expect(causeKey({ kind: 'surge', symbol: '600000' })).toBe('surge:600000')
  })

  it('双空兜底 alert:系统', () => {
    expect(causeKey({})).toBe('alert:系统')
  })
})

describe('renderAlertCopy 五段式（§8.2）', () => {
  const at = new Date(2026, 9, 21, 14, 30, 5).getTime()

  it('五段齐全：字典命中给中文名 + 行情/规则双动作', () => {
    const copy = renderAlertCopy({ kind: 'alert-rule', symbol: '600000', title: '触发规则A', body: '涨幅>5%', at }, { '600000': '浦发银行' })
    expect(copy).toEqual({
      object: '浦发银行',
      event: '触发规则A',
      condition: '涨幅>5%',
      time: '14:30:05',
      actions: [
        { id: 'quote', label: '看行情', panel: 'quote-sidebar', symbol: '600000' },
        { id: 'rule', label: '看规则', panel: 'alert-rules' },
      ],
    })
  })

  it('字典缺失 → 裸代码；非规则 kind 只有看行情；body 缺省为空串', () => {
    const copy = renderAlertCopy({ kind: 'surge', symbol: '300750', title: '急拉', at })
    expect(copy.object).toBe('300750')
    expect(copy.condition).toBe('')
    expect(copy.actions).toEqual([{ id: 'quote', label: '看行情', panel: 'quote-sidebar', symbol: '300750' }])
  })

  it('无 symbol → 对象「系统」且无看行情动作；at 缺省用注入时钟', () => {
    const copy = renderAlertCopy({ kind: 'alert-rule', title: '规则扫零' }, undefined, () => at)
    expect(copy.object).toBe('系统')
    expect(copy.time).toBe('14:30:05')
    expect(copy.actions).toEqual([{ id: 'rule', label: '看规则', panel: 'alert-rules' }])
  })
})

describe('createCauseAggregator 同因聚合', () => {
  it('同窗同因累计，first 不变', () => {
    let t = 1000
    const agg = createCauseAggregator(10_000, () => t)
    expect(agg.seen('k')).toEqual({ count: 1, first: 1000 })
    t = 5000
    expect(agg.seen('k')).toEqual({ count: 2, first: 1000 })
  })

  it('跨窗重置（now-first 等值即跨窗）', () => {
    let t = 1000
    const agg = createCauseAggregator(10_000, () => t)
    agg.seen('k')
    t = 11_000
    expect(agg.seen('k')).toEqual({ count: 1, first: 11_000 })
  })

  it('异因互不影响', () => {
    let t = 1000
    const agg = createCauseAggregator(10_000, () => t)
    agg.seen('a')
    t = 2000
    expect(agg.seen('b')).toEqual({ count: 1, first: 2000 })
    expect(agg.seen('a')).toEqual({ count: 2, first: 1000 })
  })
})

describe('cooldownCheck 冷却判定', () => {
  it('窗内抑制且不写时刻（防持续事件滑窗）', () => {
    const last = new Map([['k', 1000]])
    expect(cooldownCheck(last, 'k', 5000, 10_000)).toBe(true)
    expect(last.get('k')).toBe(1000)
  })

  it('窗外放行并写时刻；边界等值放行', () => {
    const last = new Map([['k', 1000]])
    expect(cooldownCheck(last, 'k', 11_000, 10_000)).toBe(false)
    expect(last.get('k')).toBe(11_000)
  })

  it('键隔离：异键不共享冷却', () => {
    const last = new Map([['a', 1000]])
    expect(cooldownCheck(last, 'b', 2000, 10_000)).toBe(false)
    expect(last.get('b')).toBe(2000)
  })
})

describe('quietHours 静默时段', () => {
  const win = [{ start: '22:00', end: '07:00', levels: ['L1', 'L2'] as AlertLevel[] }]
  const atH = (h: number, m = 0) => new Date(2026, 9, 21, h, m).getTime()

  it('跨午夜窗：23 时与凌晨静默，白天不静默', () => {
    expect(quietHours('L1', win, atH(23))).toBe(true)
    expect(quietHours('L1', win, atH(6, 59))).toBe(true)
    expect(quietHours('L1', win, atH(7))).toBe(false)
    expect(quietHours('L2', win, atH(22, 30))).toBe(true)
  })

  it('级别不在该窗名单 → 该窗不适用', () => {
    expect(quietHours('L3', win, atH(23))).toBe(false)
  })

  it('L4 永远直通（即便全天窗名单含 L4）', () => {
    expect(quietHours('L4', win, atH(23))).toBe(false)
    expect(quietHours('L4', [{ start: '00:00', end: '23:59', levels: ['L1', 'L2', 'L3', 'L4'] }], atH(12))).toBe(false)
  })
})
