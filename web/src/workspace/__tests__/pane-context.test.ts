/**
 * G5 面板上下文附件纪律：账面键/敏感键剔除、仅标量 ≤8、值 ≤80、
 * 嵌入段 ≤800 从尾部裁、deny 面板参数整片不出、问 AI 候选出现条件。
 * 这是验收②「簿记键与 private 字段不出现在上下文」的快照级锁。
 */
import { describe, expect, it } from 'vitest'
import {
  buildPaneAttachment,
  composePaneQuestion,
  offerAskCandidate,
  PANE_MAX_EMBED_LEN,
} from '../pane-context'

describe('buildPaneAttachment 隐私与体量纪律', () => {
  it('账面键（滚动/尺寸/排序/选中/布局/__ 前缀）一律剔除', () => {
    const att = buildPaneAttachment('market', '行情', {
      scrollTop: 42,
      width: 800,
      sortBy: 'price',
      selectedRow: 3,
      layoutVersion: 7,
      __v: 1,
      cursor: 12,
      symbol: '600519',
    })
    expect(att.params).toEqual({ symbol: '600519' })
  })

  it('敏感键（口令/令牌/webhook/cookie/api key）一律剔除', () => {
    const att = buildPaneAttachment('knowledge', '知识库', {
      apiKey: 'sk-xxx',
      webhookUrl: 'https://x',
      authToken: 't',
      password: 'p',
      sessionId: 's',
      cookieJar: 'c',
      symbol: '000001',
      note: 'ok',
    })
    expect(att.params).toEqual({ note: 'ok', symbol: '000001' })
  })

  it('非标量丢弃：对象/数组/null/undefined/NaN/Infinity', () => {
    const att = buildPaneAttachment('market', '行情', {
      nested: { a: 1 },
      list: [1, 2],
      nil: null,
      und: undefined,
      nan: Number.NaN,
      inf: Number.POSITIVE_INFINITY,
      symbol: '600519',
    })
    expect(att.params).toEqual({ symbol: '600519' })
  })

  it('字符串 trim、空串丢弃、超 80 截断；boolean/number 原样', () => {
    const att = buildPaneAttachment('market', '行情', {
      empty: '   ',
      long: 'x'.repeat(120),
      side: ' buy ',
      qty: 100,
      force: true,
    })
    expect(att.params).toEqual({ long: 'x'.repeat(80), side: 'buy', qty: 100, force: true })
  })

  it('最多 8 个参数（取插入序前 8 个通过者）', () => {
    const raw: Record<string, unknown> = {}
    for (let i = 0; i < 12; i += 1) raw[`p${i}`] = i
    const att = buildPaneAttachment('signals', '信号', raw)
    expect(Object.keys(att.params)).toHaveLength(8)
    expect(Object.keys(att.params)).toEqual(['p0', 'p1', 'p2', 'p3', 'p4', 'p5', 'p6', 'p7'])
  })

  it('deny 面板（settings 配置）参数整片不出，仍报 id/标题', () => {
    const att = buildPaneAttachment('settings', '配置', { symbol: '600519', mode: 'SIM' })
    expect(att.params).toEqual({})
    expect(att.panelId).toBe('settings')
    expect(att.panelTitle).toBe('配置')
  })

  it('rawParams 为空/无参数时 params 为空对象', () => {
    expect(buildPaneAttachment('market', '行情', undefined).params).toEqual({})
    expect(buildPaneAttachment('market', '行情', null).params).toEqual({})
  })
})

describe('composePaneQuestion 嵌入与截断', () => {
  it('无附件 → 问题原样（trim）', () => {
    expect(composePaneQuestion(null, '  为什么跳水？ ')).toBe('为什么跳水？')
    expect(composePaneQuestion({ panelId: '', panelTitle: '', params: {} }, '问题')).toBe('问题')
  })

  it('有附件 → 头部含面板标题、id 与参数对，问题缀后', () => {
    const att = buildPaneAttachment('market', '行情', { symbol: '600519' })
    const text = composePaneQuestion(att, '这个面板的股票今天涨多少')
    expect(text).toContain('【面板 行情（market）】symbol=600519')
    expect(text).toContain('问：这个面板的股票今天涨多少')
    expect(text.indexOf('symbol=600519')).toBeLessThan(text.indexOf('问：'))
  })

  it('嵌入段超 800 从尾部参数对裁短，且绝不出敏感/簿记键（因上游已滤）', () => {
    const raw: Record<string, unknown> = {}
    // 键名不设上限（值才有 80 上限）：8 个长键名 + 满值必超 800，触发尾部裁对。
    for (let i = 0; i < 8; i += 1) raw[`strategy_parameter_slot_${String(i).padStart(2, '0')}`] = 'v'.repeat(80)
    const att = buildPaneAttachment('backtest', '回测', raw)
    const text = composePaneQuestion(att, '总结这组参数')
    const header = text.split('\n问：')[0]
    expect(header.length).toBeLessThanOrEqual(PANE_MAX_EMBED_LEN)
    expect(header).toContain('【面板 回测（backtest）】')
    expect(header).not.toContain('strategy_parameter_slot_07=')
    expect(text).toContain('问：总结这组参数')
  })
})

describe('offerAskCandidate 候选出现条件', () => {
  it('≥3 字符且无助记符命中 → 供；否则不供', () => {
    expect(offerAskCandidate('为什么', false)).toBe(true)
    expect(offerAskCandidate('为什', false)).toBe(false)
    expect(offerAskCandidate('   abc ', false)).toBe(true)
    expect(offerAskCandidate('这只票跳水原因', true)).toBe(false)
  })
})
