/**
 * alert-quiet-server（M12 §8.3 存储级后端化）合同测试：
 * - parseServerQuiet：缺失不动本机 / 空串回落默认 / 坏 JSON 容错 / 逐项独立采纳；
 * - prime：服务端有值采纳为准、无值且本机非默认写回认领、本机默认不写；
 * - push：正常写 / viewer 403→readonly / 409 重试一次成才 ok / 双撞回采服务端。
 * HTTP 层按 config.test.ts 同法 vi.mock('@/api/http')；localStorage 用真 jsdom。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/http'
import { loadQuiet, saveQuiet } from '../alert-quiet'
import {
  parseServerQuiet,
  primeQuietFromServer,
  pushQuietToServer,
  QUIET_COOLDOWN_KEY,
  QUIET_WINDOWS_KEY,
  quietSyncState,
  quietSyncVersion,
  resetQuietSyncForTest,
} from '../alert-quiet-server'

const { request } = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/api/http', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/api/http')>()
  return { ...orig, request }
})

const snap = (values: Record<string, string>, revision = 7) => ({
  schema_version: 1,
  revision,
  values,
  secret_set: {},
  receipts: [],
  history: [],
  created_at: '2026-10-24T00:00:00Z',
  note: '',
})

beforeEach(() => {
  request.mockReset()
  localStorage.clear()
  resetQuietSyncForTest()
})

describe('parseServerQuiet', () => {
  it('两键缺失 → null（不动本机）', () => {
    saveQuiet({ cooldownMs: 5_000, windows: [{ start: '22:00', end: '23:00', levels: ['L1'] }] })
    expect(parseServerQuiet({})).toBeNull()
    expect(loadQuiet().cooldownMs).toBe(5_000)
  })

  it('空串 = 显式回落默认（清除覆盖）', () => {
    saveQuiet({ cooldownMs: 5_000, windows: [{ start: '22:00', end: '23:00', levels: ['L1'] }] })
    const next = parseServerQuiet({ [QUIET_COOLDOWN_KEY]: '', [QUIET_WINDOWS_KEY]: '' })
    expect(next).toEqual({ cooldownMs: 60_000, windows: [] })
  })

  it('逐项独立采纳：只回晾一窗一项时另一项保持本机', () => {
    saveQuiet({ cooldownMs: 5_000, windows: [{ start: '22:00', end: '23:00', levels: ['L1'] }] })
    const next = parseServerQuiet({ [QUIET_WINDOWS_KEY]: '[{"start":"01:00","end":"02:00","levels":["L2"]}]' })
    expect(next).toEqual({ cooldownMs: 5_000, windows: [{ start: '01:00', end: '02:00', levels: ['L2'] }] })
  })

  it('坏冷却 / 坏窗 JSON 当该项未设置', () => {
    saveQuiet({ cooldownMs: 5_000, windows: [] })
    expect(parseServerQuiet({ [QUIET_COOLDOWN_KEY]: 'abc', [QUIET_WINDOWS_KEY]: '{oops' })).toEqual({
      cooldownMs: 5_000,
      windows: [],
    })
  })
})

describe('primeQuietFromServer', () => {
  it('服务端有值 → 采纳为准（覆盖本机）', async () => {
    saveQuiet({ cooldownMs: 5_000, windows: [] })
    request.mockResolvedValueOnce(snap({ [QUIET_COOLDOWN_KEY]: '120000', [QUIET_WINDOWS_KEY]: '[{"start":"22:00","end":"07:00","levels":["L1"]}]' }))
    expect(await primeQuietFromServer()).toBe(true)
    expect(loadQuiet()).toEqual({ cooldownMs: 120_000, windows: [{ start: '22:00', end: '07:00', levels: ['L1'] }] })
    expect(quietSyncState.value).toBe('ok')
    // 不会发起认领写：只读一次
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('服务端无值且本机非默认 → 写回认领（换机不丢旧设置）', async () => {
    saveQuiet({ cooldownMs: 300_000, windows: [{ start: '22:00', end: '07:00', levels: ['L1', 'L2'] }] })
    request.mockResolvedValueOnce(snap({})) // prime GET
    request.mockResolvedValueOnce(snap({})) // 认领读基
    request.mockResolvedValueOnce(snap({}, 8)) // 认领 PUT
    expect(await primeQuietFromServer()).toBe(true)
    expect(request).toHaveBeenLastCalledWith('/api/config', {
      method: 'PUT',
      json: {
        values: {
          [QUIET_COOLDOWN_KEY]: '300000',
          [QUIET_WINDOWS_KEY]: '[{"start":"22:00","end":"07:00","levels":["L1","L2"]}]',
        },
        base_revision: 7,
      },
    })
  })

  it('服务端无值且本机默认 → 不写（未认领留给写者）', async () => {
    request.mockResolvedValueOnce(snap({}))
    expect(await primeQuietFromServer()).toBe(true)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('每上下文只 prime 一次', async () => {
    request.mockResolvedValue(snap({}))
    expect(await primeQuietFromServer()).toBe(true)
    expect(await primeQuietFromServer()).toBe(true)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('GET 失败 → error，本机不变', async () => {
    saveQuiet({ cooldownMs: 5_000, windows: [] })
    request.mockRejectedValueOnce(new ApiError('网络不可达', 0))
    expect(await primeQuietFromServer()).toBe(false)
    expect(loadQuiet().cooldownMs).toBe(5_000)
    expect(quietSyncState.value).toBe('error')
  })
})

describe('pushQuietToServer', () => {
  it('正常：读基 + PUT 两项', async () => {
    saveQuiet({ cooldownMs: 180_000, windows: [] })
    request.mockResolvedValueOnce(snap({}))
    request.mockResolvedValueOnce(snap({}, 8))
    expect(await pushQuietToServer()).toBe(true)
    expect(quietSyncState.value).toBe('ok')
    expect(request).toHaveBeenLastCalledWith('/api/config', {
      method: 'PUT',
      json: { values: { [QUIET_COOLDOWN_KEY]: '180000', [QUIET_WINDOWS_KEY]: '[]' }, base_revision: 7 },
    })
  })

  it('viewer 403 → readonly，本机仍是即时态', async () => {
    saveQuiet({ cooldownMs: 180_000, windows: [] })
    request.mockResolvedValueOnce(snap({}))
    request.mockRejectedValueOnce(new ApiError('当前是查看角色，不能改配置', 403))
    expect(await pushQuietToServer()).toBe(false)
    expect(quietSyncState.value).toBe('readonly')
    expect(loadQuiet().cooldownMs).toBe(180_000)
  })

  it('409 冲突重试一次成功 → ok', async () => {
    saveQuiet({ cooldownMs: 180_000, windows: [] })
    request.mockResolvedValueOnce(snap({}))
    request.mockRejectedValueOnce(new ApiError('配置已被其他窗口或服务修改', 409))
    request.mockResolvedValueOnce(snap({}, 9))
    request.mockResolvedValueOnce(snap({}, 10))
    expect(await pushQuietToServer()).toBe(true)
    expect(quietSyncState.value).toBe('ok')
  })

  it('双撞 409 → 回采服务端值为准（多窗并发不再互顶）', async () => {
    saveQuiet({ cooldownMs: 180_000, windows: [] })
    const seenBefore = quietSyncVersion.value
    request.mockResolvedValueOnce(snap({}))
    request.mockRejectedValueOnce(new ApiError('配置已被其他窗口或服务修改', 409))
    request.mockResolvedValueOnce(snap({}, 9))
    request.mockRejectedValueOnce(new ApiError('配置已被其他窗口或服务修改', 409))
    request.mockResolvedValueOnce(snap({ [QUIET_COOLDOWN_KEY]: '45000', [QUIET_WINDOWS_KEY]: '[]' }))
    expect(await pushQuietToServer()).toBe(true)
    expect(loadQuiet().cooldownMs).toBe(45_000)
    expect(quietSyncVersion.value).toBeGreaterThan(seenBefore)
    expect(quietSyncState.value).toBe('ok')
  })
})
