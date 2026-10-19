/**
 * ComfyUI 式画布的纯几何引擎：列布局、边束、视口变换、命中检测。
 * 不碰 DOM、不碰 Vue，便于 vitest 直测。
 *
 * 世界坐标约定：y 向下增长，单位是「未缩放的 CSS 像素」。
 * 视口变换：screen = world * scale + offset。
 */
import { funnelRate, funnelWidths, FUNNEL_MIN_W, type FunnelModel } from './funnel'
import { PALETTE } from '../theme/tokens'
import type { NodeStatus, Pulse, SceneGraph, SceneNode } from './types'

export interface StagePoint {
  x: number
  y: number
}

export interface StageRect {
  x: number
  y: number
  w: number
  h: number
}

export interface ViewTransform {
  x: number
  y: number
  scale: number
}

/** 三次贝塞尔：[x0, y0, c1x, c1y, c2x, c2y, x1, y1] */
export type CubicCurve = [number, number, number, number, number, number, number, number]

export type DomainKey =
  | 'source'
  | 'scan'
  | 'tech'
  | 'news'
  | 'capital'
  | 'overseas'
  | 'synth'
  | 'model'
  | 'signal'
  | 'miss'
  | 'risk'
  | 'account'
  | 'runtime'

export interface StageNodeLayout {
  id: string
  node: SceneNode
  rect: StageRect
  domain: DomainKey
  /** 一句话职责，标题栏右侧的小字 */
  role: string
  /** 时段带提升的焦点牌（竞价→post:scan，尾盘→book:center）：face 用 20px 大字 */
  promoted?: boolean
}

export interface StageThicken {
  /** 进入这些节点的所有束线加粗（竞价时段：['post:scan']） */
  into?: string[]
  /** 精确的边 id 加粗，格式 `from>to`（尾盘时段：['post:risk>book:center']） */
  edges?: string[]
}

export interface StageOptions {
  /** 提升为焦点牌的节点 id；加宽并向左生长，不挪动其它节点（拓扑永不变） */
  promote?: string
  /** 时段带要求加粗的束线 */
  thicken?: StageThicken
}

export interface StageEdgeLayout {
  id: string
  from: string
  to: string
  domain: DomainKey
  /** 出/入端口扇出序号，拖动后按原序号重算曲线 */
  outIndex: number
  outCount: number
  inIndex: number
  inCount: number
  /** 纤维束：同一逻辑边的 2 股错开曲线 */
  curves: CubicCurve[]
  /** 时段带要求加粗的束线（竞价：进扫描；尾盘：风控→账户） */
  thick?: boolean
  /** S3：主干被漏斗光带取代的旧束线（扫描→四维→综合→信号），压暗兜底（§4.4.3） */
  fallback?: boolean
}

/* ---------------- S3 漏斗光带布局 ---------------- */

export interface StageFunnelSegment {
  key: string
  name: string
  count: number | null
  rect: StageRect
  /** 交界处转化率（本层 / 上一层），算不出为空串 */
  rate: string
  /** 数据缺失层：画 --nt-text-3 α0.15 虚线段并标「未返回」，不 0 填充不跳过 */
  missing: boolean
}

export interface StageFunnelLeak {
  /** 侧漏水平支线：主干右端 → 收集竖线 */
  stub: StagePoint[]
  label: string
  count: number | null
}

export interface StageFunnelLayout {
  segments: StageFunnelSegment[]
  leaks: StageFunnelLeak[]
  /** 汇入「漏选」牌的收集线（竖线 + 入牌横线）；漏选牌不在台上或没有侧漏时为 null */
  collector: StagePoint[] | null
  band: StageRect
  note: string
  /** 点击光带 = 选中扫描牌（详情窗里有同一组六层数字） */
  hit: StageRect
  selectId: string
}

export interface StageColumn {
  title: string
  x: number
}

export interface StageLayout {
  nodes: StageNodeLayout[]
  edges: StageEdgeLayout[]
  columns: StageColumn[]
  bounds: StageRect
  /** S3 漏斗光带；graph.funnel 缺失（如外部构造的场景）时为 null，旧束线保持原样 */
  funnel: StageFunnelLayout | null
}

/* ---------------- 列与节点尺寸 ---------------- */

const COL_SOURCE_X = 40
const COL_SCAN_X = 300
const COL_DIM_X = 560
const COL_SYNTH_X = 820
const COL_VERDICT_X = 1080

const SOURCE_W = 150
const SOURCE_H = 48
const SOURCE_GAP = 12
const EXPERT_W = 168
const EXPERT_H = 54
const DIM_GAP = 14
const MODEL_W = 152
const MODEL_H = 46
const MODEL_GAP = 14
const VERDICT_W = 190
const VERDICT_H = 62
const ACCOUNT_W = 216
const ACCOUNT_H = 84

const TOP_Y = 64
/** 时段带焦点牌的加宽宽度（方案 §4.3「宽 280」；向左生长，避免压到右侧列） */
const PROMOTE_W = 280
const MODEL_ROW_Y = 46
/** 四维块顶部。模型条在它上方 */
const DIM_BLOCK_Y = 132

/* S3 漏斗光带几何常量（方案 §4.4.2 / §4.1）：横贯「02 研判」与「03 裁决」之间 */
const FUNNEL_X0 = COL_DIM_X
const FUNNEL_X1 = COL_VERDICT_X - 40
/** 光带顶缘：四维块底缘下方留空（主干最厚 96px 也不压块） */
const FUNNEL_BAND_OFFSET = 30
/** 段与段之间的缝 */
const FUNNEL_GAP = 6
/** 缺失层画多厚的虚线段 */
const FUNNEL_MISSING_H = 26
/** 侧漏收集线离主干右端的距离（绕过裁决列右侧通道，不穿牌） */
const FUNNEL_COLLECTOR_DX = 16
/** 段下缘到侧漏横线 / 标签区的间距 */
const FUNNEL_LEAK_DROP = 34
const FUNNEL_LABEL_DROP = 20

const ROLE: Record<string, string> = {
  'model:large': '消息 · 外围 · 综合',
  'model:small': '技术 · 资金',
  'model:token': '令牌计数',
  'post:scan': '规则漏斗',
  'dim:tech': '小模型打分',
  'dim:news': '大模型打分',
  'dim:capital': '小模型打分',
  'dim:overseas': '大模型打分',
  'dim:synth': '汇总成分',
  'post:desk': '待确认',
  'post:miss': '漏斗漏掉',
  'post:risk': '规则拦截',
  'book:center': '账本',
}

const DIMS = ['dim:tech', 'dim:news', 'dim:capital', 'dim:overseas']

/** 模型到它负责的专家的连线（scene-model 不产这条边，由舞台补） */
const MODEL_LINKS: [string, string][] = [
  ['model:small', 'dim:tech'],
  ['model:small', 'dim:capital'],
  ['model:large', 'dim:news'],
  ['model:large', 'dim:overseas'],
  ['model:large', 'dim:synth'],
]

/* ---------------- 域与颜色语义 ---------------- */

export function domainOf(id: string): DomainKey {
  if (id.startsWith('source:')) return 'source'
  if (id === 'post:scan') return 'scan'
  if (id === 'dim:tech') return 'tech'
  if (id === 'dim:news') return 'news'
  if (id === 'dim:capital') return 'capital'
  if (id === 'dim:overseas') return 'overseas'
  if (id === 'dim:synth') return 'synth'
  if (id.startsWith('model:')) return 'model'
  if (id === 'post:desk') return 'signal'
  if (id === 'post:miss') return 'miss'
  if (id === 'post:risk') return 'risk'
  if (id.startsWith('book:')) return 'account'
  return 'runtime'
}

function onStage(id: string): boolean {
  if (id.startsWith('source:')) return id !== 'source:quality'
  if (id.startsWith('model:')) return true
  if (id === 'post:scan' || id === 'post:desk' || id === 'post:miss' || id === 'post:risk') return true
  if (id === 'dim:synth' || DIMS.includes(id)) return true
  if (id === 'book:center') return true
  return false
}

/* ---------------- 布局 ---------------- */

export function buildStage(graph: SceneGraph, options?: StageOptions): StageLayout {
  const byId = new Map(graph.nodes.map((node) => [node.id, node]))
  const nodes: StageNodeLayout[] = []
  const rectOf = new Map<string, StageRect>()

  const place = (id: string, rect: StageRect): boolean => {
    const node = byId.get(id)
    if (!node) return false
    rectOf.set(id, rect)
    nodes.push({ id, node, rect, domain: domainOf(id), role: ROLE[id] ?? '' })
    return true
  }

  /* 数据源列：保持场景顺序 */
  const sources = graph.nodes.filter((node) => node.ring === 'source' && node.id !== 'source:quality')
  sources.forEach((node, index) => {
    place(node.id, { x: COL_SOURCE_X, y: TOP_Y + index * (SOURCE_H + SOURCE_GAP), w: SOURCE_W, h: SOURCE_H })
  })

  /* 模型条：四维块上方，小模型在左（管技术资金），大模型在右（管消息外围综合） */
  place('model:small', { x: COL_DIM_X, y: MODEL_ROW_Y, w: MODEL_W, h: MODEL_H })
  place('model:large', { x: COL_DIM_X + MODEL_W + MODEL_GAP, y: MODEL_ROW_Y, w: MODEL_W, h: MODEL_H })
  /* 计数牌接在模型组右侧，不能沿用综合列 x（会压住大模型牌）。 */
  place('model:token', { x: COL_DIM_X + (MODEL_W + MODEL_GAP) * 2, y: MODEL_ROW_Y, w: 132, h: MODEL_H })

  /* 四维块与扫描、综合 */
  const dimBlockH = DIMS.length * EXPERT_H + (DIMS.length - 1) * DIM_GAP
  const dimMidY = DIM_BLOCK_Y + dimBlockH / 2
  place('post:scan', { x: COL_SCAN_X, y: dimMidY - EXPERT_H / 2, w: EXPERT_W, h: EXPERT_H })
  DIMS.forEach((id, index) => {
    place(id, { x: COL_DIM_X, y: DIM_BLOCK_Y + index * (EXPERT_H + DIM_GAP), w: EXPERT_W, h: EXPERT_H })
  })
  place('dim:synth', { x: COL_SYNTH_X, y: dimMidY - EXPERT_H / 2, w: EXPERT_W, h: EXPERT_H })

  /* 裁决列：信号、漏选、风控、账户（账户最大） */
  const verdictX = COL_VERDICT_X
  place('post:desk', { x: verdictX, y: DIM_BLOCK_Y, w: VERDICT_W, h: VERDICT_H })
  place('post:miss', { x: verdictX, y: DIM_BLOCK_Y + VERDICT_H + 14, w: VERDICT_W, h: EXPERT_H })
  place('post:risk', { x: verdictX, y: DIM_BLOCK_Y + VERDICT_H + 14 + EXPERT_H + 14, w: VERDICT_W, h: VERDICT_H })
  const accountY = DIM_BLOCK_Y + VERDICT_H + 14 + EXPERT_H + 14 + VERDICT_H + 18
  place('book:center', { x: verdictX - 12, y: accountY, w: ACCOUNT_W, h: ACCOUNT_H })

  /* 时段带焦点牌：加宽并向左生长。必须在边计算前完成，曲线要跟新矩形走 */
  if (options?.promote && rectOf.has(options.promote)) {
    const rect = rectOf.get(options.promote)!
    const widened: StageRect = { ...rect, x: rect.x - (PROMOTE_W - rect.w), w: PROMOTE_W }
    rectOf.set(options.promote, widened)
    const index = nodes.findIndex((item) => item.id === options.promote)
    if (index >= 0) nodes[index] = { ...nodes[index], rect: widened, promoted: true }
  }

  /* 列标题 */
  const columns: StageColumn[] = [
    { title: '01 接入', x: COL_SOURCE_X },
    { title: '02 研判', x: COL_SCAN_X },
    { title: '03 裁决', x: COL_VERDICT_X - 12 },
  ]

  /* 边：场景边（两端都在台上）+ 模型补边 */
  const raw: { id: string; from: string; to: string }[] = []
  for (const edge of graph.edges) {
    if (onStage(edge.from) && onStage(edge.to)) raw.push({ id: edge.id, from: edge.from, to: edge.to })
  }
  for (const [from, to] of MODEL_LINKS) {
    if (rectOf.has(from) && rectOf.has(to)) raw.push({ id: `${from}>${to}`, from, to })
  }

  /* S3：漏斗光带在台上时，扫描→四维→综合→信号的旧束线降为兜底（§4.4.3 束线只在光带缺失时兜底） */
  const funnel = graph.funnel
    ? buildFunnelLayout(graph.funnel, rectOf, DIM_BLOCK_Y + dimBlockH + FUNNEL_BAND_OFFSET)
    : null

  /* 同一出口的线扇出：按 from / to 分组编号，端口沿边错开 */
  const outGroups = new Map<string, string[]>()
  const inGroups = new Map<string, string[]>()
  for (const edge of raw) {
    outGroups.set(edge.from, [...(outGroups.get(edge.from) ?? []), edge.id])
    inGroups.set(edge.to, [...(inGroups.get(edge.to) ?? []), edge.id])
  }
  const edges: StageEdgeLayout[] = []
  for (const edge of raw) {
    const from = rectOf.get(edge.from)
    const to = rectOf.get(edge.to)
    if (!from || !to) continue
    const outList = outGroups.get(edge.from) ?? [edge.id]
    const inList = inGroups.get(edge.to) ?? [edge.id]
    const outIndex = outList.indexOf(edge.id)
    const inIndex = inList.indexOf(edge.id)
    edges.push({
      ...edge,
      domain: domainOf(edge.from),
      outIndex,
      outCount: outList.length,
      inIndex,
      inCount: inList.length,
      curves: fiberCurves(from, to, outIndex, outList.length, inIndex, inList.length, edge.id),
      thick: isThick(edge.id, edge.to, options?.thicken),
      fallback: funnel != null && isTrunkEdge(edge.from, edge.to),
    })
  }

  return { nodes, edges, columns, bounds: stageBounds(nodes, funnel), funnel }
}

/** 主干束线判定：被漏斗光带取代的三段（扫描→四维、四维→综合、综合→信号） */
export function isTrunkEdge(from: string, to: string): boolean {
  if (from === 'post:scan' && to.startsWith('dim:')) return true
  if (from.startsWith('dim:') && to === 'dim:synth') return true
  return from === 'dim:synth' && to === 'post:desk'
}

/* ---------------- S3 漏斗光带布局 ---------------- */

/**
 * 六层光带几何（方案 §4.4.2）：横贯 02/03 之间，段厚 = sqrt 映射（12..96px），
 * 垂直居中对齐，形成向右收窄的阶梯剪影。缺失层画 FUNNEL_MISSING_H 厚的虚线段，不 0 填充不跳过。
 * 侧漏从交界处下折，经主干右端外侧的通道绕行汇入「漏选」牌右缘（不穿裁决列的牌）。
 */
export function buildFunnelLayout(
  funnel: FunnelModel,
  rectOf: ReadonlyMap<string, StageRect>,
  bandTop: number,
): StageFunnelLayout {
  const counts = funnel.layers.map((layer) => layer.count)
  const widths = funnelWidths(counts)
  const maxH = Math.max(FUNNEL_MIN_W, ...widths.map((width) => width ?? 0))
  const centerY = bandTop + FUNNEL_BAND_OFFSET / 2 + maxH / 2

  const step = (FUNNEL_X1 - FUNNEL_X0 + FUNNEL_GAP) / funnel.layers.length
  const segments: StageFunnelSegment[] = funnel.layers.map((layer, index) => {
    const x = FUNNEL_X0 + index * step
    const h = layer.count == null ? FUNNEL_MISSING_H : Math.max(FUNNEL_MIN_W, widths[index] ?? FUNNEL_MIN_W)
    return {
      key: layer.key,
      name: layer.name,
      count: layer.count,
      rect: { x, y: centerY - h / 2, w: Math.max(8, step - FUNNEL_GAP), h },
      rate: funnelRate(layer.count, index > 0 ? counts[index - 1] : null) ?? '',
      missing: layer.count == null,
    }
  })
  const band: StageRect = { x: FUNNEL_X0, y: bandTop, w: FUNNEL_X1 - FUNNEL_X0, h: maxH + FUNNEL_LABEL_DROP }

  /* 侧漏：junction = 段 at 的左缘（层 at-1 → 层 at 的交界），从主干下缘折到各自的收集横线（逐条错开 8px 不叠线） */
  const leakBaseY = centerY + maxH / 2 + FUNNEL_LEAK_DROP
  const collectorX = FUNNEL_X1 + FUNNEL_COLLECTOR_DX
  const leaks: StageFunnelLeak[] = []
  for (const leak of funnel.leaks) {
    if (leak.count == null || leak.count <= 0 || !leak.label) continue
    const next = segments[leak.at]
    if (!next) continue
    const leakY = leakBaseY + leaks.length * 8
    const x = next.rect.x
    const startY = next.missing ? centerY : next.rect.y + next.rect.h
    leaks.push({
      stub: [
        { x, y: startY },
        { x, y: leakY },
        { x: collectorX, y: leakY },
      ],
      label: leak.label,
      count: leak.count,
    })
  }

  /* 收集线：竖线沿裁决列左侧通道上行，从「漏选」牌左缘入牌；牌不在台上或没有侧漏时为 null。
   * 注意用的是基础矩形——拖动漏选牌后收集线不跟随（S3 范围外，拖动本就少用）。 */
  let collector: StagePoint[] | null = null
  if (leaks.length > 0) {
    const lastY = leaks[leaks.length - 1].stub[1].y
    const miss = rectOf.get('post:miss')
    collector = miss
      ? [
          { x: collectorX, y: lastY },
          { x: collectorX, y: miss.y + miss.h / 2 },
          { x: miss.x, y: miss.y + miss.h / 2 },
        ]
      : [{ x: collectorX, y: lastY }]
  }

  const leakBottom = leaks.length > 0 ? leaks[leaks.length - 1].stub[1].y : band.y + band.h
  return {
    segments,
    leaks,
    collector,
    band,
    note: funnel.note,
    hit: {
      x: band.x - 8,
      y: band.y - 12,
      w: band.w + FUNNEL_COLLECTOR_DX + 24,
      h: Math.max(band.h + 12, leakBottom - band.y + 16),
    },
    selectId: 'post:scan',
  }
}

/** 时段带加粗判定：命中「进入某节点」或精确边 id */
function isThick(edgeId: string, to: string, thicken?: StageThicken): boolean {
  if (!thicken) return false
  if (thicken.into?.includes(to)) return true
  return thicken.edges?.includes(edgeId) ?? false
}

/** 用户拖动叠加在基础布局之上 */
export function applyOffsets(nodes: StageNodeLayout[], offsets: ReadonlyMap<string, StagePoint>): StageNodeLayout[] {
  if (offsets.size === 0) return nodes
  return nodes.map((item) => {
    const delta = offsets.get(item.id)
    if (!delta) return item
    return { ...item, rect: { ...item.rect, x: item.rect.x + delta.x, y: item.rect.y + delta.y } }
  })
}

/** 拖动后按当前节点矩形、原扇出序号重算一条边的束线 */
export function reCurve(edge: StageEdgeLayout, rects: ReadonlyMap<string, StageRect>): CubicCurve[] {
  const from = rects.get(edge.from)
  const to = rects.get(edge.to)
  if (!from || !to) return edge.curves
  return fiberCurves(from, to, edge.outIndex, edge.outCount, edge.inIndex, edge.inCount, edge.id)
}

function stageBounds(nodes: StageNodeLayout[], funnel?: StageFunnelLayout | null): StageRect {
  if (nodes.length === 0) return { x: 0, y: 0, w: 1280, h: 560 }
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  for (const item of nodes) {
    minX = Math.min(minX, item.rect.x)
    minY = Math.min(minY, item.rect.y)
    maxX = Math.max(maxX, item.rect.x + item.rect.w)
    maxY = Math.max(maxY, item.rect.y + item.rect.h)
  }
  /* S3：光带与侧漏收集线纳入取景，避免被裁出画面 */
  if (funnel) {
    minX = Math.min(minX, funnel.band.x)
    maxX = Math.max(maxX, funnel.band.x + funnel.band.w + FUNNEL_COLLECTOR_DX)
    maxY = Math.max(maxY, funnel.band.y + funnel.band.h + FUNNEL_LEAK_DROP)
  }
  return { x: minX - 32, y: minY - 56, w: maxX - minX + 64, h: maxY - minY + 96 }
}

/* ---------------- 纤维曲线 ---------------- */

function fiberCurves(
  from: StageRect,
  to: StageRect,
  outIndex: number,
  outCount: number,
  inIndex: number,
  inCount: number,
  seed: string,
): CubicCurve[] {
  const outSpread = fanOffset(outIndex, outCount, from.h)
  const inSpread = fanOffset(inIndex, inCount, to.h)
  const vertical = to.x < from.x + from.w * 0.5
  const strands = 2
  const curves: CubicCurve[] = []
  for (let strand = 0; strand < strands; strand += 1) {
    const jitter = (hash01(`${seed}:${strand}`) - 0.5) * 5
    const lift = (strand - (strands - 1) / 2) * 3
    if (vertical) {
      /* 模型在专家上方：从牌底出、牌顶进 */
      const x0 = from.x + from.w / 2 + jitter * 2
      const y0 = from.y + from.h
      const x1 = to.x + to.w / 2 + jitter * 2
      const y1 = to.y
      const bend = Math.max(24, Math.abs(y1 - y0) * 0.45)
      curves.push([x0, y0, x0 + lift, y0 + bend, x1 + lift, y1 - bend, x1, y1])
    } else {
      const x0 = from.x + from.w
      const y0 = from.y + from.h / 2 + outSpread + lift
      const x1 = to.x
      const y1 = to.y + to.h / 2 + inSpread + lift
      const bend = Math.max(36, (x1 - x0) * 0.45)
      curves.push([x0, y0, x0 + bend, y0 + jitter, x1 - bend, y1 - jitter, x1, y1])
    }
  }
  return curves
}

function fanOffset(index: number, count: number, span: number): number {
  if (count <= 1) return 0
  const step = Math.min(9, span / (count + 1))
  return (index - (count - 1) / 2) * step
}

export function cubicPoint(curve: CubicCurve, t: number): StagePoint {
  const u = 1 - t
  return {
    x: u * u * u * curve[0] + 3 * u * u * t * curve[2] + 3 * u * t * t * curve[4] + t * t * t * curve[6],
    y: u * u * u * curve[1] + 3 * u * u * t * curve[3] + 3 * u * t * t * curve[5] + t * t * t * curve[7],
  }
}

/* ---------------- 视口与命中 ---------------- */

export function fitTransform(bounds: StageRect, viewportW: number, viewportH: number): ViewTransform {
  if (viewportW < 40 || viewportH < 40) return { x: 0, y: 0, scale: 1 }
  const scale = clamp(Math.min(viewportW / bounds.w, viewportH / bounds.h), 0.3, 1.15)
  return {
    scale,
    x: (viewportW - bounds.w * scale) / 2 - bounds.x * scale,
    y: (viewportH - bounds.h * scale) / 2 - bounds.y * scale,
  }
}

export function worldToScreen(view: ViewTransform, point: StagePoint): StagePoint {
  return { x: point.x * view.scale + view.x, y: point.y * view.scale + view.y }
}

export function screenToWorld(view: ViewTransform, point: StagePoint): StagePoint {
  return { x: (point.x - view.x) / view.scale, y: (point.y - view.y) / view.scale }
}

export function zoomAt(view: ViewTransform, pivot: StagePoint, next: number): ViewTransform {
  const scale = clamp(next, 0.35, 1.8)
  const world = screenToWorld(view, pivot)
  return { scale, x: pivot.x - world.x * scale, y: pivot.y - world.y * scale }
}

export function hitNode(nodes: StageNodeLayout[], world: StagePoint): string {
  /* 后画的在上层，倒序命中 */
  for (let index = nodes.length - 1; index >= 0; index -= 1) {
    const rect = nodes[index].rect
    if (world.x >= rect.x && world.x <= rect.x + rect.w && world.y >= rect.y && world.y <= rect.y + rect.h) {
      return nodes[index].id
    }
  }
  return ''
}

/** 当前（含拖后重算）束线的最近命中；调用方传 6 / scale 保持约 6px 屏幕容差。 */
export function hitEdge(curves: ReadonlyMap<string, readonly CubicCurve[]>, world: StagePoint, tolerance = 6): string {
  if (!(tolerance > 0) || !Number.isFinite(tolerance)) return ''
  let nearest = tolerance * tolerance
  let hit = ''
  for (const [id, strands] of curves) {
    for (const curve of strands) {
      const length =
        Math.hypot(curve[2] - curve[0], curve[3] - curve[1]) +
        Math.hypot(curve[4] - curve[2], curve[5] - curve[3]) +
        Math.hypot(curve[6] - curve[4], curve[7] - curve[5])
      const samples = Math.min(256, Math.max(24, Math.ceil(length / 8)))
      let previous = cubicPoint(curve, 0)
      for (let index = 1; index <= samples; index += 1) {
        const next = cubicPoint(curve, index / samples)
        const dx = next.x - previous.x
        const dy = next.y - previous.y
        const squaredLength = dx * dx + dy * dy
        const t = squaredLength
          ? clamp(((world.x - previous.x) * dx + (world.y - previous.y) * dy) / squaredLength, 0, 1)
          : 0
        const distance = (world.x - previous.x - t * dx) ** 2 + (world.y - previous.y - t * dy) ** 2
        if (distance <= nearest) {
          nearest = distance
          hit = id
        }
        previous = next
      }
    }
  }
  return hit
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

/* ---------------- 脉冲端点 ---------------- */

/**
 * 把 WS 脉冲映射到台上的两个节点 id。
 * 服务节点不上台：service:brain 归到大模型，service:market 归到扫描的入口侧。
 * 返回 null 表示这帧在台上没有对应路径，不画。
 */
export function stagePulse(pulse: Pulse): { from: string; to: string } | null {
  const from = stageEnd(pulse.from)
  const to = stageEnd(pulse.to)
  if (from === 'service:brain') return { from: 'model:large', to: to === 'dim:synth' ? 'dim:synth' : to }
  if (from === 'service:market') return { from: '', to: 'post:scan' }
  if (!onStage(from) || !onStage(to)) return null
  return { from, to }
}

function stageEnd(id: string): string {
  if (id === 'post:model') return 'dim:synth'
  if (id === 'post:news') return 'dim:news'
  if (id === 'post:capital') return 'dim:capital'
  if (id === 'post:overseas') return 'dim:overseas'
  return id
}

/* ---------------- 主题 ---------------- */

export interface StageTheme {
  bg: string
  card: string
  cardBorder: string
  titleBand: string
  text1: string
  text2: string
  text3: string
  up: string
  down: string
  grid: string
  fiber: string
  fiberActive: string
  status: Record<NodeStatus, string>
  domain: Record<DomainKey, string>
  fontSans: string
  fontMono: string
}

/** 从 CSS 变量读色板。主题切换时重读。 */
export function readStageTheme(root?: HTMLElement): StageTheme {
  const element = root ?? (typeof document === 'undefined' ? undefined : document.documentElement)
  const style = element ? getComputedStyle(element) : null
  const v = (name: string, fallback: string) => style?.getPropertyValue(name).trim() || fallback
  const accent = v('--nt-accent', PALETTE.accent)
  const accent2 = v('--nt-accent-2', PALETTE.accent2)
  return {
    bg: v('--nt-bg-0', PALETTE.bg0),
    card: v('--nt-bg-1', PALETTE.bg1),
    cardBorder: v('--nt-border-2', PALETTE.border2),
    titleBand: v('--nt-bg-2', PALETTE.bg2),
    text1: v('--nt-text-1', PALETTE.text1),
    text2: v('--nt-text-2', PALETTE.text2),
    text3: v('--nt-text-3', PALETTE.text3),
    up: v('--nt-up', PALETTE.up),
    down: v('--nt-down', PALETTE.down),
    grid: v('--nt-border-1', PALETTE.border1),
    fiber: v('--nt-info', PALETTE.info),
    fiberActive: accent,
    status: {
      live: accent,
      idle: v('--nt-text-3', PALETTE.text3),
      gap: v('--nt-warn', PALETTE.warn),
      down: v('--nt-danger', PALETTE.danger),
      attention: accent2,
    },
    domain: {
      source: v('--nt-info', PALETTE.info),
      scan: accent,
      tech: accent,
      news: accent2,
      capital: v('--nt-warn', PALETTE.warn),
      overseas: v('--nt-success', PALETTE.success),
      synth: accent,
      model: accent2,
      signal: accent,
      miss: v('--nt-warn', PALETTE.warn),
      risk: v('--nt-danger', PALETTE.danger),
      account: v('--nt-text-2', PALETTE.text2),
      runtime: v('--nt-text-3', PALETTE.text3),
    },
    fontSans: v('--nt-font-sans', "'Segoe UI', system-ui, sans-serif"),
    fontMono: v('--nt-font-mono', 'Consolas, monospace'),
  }
}

/** 给十六进制或 rgb 色加透明度，输出 rgba 字符串 */
export function withAlpha(color: string, alpha: number): string {
  const hex = /^#([0-9a-f]{6})$/i.exec(color.trim())
  if (hex) {
    const value = parseInt(hex[1], 16)
    return `rgba(${(value >> 16) & 255}, ${(value >> 8) & 255}, ${value & 255}, ${alpha})`
  }
  const rgb = /^rgba?\(([^)]+)\)$/.exec(color.trim())
  if (rgb) {
    const parts = rgb[1].split(',').map((item) => item.trim())
    return `rgba(${parts[0]}, ${parts[1]}, ${parts[2]}, ${alpha})`
  }
  return color
}

/** 稳定 hash 到 [0,1)，用于纤维抖动与相位 */
export function hash01(text: string): number {
  let hash = 2166136261
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return ((hash >>> 0) % 10000) / 10000
}

/**
 * 销毁画布背板（对照 lwc `canvas-utils.ts:28-36` 的 releaseCanvas）：宽高缩 1×1，
 * GPU 位图立刻让位不等 GC；元素自身随组件卸载被回收。
 * 收 null 兜底：画布初始化失败（getContext 为空）时 ref 可能还没接上。
 */
export function releaseCanvas(cv: { width: number; height: number } | null | undefined) {
  if (!cv) return
  cv.width = 1
  cv.height = 1
}
