/**
 * G2 F1 功能卡索引纯层：cardOf 快照纪律 / usageOf 用法行 / HOTKEY_ROWS 呈现副本漂移守护。
 * 帮助入口（palette「帮助 <助记符>」）：cardByPrefix 精确取卡 / helpCandidates 候选排序。
 * 不锁面板总数（注册表演进由 registry-integrity 管），只锁本模块语义。
 */
import { describe, expect, it } from 'vitest'
import { functionIndex, type FunctionCard } from '../command-grammar'
import {
  buildHelpSpecs,
  cardByPrefix,
  cardOf,
  helpCandidates,
  HOTKEY_ROWS,
  parseHelpQuery,
  usageOf,
} from '../function-index'

/** 最小卡构造（隔离真实注册表，漂移不扰本心）。 */
function fakeCard(patch: Partial<FunctionCard>): FunctionCard {
  return { id: 'x', title: 'X', prefix: 'XX', aliases: [], argKind: 'none', argHint: '', description: '', ...patch }
}

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

describe('cardByPrefix 帮助取卡（palette「帮助 <助记符>」）', () => {
  const cards = [
    fakeCard({ id: 'market', title: '个股行情', prefix: 'GP', aliases: ['QUOTE'] }),
    fakeCard({ id: 'trade', title: '交易', prefix: 'JY' }),
  ]

  it('prefix 精确命中（大小写归一，首尾空白容忍）', () => {
    expect(cardByPrefix('GP', cards)?.id).toBe('market')
    expect(cardByPrefix('gp', cards)?.id).toBe('market')
    expect(cardByPrefix('  Jy  ', cards)?.id).toBe('trade')
  })

  it('alias 精确命中', () => {
    expect(cardByPrefix('quote', cards)?.id).toBe('market')
  })

  it('未知 / 空词 → null（不杜撰）', () => {
    expect(cardByPrefix('ZZ', cards)).toBeNull()
    expect(cardByPrefix('', cards)).toBeNull()
    expect(cardByPrefix('   ', cards)).toBeNull()
  })

  it('真实注册表：GP 命中个股行情卡', () => {
    expect(cardByPrefix('GP')?.id).toBe(cardOf('market')?.id)
  })
})

describe('helpCandidates 帮助候选排序', () => {
  const cards = [
    fakeCard({ id: 'market', title: '个股行情', prefix: 'GP' }),
    fakeCard({ id: 'ladder', title: '涨停梯队', prefix: 'DB' }),
    fakeCard({ id: 'watchlist', title: '自选扫描', prefix: 'HY' }),
  ]

  it('无精确卡时纯子串命中（按注册表顺序返回）', () => {
    const out = helpCandidates('H', cards)
    // 'H' 无精确卡（GP/DB/HY 均不全等）；仅 HY 前缀子串命中，GP/DB 不含 H 出局
    expect(out.map((c) => c.id)).toEqual(['watchlist'])
  })

  it('prefix 精确命中卡排最前，标题子串卡在其后', () => {
    const table = [
      fakeCard({ id: 'a', title: '行情扩展', prefix: 'KX' }),
      fakeCard({ id: 'b', title: '个股行情', prefix: 'GP' }),
      fakeCard({ id: 'c', title: '自选', prefix: 'ZX', aliases: ['GP2'] }),
    ]
    // 'GP' 精确打中 b 排最前；c 挂 alias 'GP2' 子串命中随其后；a 零命中出局
    expect(helpCandidates('GP', table).map((c) => c.id)).toEqual(['b', 'c'])
    // '行情' 无精确卡，按标题子串保持表序
    expect(helpCandidates('行情', table).map((c) => c.id)).toEqual(['a', 'b'])
  })

  it('拉丁标题大小写归一（三条匹配路径同纪律）', () => {
    const table = [fakeCard({ id: 'etf', title: 'ETF轮动', prefix: 'LD' })]
    expect(helpCandidates('etf', table).map((c) => c.id)).toEqual(['etf'])
    expect(helpCandidates('ETF', table).map((c) => c.id)).toEqual(['etf'])
    expect(cardByPrefix('etf', table)).toBeNull() // 词与 prefix 仍按大写归一精确纪律不命中
  })

  it('空词 → 全部（受 limit 截断）；无命中 → 空表', () => {
    expect(helpCandidates('', cards, 2).map((c) => c.id)).toEqual(['market', 'ladder'])
    expect(helpCandidates('不存在', cards)).toEqual([])
  })
})

describe('parseHelpQuery 帮助分流（palette 门闸判据）', () => {
  it('非帮助词 → null（含词尾开头非「帮助」）', () => {
    expect(parseHelpQuery('GP 600519')).toBeNull()
    expect(parseHelpQuery('')).toBeNull()
    expect(parseHelpQuery('x帮助 GP')).toBeNull()
    expect(parseHelpQuery('帮助x帮助')).toBe('x帮助') // 首词命中即接管
  })

  it('裸词「帮助」（含尾随空白）→ 空串，出索引行', () => {
    expect(parseHelpQuery('帮助')).toBe('')
    expect(parseHelpQuery('帮助 ')).toBe('')
  })

  it('「帮助 <词>」取词尾（多空格 / 全角空格 / 裸紧随均归一）', () => {
    expect(parseHelpQuery('帮助 GP')).toBe('GP')
    expect(parseHelpQuery('帮助  GP')).toBe('GP')
    expect(parseHelpQuery('帮助　GP')).toBe('GP') // \s 覆盖 U+3000
    expect(parseHelpQuery('帮助GP')).toBe('GP')
  })
})

describe('buildHelpSpecs 帮助行构造', () => {
  const cards = [
    fakeCard({ id: 'market', title: '个股行情', prefix: 'GP', aliases: ['QUOTE'] }),
    fakeCard({ id: 'trade', title: '交易', prefix: 'JY' }),
  ]

  it('空词 → 唯一索引行（可执行、不带面板）', () => {
    const rows = buildHelpSpecs('', cards)
    expect(rows).toEqual([
      { id: 'help:index', label: '功能卡索引', hint: 'F1 · 打开即定格当前面板', action: 'open' },
    ])
  })

  it('有词 → 候选行带 panelId 且可执行（精确卡置顶）', () => {
    const rows = buildHelpSpecs('GP', cards)
    expect(rows[0]).toMatchObject({ id: 'help:market', panelId: 'market', action: 'open' })
    expect(rows[0]?.label).toContain('GP')
    expect(rows[0]?.hint).toContain('Enter 看功能卡')
  })

  it('未知词 → 提示行（不可执行，不杜撰卡）', () => {
    const rows = buildHelpSpecs('ZZ', cards)
    expect(rows).toEqual([{ id: 'help:none', label: '帮助 ZZ', hint: '未找到该助记符的功能卡（示例：帮助 GP / 帮助 JY）', action: 'none' }])
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
