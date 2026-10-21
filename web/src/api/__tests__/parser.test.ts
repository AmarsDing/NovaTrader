/**
 * API 归一化 parser 单测（P6）：pick/num/asList 容错取值 + audit.normalizeRow/listAudit 映射。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { asList, num, pick } from '@/utils/format'

vi.mock('@/api/http', () => ({
  request: vi.fn(async () => ({
    items: [
      { id: 1, actor: 'alice', role: 'owner', action: 'login', target: '', detail: '', ok: true, created_at: 't0' },
      { id: '2', actor: 'bob', role: 'viewer', action: 'risk.mode', OK: false, createdAt: 't1' },
    ],
  })),
}))

import { request } from '@/api/http'
import { listAudit, normalizeRow } from '@/api/audit'
import { getSignal, normalizeSignal } from '@/api/signals'

// api-fill 批次：normalizeSignal 回填名称字典，函数体内 useSecurityStore，需激活 pinia
beforeEach(() => setActivePinia(createPinia()))

describe('pick', () => {
  it('snake/camel 混用按顺序取第一个非空', () => {
    expect(pick({ created_at: 'a', createdAt: 'b' }, 'created_at', 'createdAt')).toBe('a')
    expect(pick({ createdAt: 'b' }, 'created_at', 'createdAt')).toBe('b')
  })
  it('null/undefined/空串跳过', () => {
    expect(pick({ a: null, b: '', c: 0 }, 'a', 'b', 'c')).toBe(0)
    expect(pick({ a: undefined }, 'a')).toBeUndefined()
  })
  it('非对象安全返回 undefined', () => {
    expect(pick(null, 'a')).toBeUndefined()
    expect(pick('str', 'a')).toBeUndefined()
  })
})

describe('num', () => {
  it('可转数字返回数字', () => {
    expect(num('12.5')).toBe(12.5)
    expect(num(3)).toBe(3)
    expect(num('-7')).toBe(-7)
  })
  it('不可转/空返回 null', () => {
    expect(num('')).toBeNull()
    expect(num('abc')).toBeNull()
    expect(num(null)).toBeNull()
    expect(num(undefined)).toBeNull()
    expect(num(NaN)).toBeNull()
  })
})

describe('asList', () => {
  it('数组直接返回', () => {
    const arr = [{ id: 1 }]
    expect(asList(arr)).toBe(arr)
  })
  it('从包装键取列表', () => {
    expect(asList({ items: [1, 2] }, 'items', 'rows')).toEqual([1, 2])
    expect(asList({ rows: [3] }, 'items', 'rows')).toEqual([3])
  })
  it('无匹配/非对象返回空数组', () => {
    expect(asList({})).toEqual([])
    expect(asList(null)).toEqual([])
    expect(asList({ items: 'nope' }, 'items')).toEqual([])
  })
})

describe('audit.normalizeRow', () => {
  it('snake_case 后端行映射为 camelCase DTO', () => {
    const row = normalizeRow({
      id: 7,
      actor: 'alice',
      role: 'owner',
      action: 'risk.killswitch',
      target: 'LIVE',
      detail: '急停',
      ok: true,
      created_at: '2026-10-09T10:00:00Z',
    })
    expect(row).toEqual({
      id: 7,
      actor: 'alice',
      role: 'owner',
      action: 'risk.killswitch',
      target: 'LIVE',
      detail: '急停',
      ok: true,
      createdAt: '2026-10-09T10:00:00Z',
    })
  })
  it('缺失字段兜底：ok=false、id=0、字符串空', () => {
    const row = normalizeRow({ action: 'login' })
    expect(row.id).toBe(0)
    expect(row.ok).toBe(false)
    expect(row.actor).toBe('')
    expect(row.createdAt).toBe('')
  })
  it('兼容大写 OK 与 camelCase createdAt', () => {
    expect(normalizeRow({ OK: true, createdAt: 't1' }).ok).toBe(true)
    expect(normalizeRow({ createdAt: 't1' }).createdAt).toBe('t1')
  })
})

describe('audit.listAudit', () => {
  it('请求 /api/v1/audit 并归一化 items', async () => {
    const rows = await listAudit({ action: 'login', limit: 10 })
    expect(request).toHaveBeenCalledWith('/api/v1/audit', { query: { action: 'login', limit: 10 } })
    expect(rows).toHaveLength(2)
    expect(rows[0]).toMatchObject({ id: 1, actor: 'alice', ok: true, createdAt: 't0' })
    // 字符串 id 转数字、大写 OK=false 兜底
    expect(rows[1]).toMatchObject({ id: 2, actor: 'bob', ok: false, createdAt: 't1' })
  })
})

describe('signals.normalizeSignal', () => {
  it('真实部分成交保留数量与四位均价，缺失与非法值不显示为零', () => {
    expect(normalizeSignal({ status: 'done', executed_volume: 100, executed_price: 10.1234 })).toMatchObject({ executedVolume: 100, executedPrice: 10.1234 })
    expect(normalizeSignal({ status: 'done' })).toMatchObject({ executedVolume: null, executedPrice: null })
    expect(normalizeSignal({ executed_volume: -1, executed_price: 0 })).toMatchObject({ executedVolume: null, executedPrice: null })
  })
  it('保留数值或字符串身份、账本和策略版本，收益采用后端百分点', () => {
    expect(
      normalizeSignal({ id: 7, book: 'paper', strategy_version_id: 12, has_pnl: true, pnl_pct: 3.2 }),
    ).toMatchObject({ id: '7', book: 'paper', strategyVersionId: '12', hasPnl: true, pnlPct: 3.2 })
    expect(normalizeSignal({ id: '8', strategyVersionId: '13' })).toMatchObject({ id: '8', strategyVersionId: '13' })
  })

  it('显式 false 胜过默认收益值，真实零收益由 true 声明', () => {
    expect(normalizeSignal({ status: 'closed', hasPnl: false, pnlPct: 3.2 })).toMatchObject({
      hasPnl: false,
      pnlPct: null,
    })
    expect(normalizeSignal({ status: 'approved', pnl_pct: 0 })).toMatchObject({ hasPnl: false, pnlPct: null })
    expect(normalizeSignal({ status: 'closed', pnl_pct: 0 })).toMatchObject({ hasPnl: false, pnlPct: null })
    expect(normalizeSignal({ status: 'closed', has_pnl: true, pnl_pct: 0 })).toMatchObject({ hasPnl: true, pnlPct: 0 })
  })

  it('旧接口只对已平仓且有非零收益的记录兼容兜底，缺值仍未知', () => {
    expect(normalizeSignal({ status: 'closed', pnl_pct: -2.1 })).toMatchObject({ hasPnl: true, pnlPct: -2.1 })
    expect(normalizeSignal({ has_pnl: true })).toMatchObject({ hasPnl: false, pnlPct: null })
    expect(normalizeSignal({ status: 'approved', pnl_pct: 3.2 })).toMatchObject({ hasPnl: false, pnlPct: null })
  })

  it('单票读取供提交前再次核验身份和状态', async () => {
    vi.mocked(request).mockResolvedValueOnce({ id: 7, book: 'paper', status: 'approved', strategy_version_id: 12 })
    expect(await getSignal('7')).toMatchObject({ id: '7', book: 'paper', status: 'approved', strategyVersionId: '12' })
    expect(request).toHaveBeenLastCalledWith('/api/strategy/signals/7')
  })
})
