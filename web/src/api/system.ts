import { request } from '@/api/http'
import { httpBase } from '@/utils/endpoint'
import { num, pick } from '@/utils/format'

export async function healthz(): Promise<boolean> {
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), 4000)
  try {
    const res = await fetch(`${httpBase()}/healthz`, { cache: 'no-store', signal: ctrl.signal })
    return res.ok
  } catch {
    return false
  } finally {
    clearTimeout(timer)
  }
}

/** GET /api/ops/time（P2-10）响应。epochMs 是漂移补偿唯一运算源，非有限数按合同违约处理（不猜、不补）。 */
export interface ServerTime {
  serverTime: string
  epochMs: number
}

export async function fetchServerTime(): Promise<ServerTime> {
  const raw = await request('/api/ops/time', { timeoutMs: 2000 })
  return {
    serverTime: pick<string>(raw, 'serverTime', 'server_time') || '',
    epochMs: num(pick(raw, 'epochMs', 'epoch_ms')) ?? NaN,
  }
}
