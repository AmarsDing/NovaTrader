import { describe, expect, it } from 'vitest'
import { coerceField, configCategories, mergeBlob, readBlobField } from '../config-catalog'

describe('配置分类', () => {
  it('键不重复，且都落在参数表能接受的长度里', () => {
    const seen = new Set<string>()
    for (const category of configCategories) {
      for (const field of category.fields) {
        const key = category.blobKey ? `${category.blobKey}.${field.jsonKey}` : field.key
        expect(seen.has(key), key).toBe(false)
        seen.add(key)
        if (field.store === 'config' && !category.blobKey) {
          expect(field.key.length).toBeLessThanOrEqual(64)
          expect(field.key.startsWith('m06.') || field.key.startsWith('app.')).toBe(true)
        }
      }
      if (category.blobKey) expect(category.blobKey.length).toBeLessThanOrEqual(64)
    }
  })

  it('合并情绪 JSON 时保留未编辑的阶段系数', () => {
    const raw = '{"phases":{"HOT":{"score_coef":1.05}},"hot_min_up":80}'
    const next = mergeBlob(raw, { hot_min_up: 90 })
    const parsed = JSON.parse(next) as { hot_min_up: number; phases: { HOT: { score_coef: number } } }
    expect(parsed.hot_min_up).toBe(90)
    expect(parsed.phases.HOT.score_coef).toBe(1.05)
    expect(readBlobField(next, 'hot_min_up', '80')).toBe('90')
    expect(readBlobField('', 'hot_min_up', '80')).toBe('80')
  })

  it('拒绝空值和非法数字', () => {
    expect(coerceField('int', ' 12 ')).toBe(12)
    expect(coerceField('number', '0.05')).toBe(0.05)
    expect(() => coerceField('int', '1.5')).toThrow()
    expect(() => coerceField('number', 'abc')).toThrow()
    expect(() => coerceField('text', '  ')).toThrow()
  })
})
