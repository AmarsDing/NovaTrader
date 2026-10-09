/**
 * admin（2003）WebSocket 链路。
 * - 健康检查 5s 轮询 + WS 指数退避重连（1s → 15s）；
 * - 断线续传：每主题记录 seq，重连时携带 resume，服务端回放环形缓冲；
 * - 按主题去重：回放帧若 seq 不大于已见值则丢弃，避免重复套用；
 * - 动态订阅：subscribe('market'|'brain') 引用计数，server 端 sub 是整体替换，
 *   每次增删都重发当前期望全集；signal/position/alert 常驻。
 */
import { reactive } from 'vue'
import { healthz } from '@/api/system'
import { askConfirm } from '@/store/confirm'
import { wsUrl } from '@/utils/endpoint'
import { deskNotify } from '@/utils/notify'
import { BASE_TOPICS, type WsEnvelope, type WsPushMessage, type WsTopic } from './topics'

export type { WsPushMessage, WsTopic } from './topics'
export { ALL_TOPICS, BASE_TOPICS } from './topics'

export interface HubDeps {
  wsUrl: () => string
  checkHealth: () => Promise<boolean>
  askConfirm: (order: Record<string, unknown>, local: boolean) => Promise<unknown>
  deskNotify: (title: string, body: string) => Promise<boolean>
}

export interface WsHub {
  link: { health: string; ws: string; lastAt: Date | null; error: string }
  isLive: () => boolean
  onPush: (fn: (msg: WsPushMessage) => void) => () => void
  /** 按需订阅主题；返回取消函数。取消后引用归零才真正退订。 */
  subscribe: (topic: WsTopic) => () => void
  startLink: () => void
  stopLink: () => void
  restartLink: () => void
  /** 当前期望订阅全集（含常驻主题）。 */
  desiredTopics: () => string[]
}

const SEQ_KEY = 'nt_ws_seq'

export function createWsHub(deps: HubDeps): WsHub {
  const link = reactive({ health: 'unknown', ws: 'idle', lastAt: null as Date | null, error: '' })

  const listeners = new Set<(msg: WsPushMessage) => void>()
  const counters = new Map<WsTopic, number>()
  const seq: Record<string, number> = {}

  let socket: WebSocket | null = null
  let healthTimer: ReturnType<typeof setInterval> | null = null
  let retry = 1000
  let stopped = true

  function loadSeq(): Record<string, number> {
    try {
      const parsed = JSON.parse(sessionStorage.getItem(SEQ_KEY) || '{}') as Record<string, unknown>
      const out: Record<string, number> = {}
      Object.entries(parsed).forEach(([key, value]) => {
        const n = Number(value)
        if (Number.isFinite(n) && n > 0) out[key] = n
      })
      return out
    } catch {
      return {}
    }
  }

  function saveSeq() {
    try {
      sessionStorage.setItem(SEQ_KEY, JSON.stringify(seq))
    } catch {
      /* sessionStorage 不可用时静默降级 */
    }
  }

  function desiredTopics(): string[] {
    const set = new Set<string>(BASE_TOPICS)
    counters.forEach((count, topic) => {
      if (count > 0) set.add(topic)
    })
    return [...set]
  }

  function syncTopics() {
    if (!socket || socket.readyState !== WebSocket.OPEN) return
    socket.send(JSON.stringify({ op: 'sub', topics: desiredTopics() }))
  }

  function subscribe(topic: WsTopic): () => void {
    counters.set(topic, (counters.get(topic) || 0) + 1)
    if ((counters.get(topic) || 0) === 1) syncTopics()
    return () => {
      const left = (counters.get(topic) || 1) - 1
      if (left > 0) {
        counters.set(topic, left)
      } else {
        counters.delete(topic)
        syncTopics()
      }
    }
  }

  function isLive() {
    return link.health === 'ok' && link.ws === 'open'
  }

  function onPush(fn: (msg: WsPushMessage) => void): () => void {
    listeners.add(fn)
    return () => listeners.delete(fn)
  }

  async function tickHealth() {
    link.health = (await deps.checkHealth()) ? 'ok' : 'down'
  }

  function connect() {
    if (stopped) return
    if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) return
    link.ws = 'connecting'
    let ws: WebSocket
    try {
      ws = new WebSocket(deps.wsUrl())
    } catch {
      link.ws = 'closed'
      link.error = 'WebSocket 地址无效'
      schedule()
      return
    }
    socket = ws
    ws.onopen = () => {
      link.ws = 'open'
      link.error = ''
      retry = 1000
      const resume: Record<string, number> = {}
      Object.entries(seq).forEach(([key, value]) => {
        if (value) resume[key] = value
      })
      ws.send(JSON.stringify({ op: 'sub', topics: desiredTopics(), resume }))
      listeners.forEach((fn) => fn({ topic: 'resume', data: null, seq: null }))
    }
    ws.onmessage = (event) => {
      link.lastAt = new Date()
      let msg: WsEnvelope
      try {
        msg = JSON.parse(String(event.data)) as WsEnvelope
      } catch {
        return
      }
      const topic = String(msg.topic || msg.e || '')
      const data = msg.data !== undefined ? msg.data : msg.payload !== undefined ? msg.payload : null
      if (msg.seq != null && topic) {
        const n = Number(msg.seq)
        if (Number.isFinite(n)) {
          // 去重：回放帧 seq 不大于已见值 → 已处理过，丢弃。
          if (seq[topic] !== undefined && n <= seq[topic]) return
          seq[topic] = n
          saveSeq()
        }
      }
      listeners.forEach((fn) => fn({ topic, data, seq: msg.seq ?? null }))
      void handlePush(topic, data)
    }
    ws.onerror = () => {
      link.error = 'WebSocket 异常'
    }
    ws.onclose = () => {
      if (socket === ws) socket = null
      link.ws = 'closed'
      if (!stopped) schedule()
    }
  }

  function schedule() {
    const wait = retry
    retry = Math.min(retry * 2, 15000)
    setTimeout(connect, wait)
  }

  function closeSocket() {
    if (!socket) return
    socket.onclose = null
    socket.close()
    socket = null
  }

  async function handlePush(topic: string, data: unknown) {
    if (!data || typeof data !== 'object') return
    const record = data as Record<string, unknown>
    const kind = String(record.kind || record.type || '')
    const account = String(record.account || record.accountType || record.account_type || '')
    if (topic === 'alert' && (kind === 'confirm' || record.needConfirm || record.need_confirm) && account === 'LIVE') {
      void deps.askConfirm(record, false)
    }
    const title = record.title || record.subject
    const body = record.body || record.message || record.reason
    if (title && (topic === 'alert' || kind === 'notify')) {
      await deps.deskNotify(String(title), body ? String(body) : '')
    }
  }

  function startLink() {
    stopped = false
    Object.assign(seq, loadSeq())
    void tickHealth()
    connect()
    if (!healthTimer) healthTimer = setInterval(() => void tickHealth(), 5000)
  }

  function stopLink() {
    stopped = true
    if (healthTimer) clearInterval(healthTimer)
    healthTimer = null
    closeSocket()
    link.ws = 'idle'
  }

  function restartLink() {
    closeSocket()
    retry = 1000
    if (!stopped) {
      void tickHealth()
      connect()
    }
  }

  return {
    link,
    isLive,
    onPush,
    subscribe,
    startLink,
    stopLink,
    restartLink,
    desiredTopics,
  }
}

/* ---------- 模块级单例（当前前端全局唯一链路） ---------- */

const hub = createWsHub({
  wsUrl,
  checkHealth: healthz,
  askConfirm: (order, local) => askConfirm(order, local),
  deskNotify,
})

export const link = hub.link
export const isLive = hub.isLive
export const onPush = hub.onPush
export const subscribe = hub.subscribe
export const startLink = hub.startLink
export const stopLink = hub.stopLink
export const restartLink = hub.restartLink
export const desiredTopics = hub.desiredTopics
