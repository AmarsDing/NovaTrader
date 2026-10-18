import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getConfigSnapshot, normalizeConfigSnapshot, previewConfig, saveConfig } from '../config'
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
