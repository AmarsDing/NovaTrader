/**
 * G3 盯盘告警规则前端切片：归一（camel/snake 双口径、缺省为 null）、草案纯校验、
 * 写口提交体形状（白名单字段 + operator + snake_case proto 字面）。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  ALERT_CONDITIONS,
  createAlertRule,
  deleteAlertRule,
  editAlertRule,
  listAlertRules,
  normalizeAlertRule,
  normalizeRuleList,
  rearmAlertRule,
  validateRuleDraft,
} from '../alert-rules'

vi.mock('@/api/http', () => ({ request: vi.fn() }))

async function mockedRequest() {
  const mod = await import('@/api/http')
  return mod.request as unknown as ReturnType<typeof vi.fn>
}

describe('normalizeAlertRule / normalizeRuleList', () => {
  it('snake_case 全字段行归一为 camel', () => {
    const r = normalizeAlertRule({
      id: 'r-1',
      symbol: '600519.SH',
      condition: 'above',
      target_price: 1699.5,
      note: '破前高',
      status: 'triggered',
      last_price: 1701.2,
      armed_at: '2026-10-18T09:00:00Z',
      triggered_at: '2026-10-18T10:00:00Z',
      created_at: '2026-10-17T09:00:00Z',
      updated_at: '2026-10-18T10:00:00Z',
    })
    expect(r).toEqual({
      id: 'r-1',
      symbol: '600519.SH',
      condition: 'above',
      targetPrice: 1699.5,
      note: '破前高',
      status: 'triggered',
      lastPrice: 1701.2,
      armedAt: '2026-10-18T09:00:00Z',
      triggeredAt: '2026-10-18T10:00:00Z',
      createdAt: '2026-10-17T09:00:00Z',
      updatedAt: '2026-10-18T10:00:00Z',
    })
  })

  it('camelCase 行（其余网关口径）同样归一', () => {
    const r = normalizeAlertRule({ id: 'r-2', symbol: '000001.SZ', condition: 'below', targetPrice: 9.5, lastPrice: 9.9 })
    expect(r?.targetPrice).toBe(9.5)
    expect(r?.lastPrice).toBe(9.9)
  })

  it('optional 字段缺省不落 0：lastPrice/triggeredAt 为 null/空串', () => {
    const r = normalizeAlertRule({ id: 'r-3', symbol: '000001.SZ', condition: 'crosses', target_price: 10 })
    expect(r?.lastPrice).toBeNull()
    expect(r?.triggeredAt).toBe('')
    expect(r?.status).toBe('active')
  })

  it('无 id 的坏行返回 null；列表归一滤掉坏行', () => {
    expect(normalizeAlertRule({ symbol: 'x' })).toBeNull()
    expect(normalizeAlertRule(null)).toBeNull()
    const list = normalizeRuleList({ rules: [{ id: 'a', symbol: 's' }, { noId: true }, 'junk'] })
    expect(list).toHaveLength(1)
    expect(list[0].id).toBe('a')
  })

  it('rules 缺省 → 空表', () => {
    expect(normalizeRuleList({})).toEqual([])
    expect(normalizeRuleList(null)).toEqual([])
  })
})

describe('validateRuleDraft 草案纯校验', () => {
  it('合法草案 → 空串', () => {
    expect(validateRuleDraft({ symbol: '600519.SH', condition: 'above', targetPrice: '1699.5' })).toBe('')
    expect(validateRuleDraft({ symbol: '000001.sz', condition: 'below', targetPrice: '9.5' })).toBe('')
    expect(validateRuleDraft({ symbol: ' 830799.BJ ', condition: 'crosses', targetPrice: '12' })).toBe('')
  })

  it('symbol：拒绝裸代码、错误后缀、非 6 位', () => {
    expect(validateRuleDraft({ symbol: '600519', condition: 'above', targetPrice: '10' })).not.toBe('')
    expect(validateRuleDraft({ symbol: '600519.SS', condition: 'above', targetPrice: '10' })).not.toBe('')
    expect(validateRuleDraft({ symbol: '12345.SZ', condition: 'above', targetPrice: '10' })).not.toBe('')
  })

  it('condition 三态白名单', () => {
    for (const c of ALERT_CONDITIONS) {
      expect(validateRuleDraft({ symbol: '600519.SH', condition: c, targetPrice: '10' })).toBe('')
    }
    expect(validateRuleDraft({ symbol: '600519.SH', condition: 'gte', targetPrice: '10' })).not.toBe('')
  })

  it('目标价：0/负数/NaN/Infinity 一律拒绝', () => {
    for (const p of ['0', '-1', 'abc', '', 'NaN', 'Infinity']) {
      expect(validateRuleDraft({ symbol: '600519.SH', condition: 'above', targetPrice: p })).not.toBe('')
    }
  })
})

describe('写口提交体 = proto 白名单字段', () => {
  beforeEach(async () => {
    ;(await mockedRequest()).mockReset()
  })

  it('create：snake_case 体 + symbol 统一大写 + operator，回包整表', async () => {
    const req = await mockedRequest()
    req.mockResolvedValue({ rules: [{ id: 'r-1', symbol: '600519.SH' }] })
    const rules = await createAlertRule({ symbol: '600519.sh', condition: 'above', targetPrice: '1699.5', note: '破前高' }, '操作员')
    expect(req).toHaveBeenCalledWith('/api/risk/alert/rules', {
      method: 'POST',
      json: { symbol: '600519.SH', condition: 'above', target_price: 1699.5, note: '破前高', operator: '操作员' },
    })
    expect(rules).toHaveLength(1)
  })

  it('edit：带 id；rearm/delete：仅 id + operator', async () => {
    const req = await mockedRequest()
    req.mockResolvedValue({ rules: [] })
    await editAlertRule('r-9', { symbol: '000001.SZ', condition: 'crosses', targetPrice: '10' }, 'op')
    expect(req).toHaveBeenLastCalledWith('/api/risk/alert/rules/edit', {
      method: 'POST',
      json: { id: 'r-9', symbol: '000001.SZ', condition: 'crosses', target_price: 10, note: '', operator: 'op' },
    })
    await rearmAlertRule('r-9', 'op')
    expect(req).toHaveBeenLastCalledWith('/api/risk/alert/rules/rearm', {
      method: 'POST',
      json: { id: 'r-9', operator: 'op' },
    })
    await deleteAlertRule('r-9', 'op')
    expect(req).toHaveBeenLastCalledWith('/api/risk/alert/rules/delete', {
      method: 'POST',
      json: { id: 'r-9', operator: 'op' },
    })
  })

  it('list：GET 无体', async () => {
    const req = await mockedRequest()
    req.mockResolvedValue({ rules: [] })
    await listAlertRules()
    expect(req).toHaveBeenCalledWith('/api/risk/alert/rules')
  })
})
