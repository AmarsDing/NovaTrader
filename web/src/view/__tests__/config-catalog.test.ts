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

  it('数据源运行时键齐备：kind/section/clearable 钉死（评审收口用例）', () => {
    const ds = configCategories.find((c) => c.id === 'datasource')
    expect(ds).toBeTruthy()
    const byKey = new Map(ds!.fields.map((f) => [f.key, f]))
    // 限频/配额/超时与换名文本键：留清即回落配置文件 → 全部 clearable:true。
    for (const key of [
      'app.ds.tushare.qps',
      'app.ds.tushare.quota',
      'app.ds.tushare.timeout_sec',
      'app.ds.eastmoney.qps',
      'app.ds.eastmoney.quota',
      'app.ds.eastmoney.timeout_sec',
      'app.ds.sina.qps',
      'app.ds.tencent.timeout_sec',
      'app.ds.cninfo.quota',
      'app.ds.tdx_local.timeout_sec',
      'app.ds.tdx_tcp.timeout_sec',
      'app.ds.akshare.timeout_sec',
      'app.ds.tavily.timeout_sec',
      'app.ds.sina7x24.timeout_sec',
      'app.ds.eastmoney.margin_reports',
    ]) {
      const f = byKey.get(key)
      expect(f, key).toBeTruthy()
      expect(f!.clearable, key).toBe(true)
      expect(f!.section, key).toBeTruthy()
    }
    expect(byKey.get('app.ds.eastmoney.margin_reports')!.kind).toBe('text')
    // .on 开关与凭证键的 kind 钉死（secret 不回显由网关掩码共担）。
    expect(byKey.get('app.ds.tushare.on')?.kind).toBe('bool')
    expect(byKey.get('app.ds.tushare.token')?.kind).toBe('secret')
    expect(byKey.get('app.ds.tavily.token')?.kind).toBe('secret')
    expect(byKey.get('app.ds.openbb_fred.key')?.kind).toBe('secret')
    expect(byKey.get('app.ds.sina7x24.on')?.kind).toBe('bool')
    expect(byKey.get('app.ds.sina7x24.on')?.fallback).toBe('true')
    expect(byKey.get('app.ds.baostock.on')?.kind).toBe('bool')
    expect(byKey.get('app.ds.baostock.on')?.fallback).toBe('false')
  })
})
