/**
 * alert-quiet（§8.3 前端静默/冷却配置）合同测试：
 * 默认回退 / 存取往返 / 坏 JSON 容错 / isQuietNow 命中·窗外·L4 直通三类。
 * 走真 localStorage（jsdom），每条用例前置清空。
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { ALERT_QUIET_KEY, isQuietNow, loadQuiet, saveQuiet } from '../alert-quiet'

const atH = (h: number, m = 0) => new Date(2026, 9, 21, h, m).getTime()

beforeEach(() => localStorage.clear())

describe('loadQuiet 默认与容错', () => {
  it('无键 → 默认（冷却 60s，无静默窗）', () => {
    expect(loadQuiet()).toEqual({ cooldownMs: 60_000, windows: [] })
  })

  it('坏 JSON → 默认', () => {
    localStorage.setItem(ALERT_QUIET_KEY, '{oops')
    expect(loadQuiet()).toEqual({ cooldownMs: 60_000, windows: [] })
  })

  it('缺字段/错类型逐项回退默认', () => {
    localStorage.setItem(ALERT_QUIET_KEY, JSON.stringify({ windows: 'bad' }))
    expect(loadQuiet()).toEqual({ cooldownMs: 60_000, windows: [] })
    localStorage.setItem(ALERT_QUIET_KEY, JSON.stringify({ cooldownMs: -1, windows: [{ start: 1, end: '07:00', levels: ['L1'] }] }))
    expect(loadQuiet()).toEqual({ cooldownMs: 60_000, windows: [] })
  })
})

describe('save/load 存取往返', () => {
  it('写入后读回一致', () => {
    const cfg = { cooldownMs: 30_000, windows: [{ start: '22:00', end: '07:00', levels: ['L1', 'L2'] as const }] }
    saveQuiet({ cooldownMs: cfg.cooldownMs, windows: cfg.windows.map((w) => ({ ...w, levels: [...w.levels] })) })
    expect(loadQuiet()).toEqual(cfg)
  })
})

describe('isQuietNow 命中/窗外/L4 直通', () => {
  beforeEach(() => saveQuiet({ cooldownMs: 60_000, windows: [{ start: '22:00', end: '07:00', levels: ['L1', 'L2'] }] }))

  it('命中窗内级别 → 静默', () => {
    expect(isQuietNow('L1', atH(23))).toBe(true)
    expect(isQuietNow('L2', atH(6, 30))).toBe(true)
  })

  it('窗外 → 不静默', () => {
    expect(isQuietNow('L1', atH(12))).toBe(false)
  })

  it('L4 直通 + 级别不在名单不适用', () => {
    expect(isQuietNow('L4', atH(23))).toBe(false)
    expect(isQuietNow('L3', atH(23))).toBe(false)
  })
})
