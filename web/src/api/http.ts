/**
 * admin（2001）HTTP 入口。所有 api/* 模块只准走这里。
 * - 自动带令牌（内存）与 JSON 头；
 * - 默认 8 秒超时，可按调用覆盖；
 * - 401 统一交回全局处理器（登录失效 → 跳登录）。
 */
import { httpBase } from '@/utils/endpoint'
import { getToken } from '@/utils/token'

export class ApiError extends Error {
  status: number
  constructor(message?: string, status = 0) {
    super(message || '请求失败')
    this.name = 'ApiError'
    this.status = status
  }
}

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH' | 'HEAD'

export interface RequestOptions {
  method?: HttpMethod
  json?: unknown
  headers?: Record<string, string>
  query?: Record<string, unknown>
  timeoutMs?: number
  body?: BodyInit | null
  signal?: AbortSignal
}

let onUnauthorized: () => void = () => {}

export function setUnauthorizedHandler(fn: () => void): void {
  onUnauthorized = fn
}

export async function request<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
  const { json, headers, query, timeoutMs = 8000, ...rest } = options
  const url = new URL(path.startsWith('http') ? path : `${httpBase()}${path.startsWith('/') ? path : `/${path}`}`)
  if (query) {
    Object.entries(query).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') url.searchParams.set(key, String(value))
    })
  }
  const token = getToken()
  const merged: Record<string, string> = { Accept: 'application/json', ...headers }
  if (json !== undefined) merged['Content-Type'] = 'application/json'
  if (token) merged.Authorization = `Bearer ${token}`

  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeoutMs)
  // 调用方可以带自己的 signal（如页面卸载）；两个都接上。
  const upstream = options.signal
  const onAbort = () => ctrl.abort()
  upstream?.addEventListener('abort', onAbort)
  try {
    const res = await fetch(url, {
      ...rest,
      headers: merged,
      body: json !== undefined ? JSON.stringify(json) : (rest.body ?? null),
      signal: ctrl.signal,
    })
    const text = await res.text()
    let data: unknown = null
    if (text) {
      try {
        data = JSON.parse(text)
      } catch {
        data = text
      }
    }
    if (!res.ok) {
      const record = data && typeof data === 'object' ? (data as Record<string, unknown>) : null
      const msg =
        record && (record.message || record.msg)
          ? String(record.message || record.msg)
          : res.statusText
      if (res.status === 401 && token && !path.includes('/login')) onUnauthorized()
      throw new ApiError(msg || '请求失败', res.status)
    }
    return data as T
  } catch (error) {
    if (error instanceof ApiError) throw error
    if ((error as Error | null)?.name === 'AbortError') throw new ApiError('请求超时', 0)
    throw new ApiError('无法连接 admin（2001）', 0)
  } finally {
    clearTimeout(timer)
    upstream?.removeEventListener('abort', onAbort)
  }
}
