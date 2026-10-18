import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, request, setUnauthorizedHandler, type RequestOptions } from '@/api/http'
import { setHost } from '@/utils/endpoint'
import { setMemoryToken } from '@/utils/token'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

type PendingFetch = ReturnType<typeof deferred<Response>> & { init?: RequestInit }
let pending: PendingFetch[]
let unauthorized = vi.fn<() => void>()

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  setMemoryToken('session-a')
  pending = []
  unauthorized = vi.fn()
  setUnauthorizedHandler(unauthorized)
  vi.stubGlobal(
    'fetch',
    vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      const call = { ...deferred<Response>(), init }
      const abort = () => call.reject(new DOMException('The operation was aborted.', 'AbortError'))
      init?.signal?.addEventListener('abort', abort, { once: true })
      pending.push(call)
      return call.promise.finally(() => init?.signal?.removeEventListener('abort', abort))
    }),
  )
})

afterEach(() => {
  setUnauthorizedHandler(() => {})
  setMemoryToken('')
  localStorage.clear()
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('HTTP in-flight GET 合并', () => {
  it('20 个并发读只发一次，完成后下次读重新发请求', async () => {
    const readers = Array.from({ length: 20 }, () => request('/api/account'))
    expect(fetch).toHaveBeenCalledTimes(1)
    pending[0].resolve(jsonResponse({ equity: 100 }))
    expect(await Promise.all(readers)).toEqual(Array.from({ length: 20 }, () => ({ equity: 100 })))
    expect(vi.getTimerCount()).toBe(0)

    const refreshed = request('/api/account')
    expect(fetch).toHaveBeenCalledTimes(2)
    pending[1].resolve(jsonResponse({ equity: 101 }))
    expect(await refreshed).toEqual({ equity: 101 })
  })

  it('以有效 query 和大小写归一化后的 headers 合并，显式 GET 与默认一致', async () => {
    const first = request('/api/market', {
      query: { book: 'SIM', limit: 20, skip: null, empty: '', absent: undefined },
      headers: { 'X-Trace': 'watch' },
    })
    const second = request('/api/market?limit=20', {
      method: 'GET',
      query: { book: 'SIM' },
      headers: { 'x-trace': 'watch' },
      timeoutMs: 8000,
    })
    expect(fetch).toHaveBeenCalledTimes(1)
    const url = new URL(String(vi.mocked(fetch).mock.calls[0][0]))
    expect([...url.searchParams]).toEqual([
      ['book', 'SIM'],
      ['limit', '20'],
    ])
    expect(new Headers(pending[0].init?.headers).get('Authorization')).toBe('Bearer session-a')
    pending[0].resolve(jsonResponse({ items: [] }))
    await Promise.all([first, second])
  })

  it.each([
    ['查询参数', { query: { book: 'SIM' } }, { query: { book: 'LIVE' } }],
    ['请求头', { headers: { 'X-View': 'one' } }, { headers: { 'X-View': 'two' } }],
    ['超时预算', { timeoutMs: 1000 }, { timeoutMs: 2000 }],
  ] satisfies [string, RequestOptions, RequestOptions][])('不同%s不合并', async (_label, left, right) => {
    const readers = [request('/api/account', left), request('/api/account', right)]
    expect(fetch).toHaveBeenCalledTimes(2)
    pending.forEach((call, index) => call.resolve(jsonResponse({ index })))
    expect(await Promise.all(readers)).toEqual([{ index: 0 }, { index: 1 }])
  })

  it('不同登录令牌与后端地址隔离，包括绝对 URL 请求', async () => {
    const url = 'http://127.0.0.1:2001/api/account'
    const first = request(url)
    setMemoryToken('session-b')
    const second = request(url)
    setHost('192.0.2.1')
    const third = request(url)
    expect(fetch).toHaveBeenCalledTimes(3)
    expect(pending.map((call) => new Headers(call.init?.headers).get('Authorization'))).toEqual([
      'Bearer session-a',
      'Bearer session-b',
      'Bearer session-b',
    ])
    pending.forEach((call, index) => call.resolve(jsonResponse({ index })))
    expect(await Promise.all([first, second, third])).toEqual([{ index: 0 }, { index: 1 }, { index: 2 }])
  })

  it.each(['POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'] as const)('%s 始终独立，交易写入不合并', async (method) => {
    const writes = [request('/api/trade/orders', { method }), request('/api/trade/orders', { method })]
    expect(fetch).toHaveBeenCalledTimes(2)
    pending.forEach((call, index) => call.resolve(jsonResponse({ id: index })))
    expect(await Promise.all(writes)).toEqual([{ id: 0 }, { id: 1 }])
  })

  it.each([{ body: 'body' }, { json: { value: 1 } }] satisfies RequestOptions[])(
    '带 GET body/json 的调用不进入共享读表（%j）',
    async (options) => {
      const readers = [request('/api/account', options), request('/api/account', options)]
      expect(fetch).toHaveBeenCalledTimes(2)
      pending.forEach((call) => call.resolve(jsonResponse({ ok: true })))
      await Promise.all(readers)
    },
  )

  it('网络失败后所有读者得到错误，并清除在途项允许再次尝试', async () => {
    const readers = [request('/api/account'), request('/api/account')]
    const failed = Promise.all(readers.map((reader) => expect(reader).rejects.toMatchObject({ status: 0 })))
    expect(fetch).toHaveBeenCalledTimes(1)
    pending[0].reject(new TypeError('offline'))
    await failed
    expect(vi.getTimerCount()).toBe(0)

    const retry = request('/api/account')
    expect(fetch).toHaveBeenCalledTimes(2)
    pending[1].resolve(jsonResponse({ ok: true }))
    expect(await retry).toEqual({ ok: true })
  })

  it('HTTP 失败保留服务端消息和状态，并允许重试', async () => {
    const first = request('/api/account')
    const failed = expect(first).rejects.toMatchObject({ name: 'ApiError', message: '服务暂不可用', status: 503 })
    pending[0].resolve(jsonResponse({ message: '服务暂不可用' }, 503))
    await failed
    const retry = request('/api/account')
    expect(fetch).toHaveBeenCalledTimes(2)
    pending[1].resolve(jsonResponse({ ok: true }))
    await retry
  })
})

describe('HTTP 取消与超时', () => {
  it('已中止的 signal 直接返回取消，不发 fetch 或创建计时器', async () => {
    const ctrl = new AbortController()
    ctrl.abort()
    await expect(request('/api/account', { signal: ctrl.signal })).rejects.toMatchObject({
      name: 'ApiError',
      message: '请求已取消',
      status: 0,
    })
    expect(fetch).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('带同一个 signal 的调用也独立，取消单个消费者不影响共享读', async () => {
    const firstCtrl = new AbortController()
    const secondCtrl = new AbortController()
    const first = request('/api/account', { signal: firstCtrl.signal })
    const second = request('/api/account', { signal: secondCtrl.signal })
    const third = request('/api/account', { signal: secondCtrl.signal })
    const shared = request('/api/account')
    const otherShared = request('/api/account')
    expect(fetch).toHaveBeenCalledTimes(4)
    const removed = vi.spyOn(firstCtrl.signal, 'removeEventListener')
    const cancelled = expect(first).rejects.toMatchObject({ message: '请求已取消', status: 0 })
    firstCtrl.abort()
    pending.slice(1).forEach((call) => call.resolve(jsonResponse({ ok: true })))
    await cancelled
    expect(await Promise.all([second, third, shared, otherShared])).toEqual(
      Array.from({ length: 4 }, () => ({ ok: true })),
    )
    expect(removed).toHaveBeenCalledWith('abort', expect.any(Function))
    expect(pending.slice(1).every((call) => !call.init?.signal?.aborted)).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('共享读的超时可读，清表后能够重新请求', async () => {
    const readers = [request('/api/account', { timeoutMs: 50 }), request('/api/account', { timeoutMs: 50 })]
    const failed = Promise.all(
      readers.map((reader) => expect(reader).rejects.toMatchObject({ message: '请求超时', status: 0 })),
    )
    expect(fetch).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(50)
    await failed
    expect(vi.getTimerCount()).toBe(0)

    const retry = request('/api/account', { timeoutMs: 50 })
    expect(fetch).toHaveBeenCalledTimes(2)
    pending[1].resolve(jsonResponse({ ok: true }))
    await retry
  })

  it('响应头已到达但响应体仍在读取时取消，丢弃迟到的 401 并释放监听', async () => {
    const ctrl = new AbortController()
    const text = deferred<string>()
    const reading = deferred<void>()
    const response = jsonResponse({ message: '迟到失效' }, 401)
    vi.spyOn(response, 'text').mockImplementation(() => {
      reading.resolve()
      return text.promise
    })
    const reader = request('/api/account', { signal: ctrl.signal })
    const failed = expect(reader).rejects.toMatchObject({ message: '请求已取消' })
    pending[0].resolve(response)
    await reading.promise
    ctrl.abort()
    text.resolve('{"message":"迟到失效"}')
    await failed
    expect(unauthorized).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('超时先发生后调用方再取消，保留首次中止原因并移除监听', async () => {
    const ctrl = new AbortController()
    const removed = vi.spyOn(ctrl.signal, 'removeEventListener')
    const reader = request('/api/account', { timeoutMs: 50, signal: ctrl.signal })
    const failed = expect(reader).rejects.toMatchObject({ message: '请求超时' })
    vi.advanceTimersByTime(50)
    ctrl.abort()
    await failed
    expect(removed).toHaveBeenCalledWith('abort', expect.any(Function))
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('HTTP 会话与响应兼容', () => {
  it('当前会话的共享 401 只调用一次统一处理器', async () => {
    const readers = [request('/api/account'), request('/api/account')]
    const failed = Promise.all(readers.map((reader) => expect(reader).rejects.toBeInstanceOf(ApiError)))
    pending[0].resolve(jsonResponse({ message: '令牌已过期' }, 401))
    await failed
    expect(unauthorized).toHaveBeenCalledTimes(1)
  })

  it.each(['token', 'endpoint'] as const)('旧 %s 的迟到 401 不会注销新会话', async (changed) => {
    const reader = request('/api/account')
    const failed = expect(reader).rejects.toMatchObject({ status: 401 })
    if (changed === 'token') setMemoryToken('session-b')
    else setHost('192.0.2.1')
    pending[0].resolve(jsonResponse({ message: '旧会话失效' }, 401))
    await failed
    expect(unauthorized).not.toHaveBeenCalled()
  })

  it('登录失败与匿名请求 401 保留错误，但不调用全局登出', async () => {
    const login = request('/api/v1/login', { method: 'POST', json: { username: 'owner' } })
    setMemoryToken('')
    const anonymous = request('/api/account')
    const failed = Promise.all(
      [login, anonymous].map((reader) => expect(reader).rejects.toMatchObject({ status: 401 })),
    )
    pending.forEach((call) => call.resolve(jsonResponse({ message: '未登录' }, 401)))
    await failed
    expect(unauthorized).not.toHaveBeenCalled()
  })

  it('保持文本与空响应兼容，JSON 写入采用当前内存令牌', async () => {
    const textReader = request('/health')
    const emptyReader = request('/api/empty')
    const write = request('/api/config', { method: 'PUT', json: { value: 1 }, headers: { authorization: 'old' } })
    pending[0].resolve(new Response('healthy'))
    pending[1].resolve(new Response(null, { status: 204 }))
    pending[2].resolve(jsonResponse({ ok: true }))
    expect(await Promise.all([textReader, emptyReader, write])).toEqual(['healthy', null, { ok: true }])
    expect(new Headers(pending[2].init?.headers).get('Content-Type')).toBe('application/json')
    expect(new Headers(pending[2].init?.headers).get('Authorization')).toBe('Bearer session-a')
    expect(pending[2].init?.body).toBe('{"value":1}')
    expect(vi.getTimerCount()).toBe(0)
  })
})
