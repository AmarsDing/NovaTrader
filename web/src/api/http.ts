/**
 * admin（2001）HTTP 入口。所有 api/* 模块只准走这里。
 * - 自动带令牌（内存）与 JSON 头；
 * - 默认 8 秒超时，可按调用覆盖；
 * - 401 统一交回全局处理器（登录失效 → 跳登录）。
 * - 无调用方 signal/body 的并发 GET 共享在途请求，完成即释放，不缓存结果。
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
// 只在当前 JS 上下文内存在；key 含会话身份，仅用于内存查找，禁止记录到日志。
const inFlightReads = new Map<string, Promise<unknown>>()

export function setUnauthorizedHandler(fn: () => void): void {
  onUnauthorized = fn
}

export async function request<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
  if (options.signal?.aborted) throw new ApiError('请求已取消', 0)

  const { method = 'GET', json, headers, query, timeoutMs = 8000, body, signal } = options
  const endpoint = httpBase()
  const url = new URL(path.startsWith('http') ? path : `${endpoint}${path.startsWith('/') ? path : `/${path}`}`)
  if (query) {
    Object.entries(query).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') url.searchParams.set(key, String(value))
    })
  }
  const token = getToken()
  const merged = new Headers({ Accept: 'application/json', ...headers })
  if (json !== undefined) merged.set('Content-Type', 'application/json')
  if (token) merged.set('Authorization', `Bearer ${token}`)

  const fetchRequest = () =>
    performRequest<T>(url, { method, json, timeoutMs, body, signal }, merged, endpoint, token, path)
  // 不共享可取消的调用，避免一个面板卸载中止其他消费者；写操作始终独立。
  if (method !== 'GET' || signal !== undefined || json !== undefined || body != null) return fetchRequest()

  const keyUrl = new URL(url)
  keyUrl.searchParams.sort()
  const key = JSON.stringify([endpoint, token, method, keyUrl.href, [...merged.entries()], timeoutMs])
  const existing = inFlightReads.get(key)
  if (existing) return existing as Promise<T>

  const pending = fetchRequest().finally(() => {
    if (inFlightReads.get(key) === pending) inFlightReads.delete(key)
  })
  inFlightReads.set(key, pending)
  return pending
}

async function performRequest<T>(
  url: URL,
  options: RequestOptions,
  headers: Headers,
  endpoint: string,
  token: string,
  path: string,
): Promise<T> {
  const { method, json, timeoutMs = 8000, body, signal: upstream } = options

  const ctrl = new AbortController()
  let abortCause: 'caller' | 'timeout' | null = null
  const abort = (cause: 'caller' | 'timeout') => {
    if (ctrl.signal.aborted) return
    abortCause = cause
    ctrl.abort()
  }
  const timer = setTimeout(() => abort('timeout'), timeoutMs)
  const onAbort = () => abort('caller')
  upstream?.addEventListener('abort', onAbort, { once: true })
  if (upstream?.aborted) onAbort()
  try {
    if (ctrl.signal.aborted) throw new ApiError('请求已取消', 0)
    const res = await fetch(url, {
      method,
      headers,
      body: json !== undefined ? JSON.stringify(json) : (body ?? null),
      signal: ctrl.signal,
    })
    const text = await res.text()
    // 读取响应体期间取消后，不能将迟到结果或 401 继续交给调用方。
    ctrl.signal.throwIfAborted()
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
      const msg = record && (record.message || record.msg) ? String(record.message || record.msg) : res.statusText
      // 旧后端/旧令牌的迟到 401 不应注销已经切换的会话。
      if (res.status === 401 && token && !path.includes('/login') && getToken() === token && httpBase() === endpoint) {
        onUnauthorized()
      }
      throw new ApiError(msg || '请求失败', res.status)
    }
    return data as T
  } catch (error) {
    if (error instanceof ApiError) throw error
    if (ctrl.signal.aborted || (error as Error | null)?.name === 'AbortError') {
      throw new ApiError(abortCause === 'timeout' ? '请求超时' : '请求已取消', 0)
    }
    throw new ApiError('无法连接 admin（2001）', 0)
  } finally {
    clearTimeout(timer)
    upstream?.removeEventListener('abort', onAbort)
  }
}
