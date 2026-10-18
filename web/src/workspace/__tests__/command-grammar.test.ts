/**
 * G2 助记符语法单测：解析、归一、参数状态机。
 * 用真实注册表（21 面板）驱动，助记符表变更时此套件同步锁定行为。
 */
import { describe, expect, it } from 'vitest'
import {
  functionIndex,
  isTicker,
  normalizeSide,
  normalizeTicker,
  parseMnemonic,
  resolveMnemonic,
} from '../command-grammar'

describe('parseMnemonic 分段', () => {
  it('无前缀参数 → null', () => {
    expect(parseMnemonic('')).toBeNull()
    expect(parseMnemonic('   ')).toBeNull()
  })

  it('首段大写化，余段为参数', () => {
    expect(parseMnemonic('gp 600519')).toEqual({ prefix: 'GP', arg: '600519' })
    expect(parseMnemonic('DB')).toEqual({ prefix: 'DB', arg: '' })
    expect(parseMnemonic('  hq   600519.SH ')).toEqual({ prefix: 'HQ', arg: '600519.SH' })
  })

  it('首段非字母数字或超 4 位 → null（交给模糊搜索）', () => {
    expect(parseMnemonic('涨停')).toBeNull()
    expect(parseMnemonic('ABCDE')).toBeNull()
  })

  it('连写不分段 → null（无空格即非助记符，交给模糊搜索）', () => {
    // 前缀最长 4 位且必须整词匹配，GP600519 整体 8 位无法入语法。
    expect(parseMnemonic('GP600519')).toBeNull()
    expect(resolveMnemonic('GP600519')).toBeNull()
  })
})

describe('normalizeTicker 代码归一', () => {
  it('6 位数字原样通过', () => {
    expect(normalizeTicker('600519')).toBe('600519')
    expect(normalizeTicker('000001')).toBe('000001')
  })

  it('交易所前后缀兼容', () => {
    expect(normalizeTicker('600519.SH')).toBe('600519')
    expect(normalizeTicker('SH600519')).toBe('600519')
    expect(normalizeTicker('sz000001')).toBe('000001')
    expect(normalizeTicker('430047.bj')).toBe('430047')
  })

  it('非法形态 → null', () => {
    expect(normalizeTicker('60051')).toBeNull()
    expect(normalizeTicker('6005199')).toBeNull()
    expect(normalizeTicker('贵州茅台')).toBeNull()
    expect(normalizeTicker('')).toBeNull()
  })

  it('isTicker 只认归一形态', () => {
    expect(isTicker('600519')).toBe(true)
    expect(isTicker('600519.SH')).toBe(false)
    expect(isTicker('hello')).toBe(false)
  })
})

describe('normalizeSide 买卖归一', () => {
  it('buy 家族', () => {
    expect(normalizeSide('buy')).toBe('buy')
    expect(normalizeSide('B')).toBe('buy')
    expect(normalizeSide('买')).toBe('buy')
  })

  it('sell 家族', () => {
    expect(normalizeSide('sell')).toBe('sell')
    expect(normalizeSide('S')).toBe('sell')
    expect(normalizeSide('卖')).toBe('sell')
  })

  it('其他 → null', () => {
    expect(normalizeSide('hold')).toBeNull()
    expect(normalizeSide('')).toBeNull()
  })
})

describe('resolveMnemonic 状态机', () => {
  it('未知前缀 → null', () => {
    expect(resolveMnemonic('ZZ')).toBeNull()
    expect(resolveMnemonic('GP60')).toBeNull()
  })

  it('无参数面板：前缀即开，多余参数宽松忽略', () => {
    const r = resolveMnemonic('DB')
    expect(r?.def.id).toBe('limit-ladder')
    expect(r?.state).toBe('valid')
    expect(resolveMnemonic('DB anything')?.state).toBe('valid')
  })

  it('ticker 面板：空参 / 输入中 / 有效 / 非法 四态', () => {
    expect(resolveMnemonic('GP')?.state).toBe('empty')
    expect(resolveMnemonic('GP 600')?.state).toBe('partial')
    const ok = resolveMnemonic('GP 600519')
    expect(ok?.state).toBe('valid')
    expect(ok?.ticker).toBe('600519')
    expect(resolveMnemonic('GP 贵州')?.state).toBe('invalid')
  })

  it('ticker 归一从前缀命令直达', () => {
    expect(resolveMnemonic('HQ sz000001')?.ticker).toBe('000001')
  })

  it('side 面板：买/卖/非法', () => {
    const buy = resolveMnemonic('JY buy')
    expect(buy?.def.id).toBe('trade')
    expect(buy?.side).toBe('buy')
    expect(resolveMnemonic('JY 卖')?.side).toBe('sell')
    expect(resolveMnemonic('JY')?.state).toBe('empty')
    expect(resolveMnemonic('JY hold')?.state).toBe('invalid')
  })

  it('别名命中同一面板', () => {
    expect(resolveMnemonic('HQ')?.def.id).toBe('market')
    expect(resolveMnemonic('HQ 600519')?.ticker).toBe('600519')
  })
})

describe('functionIndex 功能卡', () => {
  it('覆盖全部 21 面板且字段齐备', () => {
    const cards = functionIndex()
    expect(cards).toHaveLength(21)
    for (const c of cards) {
      expect(c.prefix).toMatch(/^[A-Z0-9]{2,4}$/)
      expect(c.title).toBeTruthy()
      expect(c.description).toBeTruthy()
      if (c.argKind !== 'none') expect(c.argHint).toBeTruthy()
    }
  })
})
