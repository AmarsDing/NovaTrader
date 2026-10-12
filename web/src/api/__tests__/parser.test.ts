/**
 * API 归一化 parser 单测（P6）：pick/num/asList 容错取值 + audit.normalizeRow/listAudit 映射。
 */
import { describe, expect, it, vi } from 'vitest'
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
