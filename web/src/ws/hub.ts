/**
 * admin（2003）WebSocket 链路。
 * - 健康检查 5s 轮询 + WS 指数退避重连（1s → 15s）；
 * - 断线恢复：epoch/topic_seq + 显式缺口；REST处理器完成后ack并回放有界缓冲；
 * - 按主题去重：回放帧若 seq 不大于已见值则丢弃，避免重复套用；
 * - 动态订阅：subscribe('market'|'brain') 引用计数，server 端 sub 是整体替换，
 *   每次增删都重发当前期望全集；signal/position/alert 常驻。
 */
import { reactive } from 'vue'
import { healthz } from '@/api/system'
import { askConfirm } from '@/store/confirm'
import { wsUrl } from '@/utils/endpoint'
import { deskNotify } from '@/utils/notify'
import { BASE_TOPICS, type WsEnvelope, type WsPushMessage, type WsResyncContext, type WsTopic } from './topics'

export type { MarketSnapshotMeta, WsPushMessage, WsResyncContext, WsTopic } from './topics'
export { ALL_TOPICS, BASE_TOPICS } from './topics'

export interface HubDeps {
  wsUrl: () => string
  checkHealth: () => Promise<boolean>
  askConfirm: (order: Record<string, unknown>, local: boolean) => Promise<unknown>
  deskNotify: (title: string, body: string) => Promise<boolean>
}

export interface WsHub {
  link: { health: string; ws: string; sync: string; lastAt: Date | null; error: string }
  isLive: () => boolean
  onPush: (fn: (msg: WsPushMessage) => void | Promise<void>) => () => void
  /** 按需订阅主题；返回取消函数。取消后引用归零才真正退订。 */
  subscribe: (topic: WsTopic) => () => void
  onResync: (topics: readonly WsTopic[], handler: (context: WsResyncContext) => Promise<void>) => () => void
  startLink: () => void
  stopLink: () => void
  restartLink: () => void
  /** 当前期望订阅全集（含常驻主题）。 */
  desiredTopics: () => string[]
}

const CURSOR_KEY = 'nt_ws_cursor_v2'
const MAX_BUFFER = 1000
interface TopicState {
  phase: 'pending' | 'required' | 'recovering' | 'ack_wait' | 'replaying' | 'ready'
  watermark: number
  head: number
  reason: string
}
interface ServerCursor {
  head: number
  oldest: number
  after: number
  status: string
  reason?: string
}

export function createWsHub(deps: HubDeps): WsHub {
  const link = reactive({ health: 'unknown', ws: 'idle', sync: 'idle', lastAt: null as Date | null, error: '' })
  const listeners = new Set<(msg: WsPushMessage) => void | Promise<void>>()
  const recoveryHandlers = new Set<{
    topics: readonly WsTopic[]
    handler: (context: WsResyncContext) => Promise<void>
  }>()
  const counters = new Map<WsTopic, number>()
  const seq: Record<string, number> = {}
  const acceptedSeq: Record<string, number> = {}
  const deliveries = new Map<string, Promise<void>>()
  let deliveryCount = 0
  const states = new Map<string, TopicState>()
  let epoch = ''
  let socket: WebSocket | null = null
  let healthTimer: ReturnType<typeof setInterval> | null = null
  let retryTimer: ReturnType<typeof setTimeout> | null = null
  let retry = 1000
  let stopped = true
  let requestID = 0
  let requestAbort = new AbortController()
  let buffer: WsEnvelope[] = []
  let initialized = false

  function loadCursor() {
    try {
      const parsed = JSON.parse(sessionStorage.getItem(CURSOR_KEY) || '{}') as Record<string, unknown>
      if (typeof parsed.epoch !== 'string' || parsed.endpoint !== deps.wsUrl()) return
      epoch = parsed.epoch
      if (parsed.seq && typeof parsed.seq === 'object')
        Object.entries(parsed.seq).forEach(([key, value]) => {
          if (Number.isSafeInteger(value) && Number(value) >= 0) seq[key] = Number(value)
        })
    } catch {
      /* Storage is optional, never a substitute for a state snapshot. */
    }
  }

  function saveCursor() {
    try {
      sessionStorage.setItem(CURSOR_KEY, JSON.stringify({ endpoint: deps.wsUrl(), epoch, seq }))
    } catch {
      /* optional */
    }
  }

  function desiredTopics(): string[] {
    const set = new Set<string>(BASE_TOPICS)
    counters.forEach((count, topic) => {
      if (count > 0) set.add(topic)
    })
    return [...set]
  }

  function updateSync() {
    const active = desiredTopics().map((topic) => states.get(topic))
    if (active.some((state) => !state || state.phase === 'pending')) link.sync = 'syncing'
    else if (active.some((state) => state?.phase === 'required')) link.sync = 'required'
    else if (active.every((state) => state?.phase === 'ready')) {
      const changed = link.sync !== 'ready'
      link.sync = 'ready'
      link.error = ''
      initialized = true
      if (changed)
        listeners.forEach((fn) => {
          void Promise.resolve()
            .then(() => fn({ topic: 'resume', data: null, seq: null, serverEpoch: epoch }))
            .catch(() => {
              /* regular data recovery reports failures separately */
            })
        })
    } else link.sync = 'syncing'
  }

  function syncTopics(force = false) {
    if (!socket || socket.readyState !== WebSocket.OPEN) return
    requestAbort.abort()
    requestAbort = new AbortController()
    requestID++
    const topics = desiredTopics()
    states.clear()
    topics.forEach((topic) => states.set(topic, { phase: 'pending', watermark: seq[topic] || 0, head: 0, reason: '' }))
    buffer = []
    deliveries.clear()
    deliveryCount = 0
    Object.keys(acceptedSeq).forEach((key) => delete acceptedSeq[key])
    link.sync = 'syncing'
    socket.send(
      JSON.stringify({
        op: force ? 'resync' : 'sub',
        topics,
        resume: { ...seq },
        server_epoch: epoch,
        request_id: requestID,
      }),
    )
  }

  function subscribe(topic: WsTopic): () => void {
    counters.set(topic, (counters.get(topic) || 0) + 1)
    if (counters.get(topic) === 1) syncTopics()
    let released = false
    return () => {
      if (released) return
      released = true
      const left = (counters.get(topic) || 1) - 1
      if (left > 0) counters.set(topic, left)
      else {
        counters.delete(topic)
        syncTopics()
      }
    }
  }

  function onResync(topics: readonly WsTopic[], handler: (context: WsResyncContext) => Promise<void>): () => void {
    const registration = { topics, handler }
    recoveryHandlers.add(registration)
    // Panels may mount after the subscription acknowledgement.
    startRecoveries()
    return () => {
      recoveryHandlers.delete(registration)
      // An unmounted handler cannot acknowledge an in-flight snapshot.
      if (topics.some((topic) => states.get(topic)?.phase === 'recovering')) syncTopics(true)
    }
  }

  function isLive() {
    return link.health === 'ok' && link.ws === 'open' && link.sync === 'ready'
  }
  function onPush(fn: (msg: WsPushMessage) => void | Promise<void>): () => void {
    listeners.add(fn)
    return () => listeners.delete(fn)
  }
  async function tickHealth() {
    try {
      link.health = (await deps.checkHealth()) ? 'ok' : 'down'
    } catch {
      link.health = 'down'
    }
    // Retry failed REST recovery on the existing bounded health cadence, never a tight reconnect loop.
    if (
      link.health === 'ok' &&
      link.ws === 'open' &&
      [...states.values()].some((state) => state.phase === 'required') &&
      ![...states.values()].some((state) => state.phase === 'recovering' || state.phase === 'ack_wait')
    )
      startRecoveries()
  }

  function startRecoveries() {
    const ws = socket
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    const id = requestID
    const signal = requestAbort.signal
    for (const [topic, state] of states) {
      if (state.phase !== 'required') continue
      const handlers = [...recoveryHandlers].filter((registration) => registration.topics.includes(topic as WsTopic))
      if (!handlers.length) continue // Unknown state stays unknown; no fire-and-forget fallback.
      state.phase = 'recovering'
      const context: WsResyncContext = {
        serverEpoch: epoch,
        topics: [topic as WsTopic],
        watermarks: { [topic]: state.watermark },
        reason: state.reason,
        signal,
      }
      void Promise.all(handlers.map((registration) => Promise.resolve().then(() => registration.handler(context))))
        .then(() => {
          if (signal.aborted || socket !== ws || requestID !== id || !states.has(topic)) return
          state.phase = 'ack_wait'
          ws.send(JSON.stringify({ op: 'resync_ack', server_epoch: epoch, request_id: id, resume: context.watermarks }))
          updateSync()
        })
        .catch((error: unknown) => {
          if (signal.aborted || socket !== ws || requestID !== id) return
          state.phase = 'required'
          link.error = `恢复 ${topic} 失败：${error instanceof Error ? error.message : String(error)}`
          updateSync()
        })
    }
    updateSync()
  }

  function readCursors(data: unknown): Record<string, ServerCursor> {
    if (!data || typeof data !== 'object') return {}
    const topics = (data as Record<string, unknown>).topics
    if (!topics || typeof topics !== 'object') return {}
    const out: Record<string, ServerCursor> = {}
    Object.entries(topics).forEach(([topic, value]) => {
      if (!value || typeof value !== 'object') return
      const cursor = value as ServerCursor
      if (
        [cursor.head, cursor.oldest, cursor.after].every(Number.isSafeInteger) &&
        cursor.head >= 0 &&
        cursor.oldest > 0 &&
        cursor.oldest <= cursor.head + 1 &&
        (cursor.status === 'resync_required' ||
          (cursor.status === 'resumed' && cursor.after >= cursor.oldest - 1 && cursor.after <= cursor.head))
      )
        out[topic] = cursor
    })
    return out
  }

  function acceptControl(message: WsEnvelope, topic: string, data: unknown) {
    if (message.request_id !== requestID || typeof message.server_epoch !== 'string' || !message.server_epoch) return
    if (epoch !== message.server_epoch) {
      epoch = message.server_epoch
      Object.keys(seq).forEach((key) => delete seq[key])
      buffer = []
      saveCursor()
    }
    const cursors = readCursors(data)
    Object.entries(cursors).forEach(([name, cursor]) => {
      const state = states.get(name)
      if (!state) return
      state.head = cursor.head
      state.reason = cursor.reason || ''
      if (cursor.status === 'resync_required') {
        state.watermark = cursor.head
        // sub_ack announces the need; the explicit resync_required frame starts REST.
        if (topic === 'resync_required' || (state.phase !== 'recovering' && state.phase !== 'ack_wait'))
          state.phase = 'required'
      } else if (topic !== 'resync_required') {
        seq[name] = cursor.after
        acceptedSeq[name] = cursor.after
        state.watermark = cursor.after
        state.phase = seq[name] >= cursor.head ? 'ready' : 'replaying'
      }
    })
    if (topic === 'resync_required') startRecoveries()
    const pending = buffer
    buffer = []
    pending.forEach(acceptData)
    saveCursor()
    updateSync()
  }

  function acceptData(message: WsEnvelope) {
    const topic = String(message.topic || message.e || '')
    if (!states.has(topic)) return
    const state = states.get(topic)!
    if (message.server_epoch !== epoch || !epoch) return
    const n = message.topic_seq ?? message.seq
    if (!Number.isSafeInteger(n) || Number(n) <= 0) return
    if (state.phase !== 'ready' && state.phase !== 'replaying') {
      buffer.push(message)
      if (buffer.length > MAX_BUFFER) {
        link.error = '恢复缓冲已满，重新同步'
        syncTopics(true)
      }
      return
    }
    const last = acceptedSeq[topic] ?? seq[topic] ?? 0
    if (Number(n) <= last) return
    if (Number(n) !== last + 1) {
      link.error = `${topic} 推送序号缺口，正在重新同步`
      syncTopics(true)
      return
    }
    if (deliveryCount >= MAX_BUFFER) {
      link.error = '页面消费过慢，重新同步'
      closeSocket()
      link.ws = 'closed'
      link.sync = 'required'
      schedule()
      return
    }
    const data =
      message.data !== undefined ? message.data : message.payload !== undefined ? message.payload : (message.d ?? null)
    acceptedSeq[topic] = Number(n)
    const id = requestID
    const abort = requestAbort.signal
    deliveryCount++
    const previous = deliveries.get(topic) || Promise.resolve()
    const delivery = previous
      .then(async () => {
        if (abort.aborted || requestID !== id) return
        // Async consumers can await their authoritative refresh before this cursor is committed.
        await Promise.all(
          [...listeners].map((fn) =>
            Promise.resolve().then(() =>
              fn({
                topic,
                data,
                seq: Number(n),
                serverEpoch: epoch,
                metadata: message.metadata,
                replayed: message.replayed === true,
              }),
            ),
          ),
        )
        if (abort.aborted || requestID !== id) return
        seq[topic] = Number(n)
        if (state.phase === 'replaying' && seq[topic] >= state.head) state.phase = 'ready'
        saveCursor()
        updateSync()
        // UI prompts may wait for a person; they must not hold the ordered domain-delivery queue.
        // Historical confirmation/notification events must not trigger a second external prompt.
        if (!message.replayed) handlePush(topic, data)
      })
      .catch((error: unknown) => {
        if (abort.aborted || requestID !== id) return
        link.error = `推送处理失败：${error instanceof Error ? error.message : String(error)}`
        syncTopics(true)
      })
      .finally(() => {
        if (requestID === id) deliveryCount--
      })
    deliveries.set(topic, delivery)
  }

  function receive(message: WsEnvelope) {
    const topic = String(message.topic || message.e || '')
    const data = message.data !== undefined ? message.data : (message.d ?? message.payload)
    if (['sub_ack', 'resync_required', 'resync_ack'].includes(topic)) {
      acceptControl(message, topic, data)
      return
    }
    if (topic === 'replay_batch') {
      if (message.request_id !== requestID || message.server_epoch !== epoch || !data || typeof data !== 'object')
        return
      const frames = (data as Record<string, unknown>).frames
      if (Array.isArray(frames))
        frames.forEach((item) => {
          if (item && typeof item === 'object') acceptData({ ...(item as WsEnvelope), replayed: true })
        })
      return
    }
    acceptData(message)
  }

  function connect() {
    if (stopped || (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)))
      return
    link.ws = 'connecting'
    link.sync = 'syncing'
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
      if (socket !== ws || stopped) return
      link.ws = 'open'
      link.error = ''
      retry = 1000
      // A new hub has no restored entity state even if its session cursor was copied to another window.
      syncTopics(!initialized)
    }
    ws.onmessage = (event) => {
      if (socket !== ws || stopped) return
      let message: WsEnvelope
      try {
        message = JSON.parse(String(event.data)) as WsEnvelope
      } catch {
        return
      }
      if (!message || typeof message !== 'object') return
      link.lastAt = new Date()
      receive(message)
    }
    ws.onerror = () => {
      if (socket === ws) link.error = 'WebSocket 异常'
    }
    ws.onclose = () => {
      if (socket !== ws) return
      socket = null
      requestAbort.abort()
      buffer = []
      link.ws = 'closed'
      link.sync = 'required'
      if (!stopped) schedule()
    }
  }

  function schedule() {
    if (retryTimer || stopped) return
    const wait = retry
    retry = Math.min(retry * 2, 15000)
    retryTimer = setTimeout(() => {
      retryTimer = null
      connect()
    }, wait)
  }

  function closeSocket() {
    requestAbort.abort()
    buffer = []
    if (retryTimer) clearTimeout(retryTimer)
    retryTimer = null
    if (!socket) return
    socket.onclose = null
    socket.close()
    socket = null
  }

  function runPrompt(label: string, action: () => Promise<unknown>) {
    void Promise.resolve()
      .then(action)
      .catch(() => {
        // Prompt failures do not invalidate an already-applied domain event. Do not log its payload.
        console.warn(`[ws] ${label}调用失败`)
      })
  }

  function handlePush(topic: string, data: unknown) {
    if (!data || typeof data !== 'object') return
    const record = data as Record<string, unknown>
    const kind = String(record.kind || record.type || '')
    const account = String(record.account || record.accountType || record.account_type || '')
    if (topic === 'alert' && (kind === 'confirm' || record.needConfirm || record.need_confirm) && account === 'LIVE') {
      runPrompt('交易确认提示', () => deps.askConfirm(record, false))
    }
    const title = record.title || record.subject
    const body = record.body || record.message || record.reason
    if (title && (topic === 'alert' || kind === 'notify')) {
      runPrompt('桌面通知', () => deps.deskNotify(String(title), body ? String(body) : ''))
    }
  }

  function startLink() {
    if (!stopped) return
    stopped = false
    loadCursor()
    void tickHealth()
    connect()
    if (!healthTimer) healthTimer = setInterval(() => void tickHealth(), 5000)
  }

  function stopLink() {
    stopped = true
    if (healthTimer) clearInterval(healthTimer)
    healthTimer = null
    closeSocket()
    states.clear()
    Object.keys(seq).forEach((key) => delete seq[key])
    epoch = ''
    initialized = false
    try {
      sessionStorage.removeItem(CURSOR_KEY)
    } catch {
      /* optional */
    }
    link.ws = 'idle'
    link.sync = 'idle'
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
    onResync,
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
export const onResync = hub.onResync
export const subscribe = hub.subscribe
export const startLink = hub.startLink
export const stopLink = hub.stopLink
export const restartLink = hub.restartLink
export const desiredTopics = hub.desiredTopics
