<script setup lang="ts">
/**
 * 监控驾驶舱。只读：不挂 dock，不写订单、不触发急停、不改模式。
 *
 * 版面：顶部 HUD 数字带 + 中间可拖拽画布（接入 → 研判 → 裁决）+ 底部终端带。
 * 画布只认 scene-model 产出的 SceneGraph，本文件不再自己排坐标。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { getConfigSnapshot, saveConfig, previewConfig, type ConfigSnapshot, type ConfigPreview } from '@/api/config'
import ConfigRevisionPanel from '@/view/ConfigRevisionPanel.vue'
import { ApiError } from '@/api/http'
import { useOverlay } from '@/ui/useOverlay'
import { configCategories, type ConfigField } from '@/view/config-catalog'
import { isLive, onResync } from '@/ws/hub'
import { useAccountStore } from '@/stores/account'
import { createRefreshQueue } from '@/utils/refresh-queue'
import AlertCenter from '@/workspace/AlertCenter.vue'
import { brainReasoning } from '@/api/brain'
import { getIntelItem } from '@/api/intel'
import { getRiskReport } from '@/api/risk'
import CanvasStage from './CanvasStage.vue'
import DetailSheet from './DetailSheet.vue'
import { ActivityLog } from './activity'
import { stagePulse } from './canvas-stage'
import { buildDetail, intelIdsOf, type DetailExtra } from './detail'
import { dutyOf } from './duty'
import { buildEdgeDetail, edgeOf, retainEdgePulses } from './edge-detail'
import { CounterFlow, KILL_EDGE_ID, MODEL_EDGE_KEYS, PulseFlow, tierFor, type EdgeFlow } from './flow'
import { cardFreshness, cardSilence, channelSilence, decorate, observeGraph, sourceKind, sourceNodeId } from './freshness'
import { computeHealth } from './health'
import { resolveHud } from './hud'
import { emptyMonitorInput, loadMonitorInput } from './load-input'
import { bindPulses } from './pulses'
import { assertMonitorRecovery } from './recovery'
import { observationSections, topicsForNode } from './observability'
import { RingSeries } from './sampler'
import { buildChildren, buildScene, pushPulse } from './scene-model'
import Sparkline from './Sparkline.vue'
import { TapeSession } from './tape'
import TerminalStrip from './TerminalStrip.vue'
import TraceExplorer from './TraceExplorer.vue'
import EnvironmentExplorer from './EnvironmentExplorer.vue'
import { bandCountdown, bandSegments, bandSpec, detectBand, hudCells } from './timeband'
import type { TapeRow } from './tape'
import type { Pulse } from './types'
import './monitor.css'

const router = useRouter()
const stage = ref<InstanceType<typeof CanvasStage> | null>(null)
const input = ref(emptyMonitorInput())
const selectedId = ref('')
const selectedEdgeId = ref('')
const edgePulseHistory = ref<Pulse[]>([])
const paused = ref(typeof document !== 'undefined' && document.hidden)

/* ---- 时段带：焦点随交易时段切换，拓扑永不变（timeband.ts） ---- */

/* S2：1 秒一拍的前端时钟。轮询停了这个钟也照走，角标年龄跟着涨，这才回答得出「多久没读到」 */
const clockMs = ref(Date.now())
const nowMs = computed(() => clockMs.value || input.value.now || Date.now())
const bandKey = computed(() => detectBand(new Date(nowMs.value)))
const spec = computed(() => bandSpec(bandKey.value))
const segments = computed(() => bandSegments(bandKey.value))
const countdown = computed(() => bandCountdown(new Date(nowMs.value), bandKey.value))
const offday = computed(() => bandKey.value === 'offday')

/* S2：活动条与角标只在内存里（activity.ts / freshness.ts），不落库不上传 */
const activity = new ActivityLog()

/** 静默表单独抽出来：画布牌面要用，S4 事件流也要用（模型域静默进流，§6.1） */
const silenceMap = computed(() => cardSilence(input.value, { now: nowMs.value, trading: !offday.value, activity }))

const graph = computed(() => {
  const base = buildScene(input.value, nowMs.value)
  const trading = !offday.value
  const fresh = cardFreshness(input.value, nowMs.value, trading)
  const cells = new Map<string, (boolean | null)[]>()
  for (const node of base.nodes) cells.set(node.id, activity.cells(node.id, nowMs.value))
  return decorate(base, fresh, silenceMap.value, cells)
})
const cards = computed(() => buildChildren(input.value, selectedId.value))
const picked = computed(() => graph.value.nodes.find((node) => node.id === selectedId.value) ?? null)
const pickedEdge = computed(() => edgeOf(graph.value, selectedEdgeId.value))

/* ---- S6 束线流量（§3.2/§4.4.3/§7）：计数器差分 + WS 脉冲计数，估不出固定宽度不猜 ----
 * 环形缓冲全在前端内存（§9）：不落库、不上传，页面刷新即失。 */

const counterFlow = new CounterFlow()
const pulseFlow = new PulseFlow()
/* 风控→账户没有计数器，只有 WS 告警脉冲这一个观测口；从挂载起攒 60s 窗口 */
pulseFlow.observe(KILL_EDGE_ID, Date.now())

/** 每轮轮询末尾喂计数器：数据源 successCount（边键取自场景图）、模型档 calls */
function sampleFlow() {
  const at = Date.now()
  const data = input.value
  if (data.sources?.ok) {
    const byNode = new Map(data.sources.items.map((item) => [sourceNodeId(item.id, item.name), item]))
    for (const edge of graph.value.edges) {
      if (!edge.from.startsWith('source:')) continue
      const item = byNode.get(edge.from)
      counterFlow.sample(edge.id, item?.successCount ?? null, at)
    }
  }
  const tiers = data.brainStats?.ok ? data.brainStats.tiers : []
  for (const which of ['small', 'large'] as const) {
    const tier = tierFor(tiers, which)
    for (const key of MODEL_EDGE_KEYS[which]) counterFlow.sample(key, tier?.calls ?? null, at)
  }
}

const edgeFlow = computed<Record<string, EdgeFlow>>(() => {
  const now = nowMs.value
  const data = input.value
  const out: Record<string, EdgeFlow> = {}
  /* 数据源边：差分速率。断流 = 静默表命中，或行情源计数整窗没动——
   * 非行情源（新闻/外围）本来就是一阵一阵的，闲时不算断流，不冤枉它 */
  if (data.sources?.ok) {
    const byNode = new Map(data.sources.items.map((item) => [sourceNodeId(item.id, item.name), item]))
    for (const edge of graph.value.edges) {
      if (!edge.from.startsWith('source:')) continue
      const item = byNode.get(edge.from)
      const rate = counterFlow.rate(edge.id, now)
      const stalled = silenceMap.value.has(edge.from) || (rate === 0 && item != null && sourceKind(item.id, item.name) === 'tape')
      out[edge.id] = { rate, stalled, circuit: false }
    }
  }
  /* 模型边：档位 calls 差分 + 熔断标记（§4.4.3：熔断时该边转红并停流向点） */
  const tiers = data.brainStats?.ok ? data.brainStats.tiers : []
  for (const which of ['small', 'large'] as const) {
    const tier = tierFor(tiers, which)
    for (const key of MODEL_EDGE_KEYS[which]) {
      out[key] = { rate: counterFlow.rate(key, now), stalled: false, circuit: tier?.circuitOpen === true }
    }
  }
  /* 风控→账户：WS 告警脉冲计数；窗口没攒满 = null，画固定宽度 */
  out[KILL_EDGE_ID] = { rate: pulseFlow.rate(KILL_EDGE_ID, now), stalled: false, circuit: false }
  return out
})

/** S6 急停态视觉：风控→账户束线转红加粗 + 账户牌呼吸环（§3.2） */
const killActive = computed(() => input.value.risk?.killActive === true)

/** S6 ≥85 分信号脉冲升级：WS 信号脉冲只带 id/symbol，评分从信号表对齐；没有分数不升级 */
const signalScores = computed<Record<string, number>>(() => {
  const out: Record<string, number> = {}
  if (!input.value.signals?.ok) return out
  for (const item of input.value.signals.items) {
    if (item.score == null) continue
    if (item.id) out[item.id] = item.score
    if (item.symbol) out[item.symbol] = item.score
  }
  return out
})

const AGENT = new Set(['post:scan', 'dim:tech', 'dim:news', 'dim:capital', 'dim:overseas', 'dim:synth'])

/** 选中一张卡时，与它相连的卡也保持亮，其余压暗。 */
const hotIds = computed(() => {
  if (pickedEdge.value) return [pickedEdge.value.from, pickedEdge.value.to]
  const id = selectedId.value
  if (!id) return []
  const ids = new Set<string>()
  for (const edge of graph.value.edges) {
    if (edge.from === id) ids.add(edge.to)
    if (edge.to === id) ids.add(edge.from)
  }
  return [...ids]
})

const detailWhere = computed(() => {
  const id = selectedId.value
  if (AGENT.has(id)) return 'agent'
  if (id.startsWith('source:') && id !== 'source:quality') return 'source'
  if (id.startsWith('model:')) return 'model'
  if (id.startsWith('service:') || id.startsWith('resource:') || id === 'source:quality') return 'runtime'
  return id ? 'verdict' : ''
})

/* ---- 健康总分：五项观测覆盖、扣分可解释（health.ts） ---- */

const health = computed(() => computeHealth(input.value, nowMs.value))
const healthOpen = ref(false)

/* 健康分明细是全页唯一的轻弹层：Esc 要先关它，而不是直接把监控页退掉；
 * 点弹层外也关。挂进 useOverlay 的 z 栈，Esc 只派发给栈顶。 */
const healthOverlay = useOverlay(() => {
  healthOpen.value = false
})

watch(healthOpen, (open) => {
  if (open) healthOverlay.open()
  else healthOverlay.close()
})

function onDocPointerDown(event: PointerEvent) {
  if (!healthOpen.value) return
  const target = event.target as HTMLElement | null
  if (target?.closest('.nt-health')) return
  healthOpen.value = false
}

/* ---- S4 事件流 TAPE（§4.6）：六域统一时间线，唯一不做聚合的原始流 ----
 * 脉冲 2.4s 后会从 input.pulses 过期（scene-model PULSE_TTL），健康分/静默/WS 通断
 * 是状态量不能每帧刷一行——所以进流的事件交给 TapeSession 在会话内累积与去重。 */

const tapeSession = new TapeSession()
const tapeRows = ref<TapeRow[]>([])
/** 点事件流某行 → 对应画布节点闪一下（CanvasStage 按 at 自己衰减） */
const flash = ref<{ id: string; at: number } | null>(null)

function absorbTape(pulses?: Pulse[]) {
  tapeRows.value = tapeSession.absorb(
    {
      notices: input.value.notices,
      audit: input.value.audit,
      fills: input.value.fills,
      pulses: pulses ?? input.value.pulses,
      health: health.value,
      silence: silenceMap.value,
      live: isLive(),
    },
    Date.now(),
  )
}

function onTapeFlash(id: string) {
  flash.value = { id, at: Date.now() }
}

/* ---- 顶部 HUD：时段决定十格，格内容来自 resolveHud ---- */

const hud = computed(() => resolveHud(input.value, hudCells(bandKey.value), nowMs.value))

const sparks = {
  limit: new RingSeries(30),
  broken: new RingSeries(30),
  signal: new RingSeries(30),
  p95: new RingSeries(30),
  equity: new RingSeries(30),
  /* S5 §5.1：日内权益曲线的独立缓冲（equity spark 记的是当日盈亏，口径不同） */
  day: new RingSeries(30),
}

function sparkPoints(key: string): (number | null)[] {
  if (key === 'limit' || key === 'broken' || key === 'signal' || key === 'p95' || key === 'equity') {
    return sparks[key].points()
  }
  return []
}

/** 每次轮询末尾给五条 sparkline 各推一个点；接口没返回推 null（断线不画） */
function sampleSparks() {
  const data = input.value
  sparks.limit.push(data.limits?.ok ? data.limits.rows.length : null)
  sparks.broken.push(data.sentiment?.ok && data.sentiment.brokenRate != null ? data.sentiment.brokenRate : null)
  sparks.signal.push(
    data.signals?.ok ? data.signals.items.filter((item) => item.status === 'approved' || item.status === 'risk_checking').length : null,
  )
  sparks.p95.push(data.brainStats?.ok ? Math.max(0, ...data.brainStats.tiers.map((tier) => tier.p95Ms ?? 0)) : null)
  sparks.equity.push(data.account?.ok ? data.account.dayPnl : null)
  sparks.day.push(data.account?.ok ? data.account.equity : null)
}

const sampled = computed(() => {
  const at = input.value.sampledAt ?? input.value.now
  if (!at) return ''
  const date = new Date(at)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
})

const STATUS_WORD: Record<string, string> = { live: '在线', idle: '空闲', gap: '未返回', down: '中断', attention: '要注意' }

/* ---- 详情弹层 ---- */

const draft = ref<Record<string, string>>({})
const configMsg = ref('')
const configSnapshot = ref<ConfigSnapshot | null>(null)
const configImpact = ref<ConfigPreview | null>(null)
const configBaseline = ref<Record<string, string>>({})
let configGeneration = 0
watch(draft, () => { configGeneration++; configImpact.value = null }, { deep: true, flush: 'sync' })
const saving = ref(false)
const configFields = computed(() => fieldsFor(selectedId.value, picked.value?.title || ''))
const serviceLogs = computed(() => {
  if (detailWhere.value !== 'runtime' || !picked.value) return []
  const key = picked.value.title.trim().toLowerCase()
  const items = input.value.audit?.ok ? input.value.audit.items : []
  return items.filter((item) => `${item.action} ${item.target}`.toLowerCase().includes(key)).slice(0, 8)
})

/* ---- S5 详情窗深化（§5.1–5.6）：懒加载的补充数据全部只读，失败如实写进面板 ---- */
const killHistory = ref<DetailExtra['killHistory']>(null)
const intelTitles = ref<Record<string, { title: string; source: string; time: string } | null>>({})
const signalReasoning = ref<Record<string, { found: boolean; excerpt: string; latencyMs: number; model: string; createdAt: string } | null>>({})
/** 换牌时作废旧请求：用这个令牌比对，晚到的响应直接丢 */
let detailToken = 0

const detailExtra = computed<DetailExtra>(() => ({
  equityPoints: sparks.day.points(),
  killHistory: killHistory.value,
  intel: intelTitles.value,
  reasoning: signalReasoning.value,
}))
const detailPanel = computed(() => {
  if (!selectedId.value) return null
  const panel = buildDetail(input.value, selectedId.value, nowMs.value, detailExtra.value)
  const observation = observationSections(input.value.observability, topicsForNode(selectedId.value))
  if (!panel) return observation.length ? { kind: 'ops' as const, sections: observation } : null
  return { ...panel, sections: [...panel.sections, ...observation] }
})
const edgeDetail = computed(() => buildEdgeDetail(graph.value, input.value, selectedEdgeId.value, edgeFlow.value[selectedEdgeId.value], edgePulseHistory.value, nowMs.value))

function onDetailExit(to: string) {
  closeDetail()
  void router.push(to)
}

/** 风控牌的急停历史：今日已在轮询里，近 7 日按日读（点开才读，10s 轮询不背这 7 个请求） */
async function loadKillHistory(token: number) {
  const days: NonNullable<DetailExtra['killHistory']> = []
  for (let back = 1; back <= 7; back += 1) {
    const date = new Date(nowMs.value - back * 86400000)
    const key = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
    try {
      const report = await getRiskReport(key)
      days.push({ date: key, ok: true, events: report.events ?? [] })
    } catch {
      days.push({ date: key, ok: false, events: [] })
    }
  }
  if (token === detailToken) killHistory.value = days
}

/** 信号牌证据链的 N:123：逐条读标题（GET /api/intel/items/{id}），取不到记 null → 面板写「标题未返回」 */
async function loadIntelTitles(ids: string[], token: number) {
  for (const id of ids) {
    if (intelTitles.value[id] !== undefined) continue
    try {
      const item = await getIntelItem(id)
      if (token !== detailToken) return
      intelTitles.value = { ...intelTitles.value, [id]: item ? { title: item.title, source: item.source, time: item.time } : null }
    } catch {
      if (token === detailToken) intelTitles.value = { ...intelTitles.value, [id]: null }
    }
  }
}

watch(selectedId, (id) => {
  detailToken += 1
  const token = detailToken
  killHistory.value = null
  const panel = id ? buildDetail(input.value, id, nowMs.value, { intel: intelTitles.value, reasoning: signalReasoning.value }) : null
  if (panel?.kind === 'risk') void loadKillHistory(token)
  if (panel?.kind === 'signal') {
    const symbol = id!.startsWith('symbol:') ? id!.slice('symbol:'.length) : ''
    const signal = input.value.signals?.ok ? input.value.signals.items.find((item) => item.symbol === symbol) : undefined
    if (signal) {
      void loadIntelTitles(intelIdsOf(signal.evidence), token)
      void loadReasoning(signal.id, token)
    }
  }
})

/** S7：单只信号的研判正文摘录（GET /api/brain/reasoning?signal_id=），取不到记 null → 面板写「未接入」 */
async function loadReasoning(signalId: string, token: number) {
  if (signalReasoning.value[signalId] !== undefined) return
  try {
    const item = await brainReasoning(Number(signalId))
    if (token !== detailToken) return
    signalReasoning.value = {
      ...signalReasoning.value,
      [signalId]: { found: item.found, excerpt: item.excerpt, latencyMs: item.latencyMs, model: item.model, createdAt: item.createdAt },
    }
  } catch {
    if (token === detailToken) signalReasoning.value = { ...signalReasoning.value, [signalId]: null }
  }
}

watch(selectedId, (id) => {
  configMsg.value = ''
  const fields = fieldsFor(id, graph.value.nodes.find((node) => node.id === id)?.title || '')
  if (fields.length === 0) {
    draft.value = {}
    return
  }
  void loadDraft(fields)
})

function fieldsFor(id: string, title: string): ConfigField[] {
  const all = configCategories.flatMap((category) => category.fields)
  return keysFor(id, title)
    .map((key) => all.find((field) => field.key === key))
    .filter((field): field is ConfigField => field != null)
}

function keysFor(id: string, title: string): string[] {
  if (id === 'model:large') return ['app.brain.large.url', 'app.brain.large.model', 'app.brain.large.key']
  if (id === 'model:small') return ['app.brain.small.url', 'app.brain.small.model', 'app.brain.small.key']
  if (id === 'dim:tech') return ['app.agent.technical', 'app.agent.w.technical']
  if (id === 'dim:capital') return ['app.agent.capital', 'app.agent.w.capital']
  if (id === 'dim:news') return ['app.agent.news', 'app.agent.w.news']
  if (id === 'dim:overseas') return ['app.agent.external', 'app.agent.w.external']
  if (id === 'dim:synth') return ['app.agent.synthesis']
  const name = title.toLowerCase()
  if (name.includes('tushare')) return ['app.ds.tushare.on', 'app.ds.tushare.token']
  if (name.includes('tavily')) return ['app.ds.tavily.on', 'app.ds.tavily.token', 'app.ds.tavily.feeds']
  if (name.includes('通达信') || name.includes('tdx') || name.includes('akshare')) return ['app.ds.pyworker', 'app.ds.tdx_local.on', 'app.ds.tdx_file.on', 'app.ds.akshare.on']
  if (name.includes('东方财富')) return ['app.ds.eastmoney.on']
  if (name.includes('巨潮')) return ['app.ds.cninfo.on']
  if (name.includes('rss')) return ['app.ds.rss.on', 'app.ds.rss.feeds']
  if (id === 'service:notify' || name === 'notify') return ['app.notify.webhook', 'app.notify.secret']
  return []
}

async function loadDraft(fields: ConfigField[]) {
  const generation = ++configGeneration
  configSnapshot.value = null; configImpact.value = null
  try {
    const snapshot = await getConfigSnapshot()
    if (generation !== configGeneration) return
    const next: Record<string, string> = {}
    for (const field of fields) next[field.key] = field.kind === 'secret' ? '' : snapshot.values[field.key] ?? field.fallback
    draft.value = next; configBaseline.value = { ...next }; configSnapshot.value = snapshot
  } catch (error) { if (generation === configGeneration) configMsg.value = error instanceof ApiError ? error.message : '配置没有读到' }
}
async function refreshConfigReceipts() {
  try { const next = await getConfigSnapshot(); if (next.revision !== configSnapshot.value?.revision) { configMsg.value = '保存版本已变化，请重新读取配置后修改'; return }; configSnapshot.value = next } catch { configMsg.value = '回执读取失败' }
}
async function saveDraft() {
  if (saving.value || !configSnapshot.value) return
  const values: Record<string, string> = {}
  for (const [key, value] of Object.entries(draft.value)) { if (value !== configBaseline.value[key] && value.trim()) values[key] = value.trim() }
  if (!Object.keys(values).length) { configMsg.value = '没有要保存的改动'; return }
  saving.value = true
  const generation = configGeneration
  try {
    if (!configImpact.value) {
      const preview = await previewConfig(values, configSnapshot.value.revision)
      if (generation === configGeneration) configImpact.value = preview
      return
    }
    const saved = await saveConfig(values, configImpact.value.baseRevision)
    if (generation !== configGeneration) return
    configSnapshot.value = saved; configBaseline.value = { ...draft.value }; configImpact.value = null
    for (const field of configFields.value) if (field.kind === 'secret') { draft.value[field.key] = ''; configBaseline.value[field.key] = '' }
    configMsg.value = `已保存版本 #${saved.revision}，等待目标服务加载回执`
    void pull()
  } catch (error) { if (generation === configGeneration) configMsg.value = error instanceof ApiError ? error.message : error instanceof Error ? error.message : '保存失败' }
  finally { saving.value = false }
}

function resetView() {
  stage.value?.resetLayout()
}

/* ---- 数据轮询与实时脉冲 ---- */

/* S2：1 秒时钟 + 交易通道静默巡检（§6.1：60 秒没成交且委托挂着 → 升告警） */
let ticker = 0
let hiddenAt = 0
const channelAlert = ref<{ id: string; title: string; body: string; at: string } | null>(null)
const channelSeen = new Set<string>()

function tick() {
  clockMs.value = Date.now()
  /* S4：健康分跌破 70、模型静默、WS 通断都是状态量，交给会话去重后再进流（§6.1/§6.3） */
  absorbTape()
  const hit = channelSilence(input.value, activity, clockMs.value, !offday.value)
  if (!hit || channelSeen.has(hit.orderId)) return
  channelSeen.add(hit.orderId)
  channelAlert.value = {
    id: `channel:${hit.orderId}:${clockMs.value}`,
    title: '交易通道静默',
    body: hit.text,
    at: new Date(clockMs.value).toISOString(),
  }
}

const overlay = useOverlay(() => {
  if (selectedId.value || selectedEdgeId.value) closeDetail()
  else void router.push('/')
})

let poll = 0
let flush = 0
let pending: Pulse[] = []
let unbind = () => {}
let offRecovery = () => {}
let mounted = true

onMounted(() => {
  overlay.open()
  document.addEventListener('visibilitychange', onVisibility)
  document.addEventListener('pointerdown', onDocPointerDown)
  startPoll()
  ticker = window.setInterval(tick, 1000)
  offRecovery = onResync(['position', 'signal', 'alert', 'market', 'brain'], async ({ topics, signal }) => {
    const applied = await pull()
    assertMonitorRecovery(input.value, topics, applied, mounted, signal)
  })
  unbind = bindPulses(
    (pulse) => enqueue(pulse),
    async (topic) => {
      // Hidden canvas pauses decoration and polling, never acknowledges an unread critical update.
      const applied = await pull()
      assertMonitorRecovery(input.value, [topic], applied, mounted)
    },
  )
})

onBeforeUnmount(() => {
  mounted = false
  overlay.close()
  healthOverlay.close()
  document.removeEventListener('visibilitychange', onVisibility)
  document.removeEventListener('pointerdown', onDocPointerDown)
  stopPoll()
  if (ticker) window.clearInterval(ticker)
  if (flush) window.clearTimeout(flush)
  unbind()
  offRecovery()
})

function startPoll() {
  stopPoll()
  if (paused.value) return
  void pull()
  poll = window.setInterval(() => void pull(), 10_000)
}

function stopPoll() {
  if (poll) window.clearInterval(poll)
  poll = 0
}

function onVisibility() {
  paused.value = document.hidden
  if (document.hidden) {
    stopPoll()
    hiddenAt = Date.now()
  } else {
    /* 页面藏起这段时间没有任何观测：记成 hole，格子画未知，静默时钟从这里重新起算 */
    if (hiddenAt) activity.hole(hiddenAt, Date.now())
    hiddenAt = 0
    startPoll()
  }
}

const queuedPull = createRefreshQueue(async () => {
    for (let attempt = 0; attempt < 2; attempt++) {
      const requested = input.value.focusSymbol
      const requestedBook = useAccountStore().book
      const next = await loadMonitorInput(requested, input.value.pulses)
      if (!mounted) return false
      if (input.value.focusSymbol !== requested || useAccountStore().book !== requestedBook) continue
      next.pulses = input.value.pulses
      next.lastPush = input.value.lastPush
      input.value = next
      sampleSparks()
      /* S6：这一轮的 successCount / calls 喂给流量差分（束线宽度与流向点速度） */
      sampleFlow()
      /* S2：这一轮读到的牌面喂给活动条（签名只含状态/主词/副行，见 freshness.observeGraph） */
      observeGraph(activity, buildScene(next, Date.now()), next, Date.now())
      /* S4：通知/审计/成交这三路是轮询来的，每轮拉完重新入流（id 稳定，不会重复） */
      absorbTape()
      return true
    }
    return false
})
function pull() { return queuedPull() }

function enqueue(pulse: Pulse) {
  pending.push(pulse)
  if (flush) return
  flush = window.setTimeout(() => {
    let list = input.value.pulses
    const now = Date.now()
    edgePulseHistory.value = retainEdgePulses(edgePulseHistory.value, pending, now)
    for (const item of pending) {
      list = pushPulse(list, item, now)
      /* S6：脉冲计数喂给流量口径——只有 observe 过的边（风控→账户）会被采用，其余记了也不读 */
      const mapped = stagePulse(item)
      if (mapped && mapped.from) pulseFlow.record(`${mapped.from}>${mapped.to}`, now)
    }
    pending = []
    flush = 0
    input.value = { ...input.value, pulses: list, lastPush: now }
    /* S4：脉冲入流的时机就是这里——再晚 2.4s 它们就从 input 过期了 */
    absorbTape(list)
  }, 300)
}

function select(id: string) {
  healthOpen.value = false
  selectedEdgeId.value = ''
  if (id === 'action:trade') {
    void router.push('/panel/trade')
    return
  }
  // 空白处点一下（id 为空）和再点同一张牌，都是取消选中
  if (!id || id === selectedId.value) {
    selectedId.value = ''
    return
  }
  /* S5 深化牌（账户/风控/扫描/模型/数据源）弹层内容来自 buildDetail，不走 buildChildren 的门槛 */
  if (!buildDetail(input.value, id, nowMs.value) && buildChildren(input.value, id).length === 0) return
  selectedId.value = id
  if (!id.startsWith('symbol:')) return
  const symbol = id.slice('symbol:'.length)
  if (!symbol || input.value.focusSymbol === symbol) return
  input.value = { ...input.value, focusSymbol: symbol }
  void pull()
}

function selectEdge(id: string) {
  healthOpen.value = false
  selectedId.value = ''
  selectedEdgeId.value = selectedEdgeId.value === id || !edgeOf(graph.value, id) ? '' : id
}

function closeDetail() {
  selectedId.value = ''
  selectedEdgeId.value = ''
}
</script>

<template>
  <div class="nt-monitor">
    <header class="nt-monitor__bar">
      <button type="button" class="nt-link" @click="router.push('/')">交易台</button>
      <h1>监控驾驶舱</h1>
      <TraceExplorer />
      <EnvironmentExplorer />
      <button type="button" class="nt-link" @click="resetView">重置视图</button>
      <div class="nt-health">
        <button type="button" class="nt-health__badge" :data-tone="health.tone" @click="healthOpen = !healthOpen">
          {{ health.score == null ? '健康待判定' : `健康 ${health.score}` }} · 观测 {{ health.measuredCount }}/{{ health.totalCount }}<i class="dot" :class="`is-${health.tone}`"></i>
        </button>
        <section v-if="healthOpen" class="nt-health__pop" role="dialog" aria-label="健康分明细">
          <h3>{{ health.score == null ? '健康待判定' : `健康分 ${health.score}` }} · 已观测 {{ health.measuredCount }}/{{ health.totalCount }}</h3>
          <ul>
            <li v-for="part in health.parts" :key="part.key" :data-measured="part.measured">
              <span>{{ part.label }}</span>
              <strong v-if="part.measured" :data-deduct="part.deduction > 0">{{ part.deduction > 0 ? `−${part.deduction}` : '满分' }}</strong>
              <strong v-else>未判定</strong>
              <small>{{ part.note }}</small>
            </li>
          </ul>
        </section>
      </div>
      <span v-if="sampled" class="nt-monitor__hint">采样 {{ sampled }}</span>
      <span class="nt-live" :class="{ 'is-on': isLive() }">
        <i class="dot" :class="isLive() ? 'is-live' : 'is-gap'"></i>
        {{ isLive() ? '实时' : '不是实时' }}
      </span>
    </header>

    <nav class="nt-band" :data-band="bandKey" aria-label="交易时段">
      <template v-if="!offday">
        <span
          v-for="segment in segments"
          :key="segment.key"
          class="nt-band__seg"
          :data-state="segment.state"
          :data-key="segment.key"
        >
          {{ segment.label }}
        </span>
        <span class="nt-band__focus">{{ spec.focus }}</span>
      </template>
      <span v-else class="nt-band__seg" data-state="now">今日休市</span>
      <span v-if="countdown" class="nt-band__count">{{ countdown }}</span>
    </nav>

    <section class="nt-hud" aria-label="总览">
      <button
        v-for="cell in hud"
        :key="cell.key"
        type="button"
        :class="{ 'is-hero': cell.hero }"
        :title="cell.sub"
        @click="select(cell.select)"
      >
        <span>{{ cell.label }}</span>
        <strong :class="cell.tone">{{ cell.face }}</strong>
        <Sparkline
          v-if="cell.spark"
          class="nt-hud__spark"
          :points="sparkPoints(cell.spark)"
          :tone="cell.spark === 'equity' ? cell.tone : 'flat'"
        />
        <small v-if="cell.sub">{{ cell.sub }}</small>
      </button>
    </section>

    <div class="nt-monitor__stage">
      <CanvasStage
        ref="stage"
        :graph="graph"
        :selected-id="selectedId"
        :hot-ids="hotIds"
        :paused="paused"
        :promote="spec.promote || undefined"
        :thicken="spec.thicken"
        :veil="spec.veil"
        :calm="spec.calm"
        :band-key="bandKey"
        :flash="flash ?? undefined"
        :flow="edgeFlow"
        :kill-active="killActive"
        :signal-scores="signalScores"
        @select="select"
        @edge="selectEdge"
      />
      <p v-if="offday" class="nt-monitor__offday">今日休市，链路自检中</p>
    </div>

    <TerminalStrip
      :input="input"
      :rows="tapeRows"
      :first-pane="spec.firstPane"
      :band-key="bandKey"
      @select="select"
      @flash="onTapeFlash"
    />

    <div v-if="picked" class="nt-pop" role="dialog" aria-modal="true" @click.self="closeDetail">
      <section class="nt-pop__sheet" :class="{ 'nt-pop__sheet--deep': detailPanel }">
        <header>
          <i class="dot" :class="`is-${picked.status}`"></i>
          <div>
            <h2>{{ detailWhere === 'runtime' ? dutyOf(picked.title).name : picked.title }}</h2>
            <p>{{ detailWhere === 'runtime' ? dutyOf(picked.title).role : picked.sub || picked.detail }}</p>
          </div>
          <button type="button" @click="closeDetail">关闭</button>
        </header>
        <p class="nt-pop__face" :class="picked.tone">{{ picked.face || STATUS_WORD[picked.status] || '未返回' }}</p>
        <DetailSheet v-if="detailPanel" :panel="detailPanel" @exit="onDetailExit" />
        <div v-else class="nt-pop__grid">
          <article v-for="card in cards" :key="card.id">
            <span>{{ card.title }}</span>
            <strong :class="card.tone">{{ card.face }}</strong>
            <small v-if="card.sub">{{ card.sub }}</small>
            <button
              v-if="card.id.startsWith('symbol:') || card.id === 'action:trade' || card.id.startsWith('book:')"
              type="button"
              @click="select(card.id)"
            >
              打开
            </button>
          </article>
        </div>
        <form v-if="configFields.length" class="nt-pop__form" @submit.prevent="saveDraft">
          <h3>配置</h3>
          <label v-for="field in configFields" :key="field.key">
            <span>{{ field.label }}</span>
            <select v-if="field.kind === 'select'" v-model="draft[field.key]">
              <option v-for="option in field.options" :key="option.value" :value="option.value">{{ option.label }}</option>
            </select>
            <select v-else-if="field.kind === 'bool'" v-model="draft[field.key]">
              <option value="true">打开</option>
              <option value="false">关闭</option>
            </select>
            <input v-else v-model="draft[field.key]" :type="field.kind === 'secret' ? 'password' : 'text'" :placeholder="field.hint" />
          </label>
          <ConfigRevisionPanel compact :snapshot="configSnapshot" :preview="configImpact" @refresh="refreshConfigReceipts" />
          <button type="submit" :disabled="saving || !configSnapshot">{{ saving ? '处理中' : configImpact ? '确认保存新版本' : '预览配置变更' }}</button>
          <button type="button" :disabled="saving" @click="loadDraft(configFields)">重新读取配置</button>
          <small>{{ configMsg || '留空的项不会覆盖原来的配置' }}</small>
        </form>
        <section v-if="detailWhere === 'runtime'" class="nt-pop__log">
          <h3>运行记录</h3>
          <p>当前 {{ STATUS_WORD[picked.status] || picked.status }} · {{ cards[0]?.sub || '没有更多状态' }}</p>
          <p v-if="serviceLogs.length === 0">审计里没有这条服务的操作记录。这里只能重新检查它是否在线，没有启停开关。</p>
          <ul v-else>
            <li v-for="item in serviceLogs" :key="item.id">{{ item.time }} {{ item.action }} {{ item.target }}</li>
          </ul>
          <button type="button" @click="pull">重新检查</button>
        </section>
      </section>
    </div>

    <div v-else-if="edgeDetail" class="nt-pop" role="dialog" aria-modal="true" aria-label="连线检视" @click.self="closeDetail">
      <section class="nt-pop__sheet nt-pop__sheet--deep">
        <header>
          <i class="dot is-idle"></i>
          <div>
            <h2>{{ edgeDetail.title }}</h2>
            <p>连线检视 · 结构关系与真实观测分别说明</p>
          </div>
          <button type="button" @click="closeDetail">关闭</button>
        </header>
        <nav aria-label="查看通道端点">
          <button type="button" class="nt-link" @click="select(edgeDetail.edge.from)">查看上游节点</button>
          <button type="button" class="nt-link" @click="select(edgeDetail.edge.to)">查看下游节点</button>
        </nav>
        <DetailSheet :panel="edgeDetail.panel" />
      </section>
    </div>

    <AlertCenter :extra="channelAlert" />
  </div>
</template>
