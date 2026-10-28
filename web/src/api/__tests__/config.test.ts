import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getConfigSchema, getConfigSnapshot, normalizeConfigSchema, normalizeConfigSnapshot, previewConfig, saveConfig } from '../config'
const { request } = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/api/http', () => ({ request }))
const fixture = () => ({ schema_version: 1, revision: 7, values: { 'm06.select_score': '75' }, secret_set: { 'app.brain.large.key': true }, receipts: [{ service: 'brain', instance: 'i', scope: 'app_cache', revision: 6, status: 'cache_loaded', detail: '缓存', observed_at: '2026-10-16T01:00:00Z' }] })
beforeEach(() => request.mockReset())
describe('versioned configuration API', () => {
  it('preserves desired and loaded versions separately; absent revision is not zero', () => {
    const s = normalizeConfigSnapshot(fixture())
    expect(s.revision).toBe(7); expect(s.receipts[0]?.revision).toBe(6); expect(s.secretSet['app.brain.large.key']).toBe(true)
    expect(s.values['app.brain.large.key']).toBeUndefined()
    expect(() => normalizeConfigSnapshot({ schema_version: 1, values: {} })).toThrow()
    expect(() => normalizeConfigSnapshot({ values: {} })).toThrow()
  })
  it('always sends the read base revision and does not retry conflicts', async () => {
    request.mockResolvedValueOnce(fixture()); const s = await getConfigSnapshot()
    request.mockRejectedValueOnce(new Error('配置冲突'))
    await expect(saveConfig({ 'm06.select_score': '80' }, s.revision)).rejects.toThrow('配置冲突')
    expect(request).toHaveBeenLastCalledWith('/api/config', { method: 'PUT', json: { base_revision: 7, values: { 'm06.select_score': '80' } } })
    expect(request).toHaveBeenCalledTimes(2)
  })
  it('retains unknown impact and target version in rollback preview', async () => {
    request.mockResolvedValueOnce({ base_revision: 7, changes: [], unknown_effects: ['收益未测'], known_effects: [], services: [], invalidates_plans: true })
    expect((await previewConfig({}, 7, 3)).unknownEffects).toEqual(['收益未测'])
    expect(request).toHaveBeenLastCalledWith('/api/config/preview', { method: 'POST', json: { base_revision: 7, values: {}, target_revision: 3 } })
  })
})

describe('configuration schema API (W7b / R-UI-38)', () => {
  const schemaFixture = () => ({
    schema_version: 1,
    categories: [{
      id: 'score',
      title: '评分与信号',
      note: '融合权重',
      blob_key: '',
      fields: [{
        key: 'm06.select_score', label: '入选分', hint: '综合分', kind: 'number', fallback: '75',
        store: 'config', section: '融合', blob_key: '', json_key: '', options: [], wide: false, clearable: false,
      }],
    }],
  })
  it('normalizes snake_case schema and rejects incompatible catalogs', () => {
    const s = normalizeConfigSchema(schemaFixture())
    expect(s.schemaVersion).toBe(1)
    expect(s.categories[0]?.fields[0]?.key).toBe('m06.select_score')
    expect(() => normalizeConfigSchema({ schema_version: 2, categories: [] })).toThrow(/R-UI-38/)
    expect(() => normalizeConfigSchema({ schema_version: 1 })).toThrow(/字段目录为空/)
    expect(() => normalizeConfigSchema({ schema_version: 1, categories: [] })).toThrow(/字段目录为空/)
    const broken = schemaFixture()
    broken.categories[0]!.fields[0]!.kind = 'mystery'
    expect(() => normalizeConfigSchema(broken)).toThrow(/无法识别/)
    const leaked = schemaFixture()
    leaked.categories[0]!.fields[0]!.kind = 'secret'
    leaked.categories[0]!.fields[0]!.fallback = 'should-not-surface'
    expect(normalizeConfigSchema(leaked).categories[0]?.fields[0]?.fallback).toBe('')
  })
  it('fetches /api/config/schema', async () => {
    request.mockResolvedValueOnce(schemaFixture())
    const s = await getConfigSchema()
    expect(s.categories).toHaveLength(1)
    expect(request).toHaveBeenCalledWith('/api/config/schema')
  })
})
