import { describe, expect, it } from 'vitest'
import { ApiError, classifyApiError, resolveTimeoutMs } from '../http'

describe('resolveTimeoutMs', () => {
  it('显式传参优先', () => {
    expect(resolveTimeoutMs('/api/brain/analyze', 'POST', 5000)).toBe(5000)
    expect(resolveTimeoutMs('/api/notify/messages', 'GET', 0)).toBe(8000)
  })

  it('LLM/补源/回测长任务吃长档', () => {
    expect(resolveTimeoutMs('/api/brain/analyze', 'POST')).toBe(180_000)
    expect(resolveTimeoutMs('/api/brain/ask', 'POST')).toBe(180_000)
    expect(resolveTimeoutMs('/api/datahub/backfill', 'POST')).toBe(180_000)
    expect(resolveTimeoutMs('/api/backtest/tasks', 'POST')).toBe(120_000)
    expect(resolveTimeoutMs('/api/strategy/scan', 'POST')).toBe(60_000)
  })

  it('同名前缀的 GET 与未知前缀落默认 8s', () => {
    expect(resolveTimeoutMs('/api/brain/analyze', 'GET')).toBe(8000)
    expect(resolveTimeoutMs('/api/notify/messages', 'GET')).toBe(8000)
    expect(resolveTimeoutMs('/api/ops/status', 'GET')).toBe(8000)
  })
})

describe('classifyApiError', () => {
  it('断连/超时归 network 且可重试', () => {
    expect(classifyApiError(new ApiError('无法连接 admin（127.0.0.1:2001）', 0)).kind).toBe('network')
    expect(classifyApiError(new ApiError('无法连接 admin（2001）', 0))).toEqual({
      kind: 'network',
      message: '无法连接 admin（2001）',
      retriable: true,
    })
    expect(classifyApiError(new ApiError('请求超时', 0)).kind).toBe('network')
  })

  it('401/403 归 auth 不可重试', () => {
    expect(classifyApiError(new ApiError('未授权', 401)).kind).toBe('auth')
    expect(classifyApiError(new ApiError('禁止访问', 403)).kind).toBe('auth')
  })

  it('风控/预检文案优先归 risk', () => {
    expect(classifyApiError(new ApiError('风控预检拦截', 400)).kind).toBe('risk')
  })

  it('LLM/券商/数据源文案各归各位', () => {
    expect(classifyApiError(new ApiError('DeepSeek 模型调用失败', 500)).kind).toBe('llm')
    expect(classifyApiError(new ApiError('东财文件单写入失败', 500)).kind).toBe('broker')
    expect(classifyApiError(new ApiError('数据源拉取失败', 0)).kind).toBe('data')
  })

  it('5xx 未知归 unknown 但可重试，其余 unknown 不可重试', () => {
    expect(classifyApiError(new ApiError('内部错误', 500))).toEqual({ kind: 'unknown', message: '内部错误', retriable: true })
    expect(classifyApiError(new Error('奇怪')).retriable).toBe(false)
  })
})
