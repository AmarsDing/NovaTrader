import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createWsHub, type HubDeps, type WsHub } from '@/ws/hub'
import { BASE_TOPICS, type WsPushMessage, type WsTopic } from '@/ws/topics'

class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static instances: FakeWebSocket[] = []
  readyState = 0
  sent: string[] = []
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null
  constructor(public url: string) {
    FakeWebSocket.instances.push(this)
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {
    this.readyState = 3
    this.onclose?.()
  }
  serverOpen() {
    this.readyState = 1
    this.onopen?.()
  }
  serverMessage(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }
  serverRaw(data: string) {
    this.onmessage?.({ data })
  }
  serverClose() {
    this.readyState = 3
    this.onclose?.()
  }
  static last() {
    return FakeWebSocket.instances.at(-1)!
  }
  static frames(ws: FakeWebSocket) {
    return ws.sent.map((s) => JSON.parse(s) as Record<string, unknown>)
  }
}
const CURSOR_KEY = 'nt_ws_cursor_v2'
const hubs: WsHub[] = []
function makeDeps(over: Partial<HubDeps> = {}): HubDeps {
  return {
    wsUrl: () => 'ws://test:2003/ws',
    checkHealth: async () => true,
    askConfirm: async () => true,
    deskNotify: async () => false,
    ...over,
  }
}
function makeHub(over: Partial<HubDeps> = {}) {
  const h = createWsHub(makeDeps(over))
  hubs.push(h)
  return h
}
async function flush() {
  for (let i = 0; i < 20; i++) await Promise.resolve()
}
function id(ws: FakeWebSocket) {
  return Number(
    FakeWebSocket.frames(ws)
      .reverse()
      .find((f) => f.op === 'sub' || f.op === 'resync')?.request_id,
  )
}
function cursors(topics: readonly string[], head = 0, status = 'resumed', after = head, reason = '') {
  return Object.fromEntries(
    topics.map((t) => [t, { head, oldest: head > 500 ? head - 499 : 1, after, status, reason }]),
  )
}
function control(
  ws: FakeWebSocket,
  topic: string,
  topics: Record<string, unknown>,
  epoch = 'epoch-A',
  request = id(ws),
) {
  ws.serverMessage({ topic, server_epoch: epoch, request_id: request, data: { topics } })
}
function data(
  ws: FakeWebSocket,
  n: number,
  topic = 'signal',
  payload: unknown = { n },
  epoch = 'epoch-A',
  metadata: unknown = undefined,
) {
  ws.serverMessage({ topic, topic_seq: n, seq: n, server_epoch: epoch, data: payload, metadata })
}
async function ready(hub: WsHub, ws: FakeWebSocket, epoch = 'epoch-A') {
  control(ws, 'sub_ack', cursors(hub.desiredTopics(), 0, 'resync_required', 0, 'snapshot_required'), epoch)
  control(ws, 'resync_required', cursors(hub.desiredTopics(), 0, 'resync_required', 0, 'snapshot_required'), epoch)
  await flush()
  control(ws, 'resync_ack', cursors(hub.desiredTopics()), epoch)
  await flush()
}
function register(hub: WsHub, topics: readonly WsTopic[] = BASE_TOPICS) {
  return hub.onResync(topics, async () => {})
}
beforeEach(() => {
  FakeWebSocket.instances = []
  sessionStorage.clear()
  vi.stubGlobal('WebSocket', FakeWebSocket)
  vi.useFakeTimers()
})
afterEach(() => {
  hubs.splice(0).forEach((h) => h.stopLink())
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('epoch / REST watermark recovery', () => {
  it('first connection requires a snapshot; no handler remains unknown and does not ack', async () => {
    const h = makeHub()
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    expect(FakeWebSocket.frames(ws)[0]).toMatchObject({
      op: 'resync',
      topics: ['signal', 'position', 'alert'],
      resume: {},
      server_epoch: '',
    })
    control(ws, 'sub_ack', cursors(BASE_TOPICS, 0, 'resync_required', 0, 'epoch_required'))
    control(ws, 'resync_required', cursors(BASE_TOPICS, 0, 'resync_required', 0, 'epoch_required'))
    await flush()
    expect(h.link.sync).toBe('required')
    expect(h.isLive()).toBe(false)
    expect(FakeWebSocket.frames(ws).some((f) => f.op === 'resync_ack')).toBe(false)
  })
  it('waits for the declared REST handler, buffers the recovery window, and only then commits a contiguous replay', async () => {
    const h = makeHub()
    let release!: () => void
    const gate = new Promise<void>((r) => {
      release = r
    })
    h.onResync(['signal'], async () => gate)
    h.onResync(['position', 'alert'], async () => {})
    const seen: WsPushMessage[] = []
    h.onPush((m) => {
      seen.push(m)
    })
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    control(ws, 'sub_ack', cursors(BASE_TOPICS, 0, 'resync_required', 0, 'snapshot_required'))
    control(ws, 'resync_required', cursors(BASE_TOPICS, 0, 'resync_required', 0, 'snapshot_required'))
    await flush()
    data(ws, 1)
    expect(seen.some((m) => m.topic === 'signal')).toBe(false)
    expect(
      FakeWebSocket.frames(ws).some((f) => f.op === 'resync_ack' && Object.hasOwn(f.resume as object, 'signal')),
    ).toBe(false)
    release()
    await flush()
    control(ws, 'resync_ack', {
      signal: { head: 1, oldest: 1, after: 0, status: 'resumed' },
      ...cursors(['position', 'alert']),
    })
    await flush()
    expect(seen.filter((m) => m.topic === 'signal').map((m) => m.seq)).toEqual([1])
    expect(h.link.sync).toBe('ready')
    expect(h.isLive()).toBe(true)
  })
  it('admin restart resets an old seq=1000; source metadata and payload survive', async () => {
    sessionStorage.setItem(
      CURSOR_KEY,
      JSON.stringify({ endpoint: 'ws://test:2003/ws', epoch: 'old-admin', seq: { signal: 1000 } }),
    )
    const h = makeHub()
    register(h)
    const seen: WsPushMessage[] = []
    h.onPush((m) => {
      seen.push(m)
    })
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    expect(FakeWebSocket.frames(ws)[0].resume).toEqual({ signal: 1000 })
    await ready(h, ws, 'new-admin')
    data(ws, 1, 'signal', { signal_id: 's1', stock_code: '600000' }, 'new-admin', {
      event_id: 'e1',
      trace_id: 'trace',
      entity_version: 4,
      source_time: '2026-10-15T01:00:00Z',
    })
    await flush()
    expect(seen.find((m) => m.topic === 'signal')).toMatchObject({
      seq: 1,
      serverEpoch: 'new-admin',
      data: { signal_id: 's1', stock_code: '600000' },
      metadata: { event_id: 'e1', entity_version: 4 },
    })
    expect(JSON.parse(sessionStorage.getItem(CURSOR_KEY)!).seq.signal).toBe(1)
  })
  it('recovery ring expiry restarts REST at the new head, without acknowledging stale data', async () => {
    const h = makeHub()
    const handler = vi.fn(async () => {})
    h.onResync(BASE_TOPICS, handler)
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    await ready(h, ws)
    const oldCount = handler.mock.calls.length
    control(ws, 'resync_ack', {
      signal: { head: 501, oldest: 2, after: 0, status: 'resync_required', reason: 'recovery_window_expired' },
    })
    control(ws, 'resync_required', {
      signal: { head: 501, oldest: 2, after: 0, status: 'resync_required', reason: 'recovery_window_expired' },
    })
    await flush()
    expect(handler.mock.calls.length).toBe(oldCount + 1)
    expect(h.isLive()).toBe(false)
    expect(FakeWebSocket.frames(ws).at(-1)).toMatchObject({ op: 'resync_ack', resume: { signal: 501 } })
    control(ws, 'resync_ack', { signal: { head: 501, oldest: 2, after: 501, status: 'resumed' } })
    await flush()
    expect(h.isLive()).toBe(true)
  })
  it('failed snapshot remains required; adding a usable handler can retry', async () => {
    const h = makeHub()
    const off = h.onResync(BASE_TOPICS, async () => {
      throw new Error('REST offline')
    })
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    control(ws, 'sub_ack', cursors(BASE_TOPICS, 0, 'resync_required'))
    control(ws, 'resync_required', cursors(BASE_TOPICS, 0, 'resync_required'))
    await flush()
    expect(h.link.sync).toBe('required')
    expect(h.link.error).toContain('REST offline')
    expect(FakeWebSocket.frames(ws).some((f) => f.op === 'resync_ack')).toBe(false)
    off()
    register(h)
    await flush()
    expect(FakeWebSocket.frames(ws).filter((f) => f.op === 'resync_ack')).toHaveLength(3)
  })
  it('retries a failed REST snapshot on the five-second health cadence without reconnect flooding', async () => {
    const h = makeHub()
    let attempts = 0
    h.onResync(['signal'], async () => {
      if (++attempts === 1) throw new Error('temporary failure')
    })
    h.onResync(['position', 'alert'], async () => {})
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    control(ws, 'sub_ack', cursors(BASE_TOPICS, 0, 'resync_required'))
    control(ws, 'resync_required', cursors(BASE_TOPICS, 0, 'resync_required'))
    await flush()
    control(ws, 'resync_ack', cursors(['position', 'alert']))
    expect(h.link.sync).toBe('required')
    await vi.advanceTimersByTimeAsync(4999)
    expect(attempts).toBe(1)
    await vi.advanceTimersByTimeAsync(1)
    await flush()
    expect(attempts).toBe(2)
    expect(FakeWebSocket.instances).toHaveLength(1)
    control(ws, 'resync_ack', cursors(['signal']))
    await flush()
    expect(h.isLive()).toBe(true)
  })
})

describe('contiguous delivery and windows', () => {
  it('reconnect sends the committed cursor; duplicates and old out-of-order frames do not apply twice', async () => {
    const h = makeHub()
    register(h)
    const seen: number[] = []
    h.onPush((m) => {
      if (m.topic === 'signal') seen.push(m.seq!)
    })
    h.startLink()
    const first = FakeWebSocket.last()
    first.serverOpen()
    await ready(h, first)
    data(first, 1)
    data(first, 2)
    await flush()
    first.serverClose()
    await vi.advanceTimersByTimeAsync(1000)
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    expect(FakeWebSocket.frames(ws)[0]).toMatchObject({
      op: 'sub',
      server_epoch: 'epoch-A',
      resume: { signal: 2, position: 0, alert: 0 },
    })
    control(ws, 'sub_ack', {
      ...cursors(['position', 'alert']),
      signal: { head: 3, oldest: 1, after: 2, status: 'resumed' },
    })
    data(ws, 2)
    data(ws, 1)
    data(ws, 3)
    await flush()
    expect(seen).toEqual([1, 2, 3])
    expect(h.link.sync).toBe('ready')
  })
  it('gap forces explicit resync rather than silently skipping to the largest sequence', async () => {
    const h = makeHub()
    register(h)
    const fn = vi.fn()
    h.onPush(fn)
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    await ready(h, ws)
    fn.mockClear()
    data(ws, 2)
    await flush()
    expect(fn).not.toHaveBeenCalled()
    expect(FakeWebSocket.frames(ws).at(-1)).toMatchObject({ op: 'resync' })
    expect(h.isLive()).toBe(false)
  })
  it('async consumer completion controls committed seq and replay readiness', async () => {
    const h = makeHub()
    register(h)
    let release!: () => void
    const gate = new Promise<void>((r) => {
      release = r
    })
    h.onPush(async (m) => {
      if (m.topic === 'signal') await gate
    })
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    await ready(h, ws)
    control(ws, 'sub_ack', {
      ...cursors(['position', 'alert']),
      signal: { head: 1, oldest: 1, after: 0, status: 'resumed' },
    })
    data(ws, 1)
    await flush()
    expect(JSON.parse(sessionStorage.getItem(CURSOR_KEY)!).seq.signal).toBe(0)
    expect(h.isLive()).toBe(false)
    release()
    await flush()
    expect(JSON.parse(sessionStorage.getItem(CURSOR_KEY)!).seq.signal).toBe(1)
    expect(h.isLive()).toBe(true)
  })
  it('consumer failure does not commit a cursor and triggers REST recovery', async () => {
    const h = makeHub()
    register(h)
    h.onPush(async (m) => {
      if (m.topic === 'signal') throw new Error('apply failed')
    })
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    await ready(h, ws)
    data(ws, 1)
    await flush()
    expect(FakeWebSocket.frames(ws).at(-1)?.op).toBe('resync')
    expect(JSON.parse(sessionStorage.getItem(CURSOR_KEY)!).seq.signal).toBe(0)
  })
  it('an unanswered desktop notification permission prompt cannot block the next alert delivery', async () => {
    const notify = vi.fn(() => new Promise<boolean>(() => {}))
    const h = makeHub({ deskNotify: notify })
    register(h)
    const seen: number[] = []
    h.onPush((m) => { if (m.topic === 'alert') seen.push(m.seq!) })
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    await ready(h, ws)
    data(ws, 1, 'alert', { title: 'first alert' })
    await flush()
    expect(notify).toHaveBeenCalledTimes(1)
    data(ws, 2, 'alert', { kind: 'kill-switch', active: true })
    await flush()
    expect(seen).toEqual([1, 2])
    expect(JSON.parse(sessionStorage.getItem(CURSOR_KEY)!).seq.alert).toBe(2)
    expect(h.isLive()).toBe(true)
  })
  it('rejected notification and confirmation prompts are handled without resyncing applied events', async () => {
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      const h = makeHub({
        deskNotify: async () => { throw new Error('desktop unavailable') },
        askConfirm: async () => { throw new Error('prompt unavailable') },
      })
      register(h)
      const seen: number[] = []
      h.onPush((m) => { if (m.topic === 'alert') seen.push(m.seq!) })
      h.startLink()
      const ws = FakeWebSocket.last()
      ws.serverOpen()
      await ready(h, ws)
      const sentBefore = ws.sent.length
      data(ws, 1, 'alert', { kind: 'confirm', account: 'LIVE', title: 'confirmation required' })
      data(ws, 2, 'alert', { kind: 'kill-switch', active: true })
      await flush()
      expect(seen).toEqual([1, 2])
      expect(warning).toHaveBeenCalledWith('[ws] 桌面通知调用失败')
      expect(warning).toHaveBeenCalledWith('[ws] 交易确认提示调用失败')
      expect(ws.sent).toHaveLength(sentBefore)
      expect(JSON.parse(sessionStorage.getItem(CURSOR_KEY)!).seq.alert).toBe(2)
      expect(h.isLive()).toBe(true)
    } finally {
      warning.mockRestore()
    }
  })
  it('dynamic unsubscribe aborts old recovery and ignores a delayed ACK from the previous request', async () => {
    const h = makeHub()
    register(h)
    let release!: () => void
    h.onResync(
      ['market'],
      async () =>
        new Promise<void>((r) => {
          release = r
        }),
    )
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    await ready(h, ws)
    const off = h.subscribe('market')
    const old = id(ws)
    control(ws, 'sub_ack', {
      ...cursors(BASE_TOPICS),
      market: { head: 5, oldest: 1, after: 0, status: 'resync_required' },
    })
    control(ws, 'resync_required', { market: { head: 5, oldest: 1, after: 0, status: 'resync_required' } })
    await flush()
    off()
    release()
    await flush()
    control(ws, 'resync_ack', { market: { head: 5, oldest: 1, after: 5, status: 'resumed' } }, 'outdated-epoch', old)
    expect(h.desiredTopics()).not.toContain('market')
    expect(
      FakeWebSocket.frames(ws).filter((f) => f.op === 'resync_ack' && Object.hasOwn(f.resume as object, 'market')),
    ).toHaveLength(0)
  })
  it('copied session cursors in a second window do not replace its own REST snapshot', async () => {
    const one = makeHub()
    register(one)
    one.startLink()
    const first = FakeWebSocket.last()
    first.serverOpen()
    await ready(one, first)
    data(first, 1)
    await flush()
    const two = makeHub()
    const handler = vi.fn(async () => {})
    two.onResync(BASE_TOPICS, handler)
    two.startLink()
    const second = FakeWebSocket.last()
    second.serverOpen()
    expect(FakeWebSocket.frames(second)[0]).toMatchObject({ op: 'resync', resume: { signal: 1 } })
    expect(two.isLive()).toBe(false)
    await ready(two, second)
    expect(handler).toHaveBeenCalledTimes(3)
    expect(one.isLive()).toBe(true)
    expect(two.isLive()).toBe(true)
  })
  it('replay batches retain every frame but do not reopen historical LIVE confirmations or desktop notifications', async () => {
    const ask = vi.fn(async () => true),
      notify = vi.fn(async () => false)
    const h = makeHub({ askConfirm: ask, deskNotify: notify })
    register(h)
    const seen: WsPushMessage[] = []
    h.onPush((m) => {
      seen.push(m)
    })
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    await ready(h, ws)
    control(ws, 'sub_ack', {
      ...cursors(['signal', 'position']),
      alert: { head: 2, oldest: 1, after: 0, status: 'resumed' },
    })
    ws.serverMessage({
      topic: 'replay_batch',
      request_id: id(ws),
      server_epoch: 'epoch-A',
      data: {
        frames: [1, 2].map((n) => ({
          topic: 'alert',
          topic_seq: n,
          server_epoch: 'epoch-A',
          metadata: { event_id: `e${n}` },
          data: { kind: 'confirm', account: 'LIVE', title: 'old alert' },
        })),
      },
    })
    await flush()
    expect(seen.filter((m) => m.topic === 'alert')).toHaveLength(2)
    expect(ask).not.toHaveBeenCalled()
    expect(notify).not.toHaveBeenCalled()
    expect(h.isLive()).toBe(true)
  })
})

describe('lifecycle and bounded failure', () => {
  it('reference counts and repeated unsubscribe remain idempotent', () => {
    const h = makeHub()
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    const off1 = h.subscribe('market'),
      off2 = h.subscribe('market')
    expect(ws.sent).toHaveLength(2)
    off1()
    off1()
    expect(ws.sent).toHaveLength(2)
    off2()
    expect(ws.sent).toHaveLength(3)
    expect(h.desiredTopics()).not.toContain('market')
  })
  it('logout clears the cursor; another backend does not inherit old cursors', async () => {
    sessionStorage.setItem(
      CURSOR_KEY,
      JSON.stringify({ endpoint: 'ws://other:2003/ws', epoch: 'old', seq: { signal: 500 } }),
    )
    const h = makeHub()
    register(h)
    h.startLink()
    let ws = FakeWebSocket.last()
    ws.serverOpen()
    expect(FakeWebSocket.frames(ws)[0].resume).toEqual({})
    await ready(h, ws)
    data(ws, 1)
    await flush()
    h.stopLink()
    expect(sessionStorage.getItem(CURSOR_KEY)).toBeNull()
    h.startLink()
    ws = FakeWebSocket.last()
    ws.serverOpen()
    expect(FakeWebSocket.frames(ws)[0]).toMatchObject({ op: 'resync', resume: {}, server_epoch: '' })
  })
  it('invalid JSON and unversioned old data cannot advance the new cursor', async () => {
    const h = makeHub()
    register(h)
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    await ready(h, ws)
    ws.serverRaw('not-json')
    ws.serverMessage({ topic: 'signal', seq: 1000, data: {} })
    await flush()
    expect(JSON.parse(sessionStorage.getItem(CURSOR_KEY)!).seq.signal).toBe(0)
  })
  it('unknown control status or a claimed future resume cannot mark the link ready', async () => {
    const h = makeHub()
    register(h)
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    control(ws, 'sub_ack', cursors(BASE_TOPICS, 0, 'future-status'))
    await flush()
    expect(h.isLive()).toBe(false)
    control(ws, 'sub_ack', cursors(BASE_TOPICS, 0, 'resumed', 1000))
    await flush()
    expect(h.isLive()).toBe(false)
    expect(JSON.parse(sessionStorage.getItem(CURSOR_KEY)!).seq.signal).toBeUndefined()
  })
  it('a recovery buffer overflow restarts the handshake and stays non-live', async () => {
    const h = makeHub()
    h.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    control(ws, 'sub_ack', cursors(BASE_TOPICS, 0, 'resync_required'))
    control(ws, 'resync_required', cursors(BASE_TOPICS, 0, 'resync_required'))
    for (let i = 1; i <= 1001; i++) data(ws, i)
    await flush()
    expect(FakeWebSocket.frames(ws)).toHaveLength(2)
    expect(FakeWebSocket.frames(ws).at(-1)?.op).toBe('resync')
    expect(h.isLive()).toBe(false)
  })
  it('reconnect backoff is 1s/2s/4s and stop cancels a scheduled reconnect', async () => {
    const h = makeHub()
    h.startLink()
    FakeWebSocket.last().serverClose()
    await vi.advanceTimersByTimeAsync(999)
    expect(FakeWebSocket.instances).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeWebSocket.instances).toHaveLength(2)
    FakeWebSocket.last().serverClose()
    await vi.advanceTimersByTimeAsync(2000)
    expect(FakeWebSocket.instances).toHaveLength(3)
    FakeWebSocket.last().serverClose()
    await vi.advanceTimersByTimeAsync(4000)
    expect(FakeWebSocket.instances).toHaveLength(4)
    FakeWebSocket.last().serverOpen()
    FakeWebSocket.last().serverClose()
    h.stopLink()
    await vi.advanceTimersByTimeAsync(15000)
    expect(FakeWebSocket.instances).toHaveLength(4)
  })
})
