/**
 * admin（2001）HTTP 入口。所有 api/* 模块只准走这里。
 * - 自动带令牌（内存）与 JSON 头；
 * - 默认 8 秒超时，可按调用覆盖；长任务经 resolveTimeoutMs 前缀表自动放宽（LLM/回测/补源等）；
 * - 错误归一 classifyApiError（R-UI-36）：UI 引导卡按 kind 给「去处理」动作，不裸抛 toast；
 * - 401 统一交回全局处理器（登录失效 → 跳登录）。
 * - 无调用方 signal/body 的并发 GET 共享在途请求，完成即释放，不缓存结果。
 */
import { getHttpPort, httpBase } from '@/utils/endpoint'
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

  const { method = 'GET', json, headers, query, timeoutMs, body, signal } = options
  const effectiveTimeoutMs = resolveTimeoutMs(path, method, timeoutMs)
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
    performRequest<T>(url, { method, json, timeoutMs: effectiveTimeoutMs, body, signal }, merged, endpoint, token, path)
  // 不共享可取消的调用，避免一个面板卸载中止其他消费者；写操作始终独立。
  if (method !== 'GET' || signal !== undefined || json !== undefined || body != null) return fetchRequest()

  const keyUrl = new URL(url)
  keyUrl.searchParams.sort()
  const key = JSON.stringify([endpoint, token, method, keyUrl.href, [...merged.entries()], effectiveTimeoutMs])
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
  const { method, json, timeoutMs = DEFAULT_TIMEOUT_MS, body, signal: upstream } = options

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
    throw new ApiError(`无法连接 admin（${getHttpPort()}）`, 0)
  } finally {
    clearTimeout(timer)
    upstream?.removeEventListener('abort', onAbort)
  }
}

const DEFAULT_TIMEOUT_MS = 8000

/**
 * 长任务超时前缀表：(路径前缀, 方法) → 超时毫秒。取第一个命中规则；
 * GET 一律不给长档（列表/查询快出快回）；未命中落默认 8s。
 * 新增条目随面板侧调用评审，前缀不得宽到吞掉常规查询。
 */
const LONG_TIMEOUT_RULES: ReadonlyArray<readonly [prefix: string, method: HttpMethod | 'ALL', timeoutMs: number]> = [
  ['/api/brain/analyze', 'POST', 180_000],
  ['/api/brain/ask', 'POST', 180_000],
  ['/api/brain/briefing', 'POST', 180_000],
  ['/api/brain/review', 'POST', 120_000],
  ['/api/brain/prediction-checks/run', 'POST', 120_000],
  ['/api/backtest/tasks', 'POST', 120_000],
  ['/api/backtest/introspections/run', 'POST', 120_000],
  ['/api/backtest/research/jobs', 'POST', 120_000],
  ['/api/datahub/backfill', 'POST', 180_000],
  ['/api/ops/backup', 'POST', 120_000],
  ['/api/strategy/scan', 'POST', 60_000],
  ['/api/trade/nav/export', 'ALL', 60_000],
  ['/api/trade/portfolio/preview', 'POST', 60_000],
]

/** 解析调用超时：显式传参 > 前缀表 > 默认 8s。 */
export function resolveTimeoutMs(path: string, method: HttpMethod = 'GET', explicit?: number): number {
  if (typeof explicit === 'number' && explicit > 0) return explicit
  for (const [prefix, ruleMethod, timeoutMs] of LONG_TIMEOUT_RULES) {
    if (path.startsWith(prefix) && (ruleMethod === 'ALL' || ruleMethod === method)) return timeoutMs
  }
  return DEFAULT_TIMEOUT_MS
}

/** UI 归一错误分类（R-UI-36）：引导卡按 kind 给「去处理」跳转，裸 toast 不够用。 */
export type ApiErrorKind = 'llm' | 'data' | 'broker' | 'billing' | 'auth' | 'network' | 'risk' | 'unknown'

export interface ClassifiedError {
  kind: ApiErrorKind
  message: string
  retriable: boolean
}

export function classifyApiError(error: unknown): ClassifiedError {
  const message = error instanceof Error ? error.message : String(error)
  const status = error instanceof ApiError ? error.status : 0
  if (status === 401 || status === 403) return { kind: 'auth', message, retriable: false }
  if (/无法连接|network|econnrefused|etimedout|fetch failed/i.test(message)) return { kind: 'network', message, retriable: true }
  if (/请求超时|超时|timeout/i.test(message)) return { kind: 'network', message, retriable: true }
  if (/风控|预检拦截|precheck|admission|risk.?block/i.test(message)) return { kind: 'risk', message, retriable: false }
  if (/东财|goldminer|broker|文件单|券商/i.test(message)) return { kind: 'broker', message, retriable: true }
  if (/llm|deepseek|openai|gpt|模型调用|prompt/i.test(message)) return { kind: 'llm', message, retriable: true }
  if (/billing|quota|余额|计费/i.test(message)) return { kind: 'billing', message, retriable: false }
  if (/数据源|datahub|source.?down|拉取失败|采集失败/i.test(message)) return { kind: 'data', message, retriable: true }
  if (status >= 500) return { kind: 'unknown', message, retriable: true }
  return { kind: 'unknown', message, retriable: false }
}
