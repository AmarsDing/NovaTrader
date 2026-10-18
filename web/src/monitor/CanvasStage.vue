<script setup lang="ts">
/**
 * ComfyUI 式节点画布：卡片可拖动，空白拖动平移，滚轮缩放，双击复位。
 * 卡片与连线全部画在 2D canvas 上；配色从主题 token 实时读取，跟随明暗切换。
 * 动画分两档：已观测流量的纤维流向点 + 真实 WS 脉冲；未知链路只画静态结构。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { createVisualFrameLoop } from '@/theme/visual-quality'
import type { NodeStatus, Pulse, SceneGraph } from './types'
import { ACTIVITY_OFF_ALPHA, ACTIVITY_ON_ALPHA } from './activity'
import { ambientPeriod, flowWidth, HIGH_SCORE, KILL_EDGE_ID, type EdgeFlow } from './flow'
import { funnelCountText } from './funnel'
import {
  applyOffsets,
  buildStage,
  cubicPoint,
  fitTransform,
  hash01,
  hitNode,
  hitEdge,
  readStageTheme,
  reCurve,
  screenToWorld,
  stagePulse,
  withAlpha,
  zoomAt,
  type CubicCurve,
  type StageEdgeLayout,
  type StageLayout,
  type StageNodeLayout,
  type StagePoint,
  type StageRect,
  type StageTheme,
  type StageThicken,
  type ViewTransform,
} from './canvas-stage'

const props = defineProps<{
  graph: SceneGraph
  selectedId: string
  hotIds?: string[]
  paused: boolean
  /** 时段带提升的焦点牌（竞价→post:scan，尾盘→book:center） */
  promote?: string
  /** 时段带要加粗的束线 */
  thicken?: StageThicken
  /** 画布压暗系数：1 常态；午休 0.75；盘后 0.5 */
  veil?: number
  /** 休市/午休/盘后停掉环境流向点 */
  calm?: boolean
  /** 时段键：拖动偏移按时段分别持久化到 localStorage */
  bandKey?: string
  /** S4：事件流点行后要让对应节点「闪一下」。at 是触发时刻，动画按 Date.now() 自己衰减 */
  flash?: { id: string; at: number }
  /** S6：束线流量（键 = 边 id `from>to`）。rate null = 没观测 → 固定宽度 1.3px，不猜 */
  flow?: Record<string, EdgeFlow>
  /** S6：急停态——风控→账户束线转红加粗 + 账户牌呼吸环（§3.2） */
  killActive?: boolean
  /** S6：信号 id/symbol → 评分。≥85 的信号脉冲升级（更亮、三格尾迹、标签加粗） */
  signalScores?: Record<string, number>
}>()
const emit = defineEmits<{ select: [id: string]; edge: [id: string] }>()

const host = ref<HTMLElement | null>(null)
const canvas = ref<HTMLCanvasElement | null>(null)
const hoverId = ref('')
const hoverEdgeId = ref('')
/** S3：指针悬停在漏斗光带上（点它 = 选中扫描牌，详情窗里有同一组六层数字） */
const funnelHover = ref(false)
const failed = ref(false)
const dragState = ref<{ id: string; moved: boolean } | null>(null)

let ctx: CanvasRenderingContext2D | null = null
let theme: StageTheme = readStageThemeSafe()
let layout: StageLayout = buildStage(props.graph, { promote: props.promote, thicken: props.thicken })
let nodes: StageNodeLayout[] = layout.nodes
let edgeCurves = new Map<string, CubicCurve[]>()
let rects = new Map<string, StageRect>()
let view: ViewTransform = { x: 0, y: 0, scale: 1 }
let cssW = 0
let cssH = 0
let fitted = false
let stopped = false
let observer: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null
let pointer: {
  x: number
  y: number
  worldX: number
  worldY: number
  mode: '' | 'pan' | 'node' | 'band'
  id: string
  edgeId: string
  moved: boolean
  startX: number
  startY: number
  baseX: number
  baseY: number
} = {
  x: 0,
  y: 0,
  worldX: 0,
  worldY: 0,
  mode: '',
  id: '',
  edgeId: '',
  moved: false,
  startX: 0,
  startY: 0,
  baseX: 0,
  baseY: 0,
}
const offsets = new Map<string, StagePoint>()
const PULSE_TTL = 2400
/** S4：事件流点行的定位闪光时长。一圈向外扩散的描边，不用 shadowBlur，不占发光预算 */
const FLASH_MS = 620
/** S6：数字变化闪（§3.2）——卡片边框 300ms 内 α0.4→0，不放大、不跳动 */
const NUM_FLASH_MS = 300
/** S6：急停态账户牌呼吸环周期（§3.2：1.2s，sin 缓动 α0.35↔0.75） */
const KILL_BREATH_MS = 1200
/** S6：牌面主词的上一次快照，用来侦测「数字变化」；首次建台不闪 */
let prevFaces: Map<string, string> | null = null
const numFlash = new Map<string, number>()

/* Canvas 壳层使用同源材质；数据背板不做像素模糊，避免文字与数字变软。 */
function readMaterial() {
  const style = getComputedStyle(document.documentElement)
  const token = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback
  return {
    glass: token('--nt-glass-bg', theme.card),
    data: token('--nt-data-bg', theme.card),
    highlight: token('--nt-glass-highlight', 'transparent'),
    sheen: token('--nt-glass-sheen-color', 'transparent'),
    spaceCyan: token('--nt-space-cyan', 'transparent'),
    spaceBlue: token('--nt-space-blue', 'transparent'),
    accentBlue: token('--nt-accent-2', theme.fiberActive),
    gridOpacity: Number(token('--nt-grid-opacity', '0.22')),
    titleTint: Number(token('--nt-canvas-tint-alpha', '0.035')),
  }
}

let material = readMaterial()
let eco = document.documentElement.dataset.visualQuality === 'eco'
let reducedMotion = document.documentElement.dataset.motion === 'reduced' ||
  window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true

const renderLoop = createVisualFrameLoop({
  draw,
  canRender: () => !stopped && !props.paused && document.visibilityState !== 'hidden' &&
    document.documentElement.dataset.pageVisibility !== 'hidden',
  animate: () => !reducedMotion && hasAnimatedContent(),
  request: (callback) => requestAnimationFrame(callback),
  cancel: (id) => cancelAnimationFrame(id),
})

function syncVisualMode() {
  theme = readStageThemeSafe()
  material = readMaterial()
  eco = document.documentElement.dataset.visualQuality === 'eco'
  reducedMotion = document.documentElement.dataset.motion === 'reduced' || eco ||
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true
  renderLoop.refresh()
}

function hasAnimatedContent(): boolean {
  const now = Date.now()
  if (props.killActive === true) return true
  if (props.graph.pulses.some((pulse) => now >= pulse.at && now - pulse.at <= PULSE_TTL)) return true
  if (props.flash && now >= props.flash.at && now - props.flash.at <= FLASH_MS) return true
  if ([...numFlash.values()].some((at) => now >= at && now - at <= NUM_FLASH_MS)) return true
  return !props.calm && layout.edges.some((edge) => {
    const flow = props.flow?.[edge.id]
    return !edge.fallback && edgeLit(edge) && flow?.rate != null && Number.isFinite(flow.rate) &&
      flow.rate > 0 && flow.stalled !== true && flow.circuit !== true
  })
}

const selectedSet = computed(() => {
  const lit = new Set<string>(props.hotIds ?? [])
  if (props.selectedId) lit.add(props.selectedId)
  return lit
})

function readStageThemeSafe(): StageTheme {
  return readStageTheme()
}

function rebuild() {
  layout = buildStage(props.graph, { promote: props.promote, thicken: props.thicken })
  nodes = applyOffsets(layout.nodes, offsets)
  syncGeometry()
  /* S6 数字变化闪（§3.2）：牌面主词变了记一笔时刻，drawNumFlash 按 300ms 衰减。
   * 只比 face——角标「距今 Ns」每秒都在变，那不是数字变化，闪起来就成噪声了 */
  const faces = new Map(nodes.map((item) => [item.id, item.node.face]))
  if (prevFaces) {
    const at = Date.now()
    for (const [id, face] of faces) {
      const before = prevFaces.get(id)
      if (before != null && before !== face) numFlash.set(id, at)
    }
  }
  prevFaces = faces
  renderLoop.invalidate()
}

/* ---------------- 时段感知的拖动持久化 ----------------
 * 每个时段一套偏移：竞价盯扫描、尾盘盯账本，各自记住各自的摆法。
 * 关键：写盘用的是「当前内存偏移属于哪个时段」（savedBand），不是 props.bandKey——
 * watcher 触发时 props.bandKey 已经是新值，用它会把旧时段的摆法写进新时段的键里。
 * localStorage 失败（隐私模式/配额）静默降级为内存态。 */

let savedBand = props.bandKey ?? ''

function offsetsStorageKey(band: string): string {
  return `nt-monitor:offsets:v1:${band}`
}

function persistOffsets() {
  try {
    const data: Record<string, StagePoint> = {}
    for (const [id, point] of offsets) data[id] = point
    localStorage.setItem(offsetsStorageKey(savedBand), JSON.stringify(data))
  } catch {
    /* 静默：拖动只在本次会话生效 */
  }
}

function loadOffsets() {
  offsets.clear()
  savedBand = props.bandKey ?? ''
  try {
    const raw = localStorage.getItem(offsetsStorageKey(savedBand))
    if (!raw) return
    const data = JSON.parse(raw) as Record<string, unknown>
    for (const [id, point] of Object.entries(data)) {
      if (point && typeof point === 'object' && 'x' in point && 'y' in point) {
        const candidate = point as { x: unknown; y: unknown }
        offsets.set(id, { x: Number(candidate.x) || 0, y: Number(candidate.y) || 0 })
      }
    }
  } catch {
    /* 坏数据丢弃 */
  }
}

function syncGeometry() {
  rects = new Map(nodes.map((item) => [item.id, item.rect]))
  edgeCurves = new Map()
  for (const edge of layout.edges) {
    edgeCurves.set(edge.id, reCurve(edge, rects))
  }
}

onMounted(() => {
  const el = host.value
  const cv = canvas.value
  if (!el || !cv) return
  ctx = cv.getContext('2d')
  if (!ctx) {
    failed.value = true
    return
  }
  const measure = () => {
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    cssW = el.clientWidth
    cssH = el.clientHeight
    cv.width = Math.max(1, Math.round(cssW * dpr))
    cv.height = Math.max(1, Math.round(cssH * dpr))
    cv.style.width = `${cssW}px`
    cv.style.height = `${cssH}px`
    ctx?.setTransform(dpr, 0, 0, dpr, 0, 0)
    if (!fitted && cssW > 40 && cssH > 40) {
      fitView()
      fitted = true
    }
    renderLoop.invalidate()
  }
  loadOffsets()
  rebuild()
  measure()
  observer = new ResizeObserver(measure)
  observer.observe(el)
  themeObserver = new MutationObserver(syncVisualMode)
  themeObserver.observe(document.documentElement, {
    attributes: true, attributeFilter: ['data-theme', 'data-visual-quality', 'data-motion', 'data-page-visibility'],
  })
  document.addEventListener('visibilitychange', syncVisualMode)
  cv.addEventListener('pointerdown', onPointerDown)
  cv.addEventListener('pointermove', onPointerMove)
  cv.addEventListener('pointerup', onPointerUp)
  cv.addEventListener('pointercancel', onPointerUp)
  cv.addEventListener('wheel', onWheel, { passive: false })
  cv.addEventListener('dblclick', onDblClick)
  renderLoop.refresh()
})

onBeforeUnmount(() => {
  stopped = true
  renderLoop.dispose()
  persistOffsets()
  observer?.disconnect()
  themeObserver?.disconnect()
  document.removeEventListener('visibilitychange', syncVisualMode)
})

watch(
  () => props.graph,
  () => {
    rebuild()
    if (!fitted && cssW > 40 && cssH > 40) {
      fitView()
      fitted = true
    }
  },
)

watch(
  () => props.bandKey,
  () => {
    persistOffsets()
    loadOffsets()
    rebuild()
  },
)

watch(
  () => [props.promote, props.thicken] as const,
  () => rebuild(),
)

watch(
  () => props.paused,
  () => renderLoop.refresh(),
)

watch(
  () => [props.selectedId, props.hotIds, props.flow, props.killActive, props.flash, props.signalScores,
    props.calm, props.veil, props.graph.pulses],
  () => renderLoop.invalidate(),
  { deep: true },
)

function fitView() {
  view = fitTransform(layout.bounds, cssW, cssH)
  renderLoop.invalidate()
}

function onDblClick() {
  fitView()
}

function localPoint(event: PointerEvent | WheelEvent): StagePoint {
  const cv = canvas.value
  const box = cv?.getBoundingClientRect()
  return { x: event.clientX - (box?.left ?? 0), y: event.clientY - (box?.top ?? 0) }
}

function onPointerDown(event: PointerEvent) {
  const cv = canvas.value
  if (!cv || event.button !== 0) return
  cv.setPointerCapture(event.pointerId)
  const point = localPoint(event)
  const world = screenToWorld(view, point)
  const id = hitNode(nodes, world)
  /* S3：光带也是可点目标（选中扫描牌），但不可拖动 */
  const onBand = !id && inRect(world, layout.funnel?.hit ?? null)
  pointer = {
    x: point.x,
    y: point.y,
    worldX: world.x,
    worldY: world.y,
    mode: id ? 'node' : onBand ? 'band' : 'pan',
    id,
    edgeId: !id && !onBand ? hitEdge(edgeCurves, world, 6 / view.scale) : '',
    moved: false,
    startX: point.x,
    startY: point.y,
    baseX: view.x,
    baseY: view.y,
  }
  if (id) dragState.value = { id, moved: false }
  renderLoop.invalidate()
}

function inRect(point: StagePoint, rect: StageRect | null): boolean {
  if (!rect) return false
  return point.x >= rect.x && point.x <= rect.x + rect.w && point.y >= rect.y && point.y <= rect.y + rect.h
}

function onPointerMove(event: PointerEvent) {
  renderLoop.invalidate()
  const point = localPoint(event)
  const world = screenToWorld(view, point)
  if (!pointer.mode) {
    hoverId.value = hitNode(nodes, world)
    funnelHover.value = !hoverId.value && inRect(world, layout.funnel?.hit ?? null)
    hoverEdgeId.value = !hoverId.value && !funnelHover.value ? hitEdge(edgeCurves, world, 6 / view.scale) : ''
    return
  }
  const dx = point.x - pointer.startX
  const dy = point.y - pointer.startY
  if (Math.abs(dx) + Math.abs(dy) > 4) pointer.moved = true
  if (pointer.mode === 'pan' || pointer.mode === 'band') {
    /* band 模式也允许拖出平移：按下后移动超过阈值就转成平移，松手不触发选中 */
    view = { ...view, x: pointer.baseX + dx, y: pointer.baseY + dy }
    return
  }
  if (pointer.mode === 'node' && pointer.id) {
    if (Math.abs(dx) + Math.abs(dy) > 4 && dragState.value) dragState.value.moved = true
    const prev = offsets.get(pointer.id) ?? { x: 0, y: 0 }
    offsets.set(pointer.id, { x: prev.x + (world.x - pointer.worldX), y: prev.y + (world.y - pointer.worldY) })
    pointer.worldX = world.x
    pointer.worldY = world.y
    nodes = applyOffsets(layout.nodes, offsets)
    syncGeometry()
  }
}

function onPointerUp(event: PointerEvent) {
  renderLoop.invalidate()
  const point = localPoint(event)
  const moved = Math.abs(point.x - pointer.startX) + Math.abs(point.y - pointer.startY)
  const id = pointer.id
  const click = moved <= 4 && !pointer.moved && event.type !== 'pointercancel'
  const edgeId = pointer.edgeId
  const wasNodeClick = pointer.mode === 'node' && click
  const wasBandClick = pointer.mode === 'band' && click
  const wasEdgeClick =
    pointer.mode === 'pan' &&
    click &&
    edgeId &&
    hitEdge(edgeCurves, screenToWorld(view, point), 6 / view.scale) === edgeId
  const wasEmptyClick = click && !id && !edgeId && pointer.mode === 'pan'
  const draggedNode = pointer.mode === 'node' && (moved > 4 || pointer.moved)
  pointer.mode = ''
  pointer.id = ''
  pointer.edgeId = ''
  dragState.value = null
  if (draggedNode) persistOffsets()
  if (wasNodeClick && id) emit('select', id)
  else if (wasBandClick && layout.funnel) emit('select', layout.funnel.selectId)
  else if (wasEdgeClick) emit('edge', edgeId)
  else if (wasEmptyClick) emit('select', '')
}

function onWheel(event: WheelEvent) {
  event.preventDefault()
  const point = localPoint(event)
  const factor = event.deltaY < 0 ? 1.12 : 1 / 1.12
  view = zoomAt(view, point, view.scale * factor)
  renderLoop.invalidate()
}

/* ---------------- 绘制 ---------------- */

function draw() {
  const context = ctx
  if (!context || cssW < 10 || cssH < 10) return
  context.save()
  context.fillStyle = theme.bg
  context.fillRect(0, 0, cssW, cssH)
  if (!eco) {
    const radius = Math.max(cssW, cssH)
    for (const [x, y, color] of [[0, 0, material.spaceCyan], [cssW, cssH, material.spaceBlue]] as const) {
      const glow = context.createRadialGradient(x, y, 0, x, y, radius)
      glow.addColorStop(0, color)
      glow.addColorStop(1, 'transparent')
      context.fillStyle = glow
      context.fillRect(0, 0, cssW, cssH)
    }
  }
  context.translate(view.x, view.y)
  context.scale(view.scale, view.scale)

  drawGrid(context)
  drawColumns(context)
  drawEdges(context)
  drawFunnel(context)
  if (!reducedMotion) {
    drawAmbient(context)
    drawPulses(context)
  }
  /* 时段只压背景与管线，节点正文和未知状态保持可读。 */
  const veil = props.veil ?? 1
  if (veil < 1) {
    const area = visibleWorld()
    context.fillStyle = withAlpha(theme.bg, Math.min(0.36, (1 - veil) * 0.6))
    context.fillRect(area.x, area.y, area.w, area.h)
  }
  drawNodes(context)
  if (!reducedMotion) {
    drawFlash(context)
    drawNumFlash(context)
  } else numFlash.clear()
  context.restore()
}

function visibleWorld(): StageRect {
  const topLeft = screenToWorld(view, { x: 0, y: 0 })
  const bottomRight = screenToWorld(view, { x: cssW, y: cssH })
  return { x: topLeft.x, y: topLeft.y, w: bottomRight.x - topLeft.x, h: bottomRight.y - topLeft.y }
}

function drawGrid(context: CanvasRenderingContext2D) {
  const area = visibleWorld()
  const step = 44
  const startX = Math.floor(area.x / step) * step
  const startY = Math.floor(area.y / step) * step
  context.strokeStyle = withAlpha(theme.grid, material.gridOpacity)
  context.lineWidth = 0.65 / view.scale
  context.beginPath()
  for (let x = startX; x < area.x + area.w + step; x += step) {
    context.moveTo(x, area.y)
    context.lineTo(x, area.y + area.h)
  }
  for (let y = startY; y < area.y + area.h + step; y += step) {
    context.moveTo(area.x, y)
    context.lineTo(area.x + area.w, y)
  }
  context.stroke()
  context.fillStyle = withAlpha(theme.text3, 0.34)
  for (let x = startX; x < area.x + area.w + step; x += step) {
    for (let y = startY; y < area.y + area.h + step; y += step) {
      context.fillRect(x - 0.65, y - 0.65, 1.3, 1.3)
    }
  }
}

function drawColumns(context: CanvasRenderingContext2D) {
  context.font = `600 11px ${theme.fontMono}`
  context.textBaseline = 'alphabetic'
  for (const column of layout.columns) {
    context.fillStyle = theme.fiberActive
    context.fillRect(column.x, 17, 3, 11)
    context.fillStyle = theme.text2
    context.fillText(column.title, column.x + 10, 28)
    context.fillStyle = withAlpha(theme.text3, 0.3)
    context.fillRect(column.x, 35, 150, 0.8)
    context.fillStyle = withAlpha(theme.fiberActive, 0.55)
    context.fillRect(column.x, 35, 24, 1.3)
  }
}

function edgeLit(edge: StageEdgeLayout): boolean {
  const lit = selectedSet.value
  if (lit.size === 0) return true
  return lit.has(edge.from) || lit.has(edge.to)
}

function drawEdges(context: CanvasRenderingContext2D) {
  context.lineCap = 'round'
  const selection = selectedSet.value
  for (const edge of layout.edges) {
    const curves = edgeCurves.get(edge.id)
    if (!curves) continue
    const flow = props.flow?.[edge.id]
    /* S6 急停态（§3.2）：风控→账户的边 --nt-danger α0.6 宽 2.4，盖过时段带加粗与选中提亮 */
    const kill = props.killActive === true && edge.id === KILL_EDGE_ID && !edge.fallback
    /* S6 模型熔断（§4.4.3）：模型→维度的边转 --nt-danger α0.4，流向点停（见 drawAmbient） */
    const circuit = flow?.circuit === true && !edge.fallback
    const active = !edge.fallback && selection.size > 0 && (selection.has(edge.from) || selection.has(edge.to))
    const dim = !edge.fallback && selection.size > 0 && !active
    const boost = edge.thick === true && !edge.fallback && !kill && !circuit
    const color = kill || circuit ? theme.status.down : theme.domain[edge.domain] || theme.fiber
    /* S6 束线流量映射（§3.2）：宽 = 0.8 + min(2.4, log10(每分钟条数+1)*0.9) px，
     * 估不出固定 1.3px 不猜；选中相邻 +0.4px；时段带加粗再 +1.3px。
     * 只动线宽与透明度——glow 预算留给异常与高分信号。 */
    const main = kill ? 2.4 : flowWidth(flow?.rate ?? null) + (active ? 0.4 : 0) + (boost ? 1.3 : 0)
    curves.forEach((curve, index) => {
      context.beginPath()
      context.moveTo(curve[0], curve[1])
      context.bezierCurveTo(curve[2], curve[3], curve[4], curve[5], curve[6], curve[7])
      /* 结构线保持可辨；选中提亮，光带兜底仍压暗。只改变透明度，不引入发光。 */
      const pair: [number, number] = edge.fallback
        ? [0.05, 0.05]
        : kill
          ? [0.6, 0.35]
          : circuit
            ? [0.55, 0.28]
            : dim
              ? [0.15, 0.08]
              : active
                ? [0.72, 0.34]
                : [0.42, 0.17]
      context.strokeStyle = withAlpha(color, Math.min(0.85, pair[index === 0 ? 0 : 1] + (boost ? 0.28 : 0)))
      context.lineWidth = index === 0 ? main : Math.max(0.6, main * 0.69)
      context.stroke()
    })
  }
}

/* ---------------- S3 漏斗光带渲染（方案 §4.4.2） ---------------- */

function isLightTheme(): boolean {
  if (typeof document === 'undefined') return false
  const attr = document.documentElement.getAttribute('data-theme')
  if (attr) return attr === 'light'
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: light)').matches === true
}

function strokePolyline(context: CanvasRenderingContext2D, points: StagePoint[]) {
  if (points.length < 2) return
  context.beginPath()
  context.moveTo(points[0].x, points[0].y)
  for (let index = 1; index < points.length; index += 1) context.lineTo(points[index].x, points[index].y)
  context.stroke()
}

function drawFunnel(context: CanvasRenderingContext2D) {
  const funnel = layout.funnel
  if (!funnel) return
  const light = isLightTheme()
  const accent = theme.fiberActive
  const on = props.selectedId === funnel.selectId

  /* 主干六段：青色渐变 alpha 0.18→0.30（浅色主题统一压到 0.12，§9/§11）；
   * 缺失层画 --nt-text-3 alpha 0.15 虚线段并标「未返回」，不 0 填充不跳过 */
  funnel.segments.forEach((segment, index) => {
    const { rect } = segment
    if (segment.missing) {
      context.save()
      context.setLineDash([4, 3])
      context.fillStyle = withAlpha(theme.text3, 0.05)
      context.fillRect(rect.x, rect.y, rect.w, rect.h)
      context.strokeStyle = withAlpha(theme.text3, 0.5)
      context.lineWidth = 1
      context.strokeRect(rect.x, rect.y, rect.w, rect.h)
      context.restore()
      return
    }
    const spread = funnel.segments.length > 1 ? index / (funnel.segments.length - 1) : 0
    const grad = context.createLinearGradient(rect.x, rect.y, rect.x + rect.w, rect.y + rect.h)
    if (light) {
      grad.addColorStop(0, withAlpha(accent, 0.12))
      grad.addColorStop(1, withAlpha(accent, 0.12))
    } else {
      grad.addColorStop(0, withAlpha(accent, 0.18 + spread * 0.04))
      grad.addColorStop(1, withAlpha(accent, 0.26 + spread * 0.04))
    }
    context.beginPath()
    roundedRect(context, rect.x, rect.y, rect.w, rect.h, 3)
    context.fillStyle = grad
    context.fill()
    context.strokeStyle = withAlpha(accent, light ? 0.3 : on ? 0.6 : 0.4)
    context.lineWidth = 1
    context.stroke()
  })

  /* 层标签：段内计数（mono 10px），段下「名称 · 转化率」；交界处读法 = 数量 / 转化率 */
  context.save()
  context.textAlign = 'center'
  context.textBaseline = 'middle'
  for (const segment of funnel.segments) {
    const cx = segment.rect.x + segment.rect.w / 2
    if (segment.missing) {
      context.font = `9px ${theme.fontMono}`
      context.fillStyle = theme.text3
      context.fillText('未返回', cx, segment.rect.y + segment.rect.h / 2)
    } else {
      context.font = `10px ${theme.fontMono}`
      context.fillStyle = theme.text1
      context.fillText(funnelCountText(segment.count), cx, segment.rect.y + segment.rect.h / 2)
    }
    const sub = [segment.name, segment.rate].filter(Boolean).join(' · ')
    if (sub) {
      context.font = `9px ${theme.fontSans}`
      context.fillStyle = theme.text2
      context.fillText(clipText(context, sub, segment.rect.w + 8), cx, segment.rect.y + segment.rect.h + 11)
    }
  }
  context.restore()

  /* 侧漏：--nt-warn alpha 0.25 虚线 [3,3]，从交界下折汇入「漏选」牌；标签 = 只数 + 最大过滤原因 */
  const warn = theme.domain.miss
  context.save()
  context.setLineDash([3, 3])
  context.strokeStyle = withAlpha(warn, 0.25)
  context.lineWidth = 1
  for (const leak of funnel.leaks) strokePolyline(context, leak.stub)
  if (funnel.collector) strokePolyline(context, funnel.collector)
  context.restore()
  context.font = `9px ${theme.fontMono}`
  context.textBaseline = 'alphabetic'
  context.fillStyle = withAlpha(warn, 0.75)
  for (const leak of funnel.leaks) {
    const joint = leak.stub[1]
    context.fillText(`${funnelCountText(leak.count)} ${leak.label}`, joint.x + 5, joint.y - 3)
  }

  /* 口径提醒（候选上限 200 之类）写在光带左下，让开侧漏横线区 */
  if (funnel.note) {
    context.font = `9px ${theme.fontSans}`
    context.fillStyle = theme.text3
    context.fillText(funnel.note, funnel.band.x, funnel.band.y + funnel.band.h + 30 + funnel.leaks.length * 8)
  }
}

function drawAmbient(context: CanvasRenderingContext2D) {
  if (props.calm) return
  const now = performance.now()
  for (const edge of layout.edges) {
    if (edge.fallback) continue
    if (!edgeLit(edge)) continue
    const curves = edgeCurves.get(edge.id)
    if (!curves || curves.length === 0) continue
    const flow = props.flow?.[edge.id]
    /* S6 模型熔断（§4.4.3）：停止流向点，边本身已转红 */
    if (flow?.circuit === true) continue
    const phase = hash01(`ambient:${edge.id}`)
    /* S6 断流（§3.2）：流向点停住并转 --nt-warn α0.35——位置冻在相位处，不再前进 */
    if (flow?.stalled === true) {
      const stuck = cubicPoint(curves[0], phase % 1)
      context.beginPath()
      context.arc(stuck.x, stuck.y, 1.7, 0, Math.PI * 2)
      context.fillStyle = withAlpha(theme.status.gap, 0.35)
      context.fill()
      continue
    }
    /* 未测到流量或测到零时不造出「正在流动」的印象。 */
    if (flow?.rate == null || flow.rate <= 0 || !Number.isFinite(flow.rate)) continue
    /* S6 周期由速率映射 1200–8000ms；估不出保持固定周期（§11 行 6），不外推 */
    const period = ambientPeriod(flow?.rate ?? null)
    const t = (now / period + phase) % 1
    const point = cubicPoint(curves[0], t)
    context.beginPath()
    context.arc(point.x, point.y, 1.7, 0, Math.PI * 2)
    context.fillStyle = withAlpha(theme.domain[edge.domain] || theme.fiber, 0.5)
    context.fill()
  }
}

function drawPulses(context: CanvasRenderingContext2D) {
  const now = Date.now()
  const pulses = props.graph.pulses.filter((pulse) => now - pulse.at <= PULSE_TTL)
  for (const pulse of pulses) {
    const mapped = stagePulse(pulse)
    if (!mapped) continue
    const curve = pulseCurve(mapped.from, mapped.to)
    if (!curve) continue
    const t = Math.min(1, Math.max(0, (now - pulse.at) / PULSE_TTL))
    const point = cubicPoint(curve, t)
    /* S6 高分信号脉冲升级（§3.2）：≥85 分 → r4.5 + shadowBlur 8 + 三格尾迹 + 标签加粗。
     * 发光预算内的五个对象之一；分数查不到（signalScores 没有这条）按普通脉冲画，不猜 */
    const score = pulse.identifier.kind === 'signal' ? props.signalScores?.[pulse.identifier.value] : undefined
    const hot = score != null && score >= HIGH_SCORE
    /* 尾迹：沿路往回画渐隐点。普通 2 格 α0.5/0.25，高分 3 格 α0.6/0.35/0.15（§3.2） */
    const tailAlpha = hot ? [0.6, 0.35, 0.15] : [0.5, 0.25]
    for (let back = 1; back <= tailAlpha.length; back += 1) {
      const tail = cubicPoint(curve, Math.max(0, t - back * 0.045))
      context.beginPath()
      context.arc(tail.x, tail.y, (hot ? 2.8 : 2.2) - back * 0.6, 0, Math.PI * 2)
      context.fillStyle = withAlpha(theme.fiberActive, tailAlpha[back - 1])
      context.fill()
    }
    context.save()
    if (hot && !eco) {
      context.shadowColor = theme.fiberActive
      context.shadowBlur = 8
    }
    context.beginPath()
    context.arc(point.x, point.y, hot ? 4.5 : 3, 0, Math.PI * 2)
    context.fillStyle = withAlpha(theme.fiberActive, 0.95)
    context.fill()
    context.restore()
    const label = pulseLabel(pulse)
    if (label) {
      context.font = `${hot ? '700 ' : ''}9px ${theme.fontMono}`
      context.textBaseline = 'alphabetic'
      context.fillStyle = withAlpha(theme.fiberActive, hot ? 0.95 : 0.85)
      context.fillText(label, point.x + 7, point.y - 5)
    }
  }
}

function pulseCurve(from: string, to: string): CubicCurve | null {
  if (from === '') {
    /* 入口侧来路（如行情推送）：从扫描牌左缘外 80px 处汇入 */
    const target = rects.get(to)
    if (!target) return null
    const x1 = target.x
    const y1 = target.y + target.h / 2
    const x0 = x1 - 80
    return [x0, y1, x0 + 30, y1, x1 - 30, y1, x1, y1]
  }
  const edge = layout.edges.find((item) => item.from === from && item.to === to)
  if (edge) return edgeCurves.get(edge.id)?.[0] ?? null
  /* 场景边不在台上（如补边缺失）时画一条直连 */
  const a = rects.get(from)
  const b = rects.get(to)
  if (!a || !b) return null
  const x0 = a.x + a.w
  const y0 = a.y + a.h / 2
  const x1 = b.x
  const y1 = b.y + b.h / 2
  const bend = Math.max(36, (x1 - x0) * 0.45)
  return [x0, y0, x0 + bend, y0, x1 - bend, y1, x1, y1]
}

function pulseLabel(pulse: Pulse): string {
  const value = pulse.identifier.value.length > 10 ? `${pulse.identifier.value.slice(0, 10)}…` : pulse.identifier.value
  return `${pulse.identifier.kind}:${value}`
}

function drawNodes(context: CanvasRenderingContext2D) {
  for (const item of nodes) {
    drawNode(context, item)
  }
}

/** S4：事件流点某行 → 选中对应画布节点并闪一下（§4.6 交互列）。
 * 一圈描边从牌缘向外扩 6px，620ms 内透明度与线宽一起衰减到 0。 */
function drawFlash(context: CanvasRenderingContext2D) {
  const flash = props.flash
  if (!flash?.id) return
  const age = Date.now() - flash.at
  if (!Number.isFinite(age) || age < 0 || age > FLASH_MS) return
  const item = nodes.find((node) => node.id === flash.id)
  if (!item) return
  const progress = age / FLASH_MS
  const grow = 6 * progress
  const rect = item.rect
  context.beginPath()
  roundedRect(context, rect.x - grow, rect.y - grow, rect.w + grow * 2, rect.h + grow * 2, 8 + grow)
  context.strokeStyle = withAlpha(theme.fiberActive, 0.6 * (1 - progress))
  context.lineWidth = 0.5 + 1.5 * (1 - progress)
  context.stroke()
}

/** S6 数字变化闪（§3.2）：值变化时卡片边框 300ms 内 α0.4→0（--nt-accent），不放大、不跳动。
 * 过期的条目顺手清掉，Map 不会越攒越大。 */
function drawNumFlash(context: CanvasRenderingContext2D) {
  if (numFlash.size === 0) return
  const now = Date.now()
  for (const [id, at] of numFlash) {
    const age = now - at
    if (!Number.isFinite(age) || age < 0 || age > NUM_FLASH_MS) {
      numFlash.delete(id)
      continue
    }
    const item = nodes.find((node) => node.id === id)
    if (!item) continue
    const rect = item.rect
    context.beginPath()
    roundedRect(context, rect.x, rect.y, rect.w, rect.h, 8)
    context.strokeStyle = withAlpha(theme.fiberActive, 0.4 * (1 - age / NUM_FLASH_MS))
    context.lineWidth = 1
    context.stroke()
  }
}

function drawNode(context: CanvasRenderingContext2D, item: StageNodeLayout) {
  const { rect, node, domain } = item
  const color = theme.domain[domain] || theme.text3
  const on = props.selectedId === item.id
  const hot = (props.hotIds ?? []).includes(item.id)
  const hover = hoverId.value === item.id
  const dragging = dragState.value?.id === item.id
  const focus = node.rank === 'focus' || item.promoted === true
  const radius = 8

  /* 牌体 */
  context.save()
  context.beginPath()
  roundedRect(context, rect.x, rect.y, rect.w, rect.h, radius)
  if (on && !eco) {
    context.shadowColor = withAlpha(theme.fiberActive, 0.24)
    context.shadowBlur = 14
  }
  context.fillStyle = dragging ? theme.titleBand : eco ? theme.card : material.glass
  context.fill()
  context.restore()
  context.lineWidth = on ? 1.8 : 1
  context.strokeStyle = on
    ? theme.fiberActive
    : hot || hover
      ? withAlpha(color, 0.85)
      : theme.cardBorder
  context.stroke()

  /* 标题带 */
  const bandH = 20
  context.save()
  context.beginPath()
  roundedRect(context, rect.x, rect.y, rect.w, rect.h, radius)
  context.clip()
  /* 高不透明数据背板覆盖网格，透光只留在玻璃标题与外壳。 */
  context.fillStyle = material.data
  context.fillRect(rect.x + 1, rect.y + bandH, rect.w - 2, rect.h - bandH - 1)
  context.fillStyle = theme.titleBand
  context.fillRect(rect.x, rect.y, rect.w, bandH)
  context.fillStyle = withAlpha(color, material.titleTint)
  context.fillRect(rect.x, rect.y, rect.w, bandH)
  context.fillStyle = withAlpha(color, 0.9)
  context.fillRect(rect.x, rect.y + 3, 2.5, bandH - 6)
  if (!eco) {
    const sheen = context.createLinearGradient(rect.x, rect.y, rect.x + rect.w, rect.y + bandH)
    sheen.addColorStop(0, material.sheen)
    sheen.addColorStop(0.72, 'transparent')
    context.fillStyle = sheen
    context.fillRect(rect.x, rect.y, rect.w, bandH)
    context.strokeStyle = material.highlight
    context.lineWidth = 0.75
    context.beginPath()
    context.moveTo(rect.x + radius, rect.y + 1)
    context.lineTo(rect.x + rect.w - radius, rect.y + 1)
    context.stroke()
  }
  if (on) {
    const rail = context.createLinearGradient(rect.x, 0, rect.x + rect.w, 0)
    rail.addColorStop(0, theme.fiberActive)
    rail.addColorStop(1, material.accentBlue)
    context.fillStyle = rail
    context.fillRect(rect.x, rect.y + rect.h - 2, rect.w, 2)
  }
  context.restore()
  context.fillStyle = theme.cardBorder
  context.fillRect(rect.x, rect.y + bandH, rect.w, 0.75)

  /* 状态点 + 标题 + 职责 */
  const dotColor = theme.status[node.status as NodeStatus] || theme.text3
  context.beginPath()
  context.arc(rect.x + 10, rect.y + bandH / 2, 3, 0, Math.PI * 2)
  context.fillStyle = dotColor
  context.fill()
  context.font = `600 11px ${theme.fontSans}`
  context.textBaseline = 'middle'
  context.fillStyle = theme.text1
  context.fillText(node.title, rect.x + 18, rect.y + bandH / 2 + 0.5)
  if (item.role) {
    context.font = `9px ${theme.fontSans}`
    context.fillStyle = theme.text2
    const roleWidth = context.measureText(item.role).width
    context.fillText(item.role, rect.x + rect.w - roleWidth - 8, rect.y + bandH / 2 + 0.5)
  }

  /* 牌面主词与副行 */
  const faceSize = focus ? 20 : 15
  const faceY = rect.y + bandH + (rect.h - bandH) / 2 - (node.sub ? 6 : 0)
  /* S2 新鲜度角标占住主词行右缘，主词给它让位 */
  let badgeWidth = 0
  if (node.fresh?.text) {
    context.font = `9px ${theme.fontMono}`
    badgeWidth = context.measureText(node.fresh.text).width + 10
  }
  context.font = `${focus ? 700 : 600} ${faceSize}px ${theme.fontMono}`
  context.fillStyle = faceColor(item)
  context.fillText(clipText(context, node.face || '—', rect.w - 20 - badgeWidth), rect.x + 10, faceY)
  if (node.fresh?.text) {
    context.font = `9px ${theme.fontMono}`
    context.fillStyle =
      node.fresh.tone === 'danger' ? theme.status.down : node.fresh.tone === 'warn' ? theme.status.gap : theme.text2
    context.fillText(node.fresh.text, rect.x + rect.w - badgeWidth, faceY)
  }
  if (node.sub) {
    context.font = `9.5px ${theme.fontSans}`
    context.fillStyle = theme.text2
    context.fillText(clipText(context, node.sub, rect.w - 20), rect.x + 10, faceY + (focus ? 16 : 13))
  }

  /* S2 活动条：底缘 2px，60 格每格 1 秒（§4.4.1）。null 格（没观测到）留白 */
  if (node.activity && node.activity.length > 0) {
    const inset = 8
    const pitch = Math.max(1.2, (rect.w - inset * 2) / node.activity.length)
    const cellW = Math.max(0.8, pitch - 0.4)
    const barY = rect.y + rect.h - 4
    for (let index = 0; index < node.activity.length; index += 1) {
      const cell = node.activity[index]
      if (cell === null) continue
      context.fillStyle = withAlpha(theme.fiberActive, cell ? ACTIVITY_ON_ALPHA : ACTIVITY_OFF_ALPHA)
      context.fillRect(rect.x + inset + index * pitch, barY, cellW, 2)
    }
  }

  /* 端口 */
  drawPort(context, rect.x, rect.y + rect.h / 2, theme.text3)
  drawPort(context, rect.x + rect.w, rect.y + rect.h / 2, withAlpha(color, 0.9))

  /* attention / down 的外圈提示（glow 预算内的页面级例外：仅异常与待确认） */
  if (node.status === 'attention' || node.status === 'down') {
    context.beginPath()
    roundedRect(context, rect.x - 2, rect.y - 2, rect.w + 4, rect.h + 4, radius + 2)
    context.strokeStyle = withAlpha(dotColor, 0.5)
    context.lineWidth = 1
    context.stroke()
  }

  /* S6 急停态（§3.2）：账户牌外圈 1.5px 红环，呼吸 1.2s（sin 缓动，α0.35↔0.75）。
   * 发光预算内的五个对象之一——用透明度呼吸表达，不叠 shadowBlur。 */
  if (props.killActive === true && domain === 'account') {
    const phase = (performance.now() % KILL_BREATH_MS) / KILL_BREATH_MS
    const breath = reducedMotion ? 0.7 : 0.55 + 0.2 * Math.sin(phase * Math.PI * 2)
    context.beginPath()
    roundedRect(context, rect.x - 3.5, rect.y - 3.5, rect.w + 7, rect.h + 7, radius + 3.5)
    context.strokeStyle = withAlpha(theme.status.down, breath)
    context.lineWidth = 1.5
    context.stroke()
  }
}

function faceColor(item: StageNodeLayout): string {
  const node = item.node
  if (item.domain === 'account') {
    if (node.tone === 'up') return theme.up
    if (node.tone === 'down') return theme.down
  }
  if (node.status === 'gap' || node.status === 'down') return theme.text2
  return theme.text1
}

function drawPort(context: CanvasRenderingContext2D, x: number, y: number, color: string) {
  context.beginPath()
  context.arc(x, y, 3.7, 0, Math.PI * 2)
  context.fillStyle = theme.bg
  context.fill()
  context.strokeStyle = color
  context.lineWidth = 1.3
  context.stroke()
  context.beginPath()
  context.arc(x, y, 1.2, 0, Math.PI * 2)
  context.fillStyle = color
  context.fill()
}

function roundedRect(context: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  const radius = Math.min(r, w / 2, h / 2)
  context.moveTo(x + radius, y)
  context.arcTo(x + w, y, x + w, y + h, radius)
  context.arcTo(x + w, y + h, x, y + h, radius)
  context.arcTo(x, y + h, x, y, radius)
  context.arcTo(x, y, x + w, y, radius)
  context.closePath()
}

function clipText(context: CanvasRenderingContext2D, text: string, maxWidth: number): string {
  if (context.measureText(text).width <= maxWidth) return text
  let clipped = text
  while (clipped.length > 1 && context.measureText(`${clipped}…`).width > maxWidth) {
    clipped = clipped.slice(0, -1)
  }
  return `${clipped}…`
}

/** 暴露给页面：清空当前时段的拖动并复位视角 */
function resetLayout() {
  offsets.clear()
  persistOffsets()
  nodes = applyOffsets(layout.nodes, offsets)
  syncGeometry()
  fitView()
}

defineExpose({ resetLayout })
</script>

<template>
  <div ref="host" class="nt-stage" :class="{ 'is-dragging': dragState != null }">
    <canvas
      v-show="!failed"
      ref="canvas"
      :style="{ cursor: dragState ? 'grabbing' : hoverId || funnelHover || hoverEdgeId ? 'pointer' : 'grab' }"
    ></canvas>
    <div v-if="failed" class="nt-stage__failed">画布不可用，监控链路改读侧栏与终端带</div>
  </div>
</template>
