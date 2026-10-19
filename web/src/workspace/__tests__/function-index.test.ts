/**
 * G2 F1 功能卡索引纯层：cardOf 快照纪律 / usageOf 用法行 / HOTKEY_ROWS 呈现副本漂移守护。
 * 不锁面板总数（注册表演进由 registry-integrity 管），只锁本模块语义。
 */
import { describe, expect, it } from 'vitest'
import { functionIndex } from '../command-grammar'
import { cardOf, HOTKEY_ROWS, usageOf } from '../function-index'

describe('cardOf 快照取卡', () => {
  it('未知 / 空 id → null（不杜撰）', () => {
    expect(cardOf(undefined)).toBeNull()
    expect(cardOf('')).toBeNull()
    expect(cardOf('no-such-panel')).toBeNull()
  })

  it('已知面板 id → 对应卡（prefix 与注册表一致）', () => {
    const cards = functionIndex()
    const target = cards[0]
    expect(cardOf(target.id)?.prefix).toBe(target.prefix)
  })

  it('以注入 card 表为准（测试隔离）', () => {
    const fake = [{ id: 'x', title: 'X', prefix: 'XX', aliases: [], argKind: 'none' as const, argHint: '', description: 'd' }]
    expect(cardOf('x', fake)?.title).toBe('X')
    expect(cardOf('market', fake)).toBeNull()
  })
})

describe('usageOf 用法行', () => {
  const pick = (prefix: string) => {
    const card = functionIndex().find((c) => c.prefix === prefix)
    expect(card, `${prefix} 必须存在`).toBeTruthy()
    return card!
  }

  it('无参数面板（alt JT 决策台）→ 直开句式', () => {
    expect(usageOf(pick('JT'))).toBe('JT（无参数直接开）')
  })

  it('ticker 面板挂 argHint → 「prefix 参数形态」', () => {
    expect(usageOf(pick('GP'))).toMatch(/^GP\s+\S+/)
  })

  it('side 面板挂 argHint → 「prefix 参数形态」', () => {
    expect(usageOf(pick('JY'))).toMatch(/^JY\s+\S+/)
  })

  it('argHint 缺省的有参面板 → 仅 prefix（不编参数形）', () => {
    expect(usageOf({ id: 'x', title: 'X', prefix: 'XX', aliases: [], argKind: 'ticker', argHint: '', description: '' })).toBe('XX')
  })
})

describe('HOTKEY_ROWS 全局热键呈现副本', () => {
  it('清单快照（漂移必须显式评审同步）', () => {
    expect(HOTKEY_ROWS).toMatchSnapshot()
  })

  it('每项都有键符与动作', () => {
    for (const row of HOTKEY_ROWS) {
      expect(row.keys.trim()).not.toBe('')
      expect(row.action.trim()).not.toBe('')
    }
  })
})
