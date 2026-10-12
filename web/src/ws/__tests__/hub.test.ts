/**
 * WS hub 单测（P6）：假 WebSocket + fake timers 验证——
 * 首连订阅帧、断线序号续传（resume）、回放去重、指数退避重连顺序、
 * 动态订阅引用计数、坏帧容错、sessionStorage 序号持久化。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createWsHub, type HubDeps } from '@/ws/hub'
import type { WsPushMessage } from '@/ws/topics'

class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static instances: FakeWebSocket[] = []

  readyState = 0
  url: string
  sent: string[] = []
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  send(data: string) {
    this.sent.push(data)
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED
    this.onclose?.()
  }

  /* --- 测试侧驱动 --- */
  serverOpen() {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.()
  }

  serverMessage(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }

  serverRaw(data: string) {
    this.onmessage?.({ data })
  }

  serverClose() {
    this.readyState = FakeWebSocket.CLOSED
    this.onclose?.()
  }

  static last(): FakeWebSocket {
    const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
    if (!ws) throw new Error('没有创建 WebSocket')
    return ws
  }

  static frames(ws: FakeWebSocket): Array<Record<string, unknown>> {
    return ws.sent.map((s) => JSON.parse(s) as Record<string, unknown>)
  }
}

function makeDeps(over: Partial<HubDeps> = {}): HubDeps {
  return {
    wsUrl: () => 'ws://test:2003/ws',
    checkHealth: async () => true,
    askConfirm: async () => true,
    deskNotify: async () => false,
    ...over,
  }
}

const SEQ_KEY = 'nt_ws_seq'

beforeEach(() => {
  FakeWebSocket.instances = []
  sessionStorage.clear()
  vi.stubGlobal('WebSocket', FakeWebSocket)
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('ws/hub 连接与订阅', () => {
  it('startLink 建连，open 后发出含常驻主题的订阅帧', () => {
    const hub = createWsHub(makeDeps())
    hub.startLink()
    const ws = FakeWebSocket.last()
    expect(ws.url).toBe('ws://test:2003/ws')
    ws.serverOpen()
    const frame = FakeWebSocket.frames(ws)[0]
    expect(frame).toMatchObject({ op: 'sub', resume: {} })
    expect(frame.topics).toEqual(expect.arrayContaining(['signal', 'position', 'alert']))
    hub.stopLink()
  })

  it('动态订阅：首次订阅重发全集，引用计数归零才退订', () => {
    const hub = createWsHub(makeDeps())
    hub.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    expect(ws.sent.length).toBe(1)

    const off1 = hub.subscribe('market')
    expect(ws.sent.length).toBe(2)
    expect(FakeWebSocket.frames(ws)[1].topics).toContain('market')

    const off2 = hub.subscribe('market')
    expect(ws.sent.length).toBe(2) // 引用计数 +1，不发帧
    expect(hub.desiredTopics()).toContain('market')

    off1()
    expect(ws.sent.length).toBe(2) // 还剩一个引用
    expect(hub.desiredTopics()).toContain('market')

    off2()
    expect(ws.sent.length).toBe(3)
    expect(FakeWebSocket.frames(ws)[2].topics).not.toContain('market')
    hub.stopLink()
  })
})

describe('ws/hub 序号续传与去重', () => {
  it('收帧记录 seq；重连 open 帧携带 resume；回放旧 seq 被丢弃', () => {
    const hub = createWsHub(makeDeps())
    const pushed: WsPushMessage[] = []
    hub.onPush((m) => pushed.push(m))
    hub.startLink()

    const ws1 = FakeWebSocket.last()
    ws1.serverOpen()
    ws1.serverMessage({ topic: 'signal', seq: 1, data: { a: 1 } })
    ws1.serverMessage({ topic: 'signal', seq: 2, data: { a: 2 } })
    ws1.serverMessage({ topic: 'position', seq: 7, data: {} })

    // 序号持久化到 sessionStorage
    expect(JSON.parse(sessionStorage.getItem(SEQ_KEY) || '{}')).toEqual({ signal: 2, position: 7 })

    ws1.serverClose()
    return vi.advanceTimersByTimeAsync(1000).then(() => {
      const ws2 = FakeWebSocket.last()
      expect(ws2).not.toBe(ws1)
      ws2.serverOpen()
      const frame = FakeWebSocket.frames(ws2)[0]
      expect(frame.resume).toEqual({ signal: 2, position: 7 })

      const seen = pushed.length
      ws2.serverMessage({ topic: 'signal', seq: 2, data: { a: 'dup' } })
      expect(pushed.length).toBe(seen) // 去重：不大于已见值
      ws2.serverMessage({ topic: 'signal', seq: 3, data: { a: 3 } })
      expect(pushed.length).toBe(seen + 1)
      hub.stopLink()
    })
  })

  it('同会话新 hub 从 sessionStorage 恢复序号', async () => {
    sessionStorage.setItem(SEQ_KEY, JSON.stringify({ alert: 42 }))
    const hub = createWsHub(makeDeps())
    hub.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen()
    expect(FakeWebSocket.frames(ws)[0].resume).toEqual({ alert: 42 })
    hub.stopLink()
  })

  it('坏 JSON 帧静默忽略', () => {
    const hub = createWsHub(makeDeps())
    const pushed: WsPushMessage[] = []
    hub.onPush((m) => pushed.push(m))
    hub.startLink()
    const ws = FakeWebSocket.last()
    ws.serverOpen() // open 会推一条 resume 通知
    const seen = pushed.length
    ws.serverRaw('not-json{{{')
    expect(pushed.length).toBe(seen) // 坏帧不产生新消息
    expect(pushed.every((m) => m.topic !== 'not-json{{{')).toBe(true)
    hub.stopLink()
  })
})

describe('ws/hub 重连退避', () => {
  it('指数退避 1s→2s→4s，open 成功后重置回 1s', async () => {
    const hub = createWsHub(makeDeps())
    hub.startLink()
    expect(FakeWebSocket.instances.length).toBe(1)

    FakeWebSocket.last().serverClose()
    await vi.advanceTimersByTimeAsync(999)
    expect(FakeWebSocket.instances.length).toBe(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeWebSocket.instances.length).toBe(2) // 1s 后第一次重连

    FakeWebSocket.last().serverClose()
    await vi.advanceTimersByTimeAsync(1999)
    expect(FakeWebSocket.instances.length).toBe(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeWebSocket.instances.length).toBe(3) // 再 2s

    FakeWebSocket.last().serverClose()
    await vi.advanceTimersByTimeAsync(3999)
    expect(FakeWebSocket.instances.length).toBe(3)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeWebSocket.instances.length).toBe(4) // 再 4s

    FakeWebSocket.last().serverOpen() // 成功 → retry 重置
    FakeWebSocket.last().serverClose()
    await vi.advanceTimersByTimeAsync(1000)
    expect(FakeWebSocket.instances.length).toBe(5) // 回到 1s
    hub.stopLink()
  })
})
