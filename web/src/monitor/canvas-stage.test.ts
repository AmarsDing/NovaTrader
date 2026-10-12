import { describe, expect, it } from 'vitest'
import {
  applyOffsets,
  buildStage,
  cubicPoint,
  fitTransform,
  hash01,
  hitNode,
  reCurve,
  screenToWorld,
  stagePulse,
  worldToScreen,
  zoomAt,
  type ViewTransform,
} from './canvas-stage'
import { buildScene, type MonitorInput } from './scene-model'
import type { Pulse, SceneGraph } from './types'

function emptyInput(): MonitorInput {
  return {
    now: 1_000_000,
    focusSymbol: '',
    account: null,
    risk: null,
    ops: null,
    sources: null,
    sentiment: null,
    sectors: null,
    limits: null,
    signals: null,
    briefing: null,
    hotWords: null,
    timeline: null,
    brainStats: null,
    equityCurve: null,
    pulses: [],
  }
}

function demoGraph(): SceneGraph {
  return buildScene(
    {
      ...emptyInput(),
      sources: {
        ok: true,
        items: [
          { id: 'tdx', name: '通达信', status: 'open', detail: '', lastAt: '' },
          { id: 'rss', name: '新闻快讯', status: 'open', detail: '', lastAt: '' },
        ],
      },
      brainStats: {
        ok: true,
        tiers: [
          { tier: 'large', model: 'qwen', configured: true, circuitOpen: false, calls: 8, failures: 0, p50Ms: 12, p95Ms: 30, inFlight: 1, queued: 0, kvCacheUsage: null },
          { tier: 'small', model: 'mini', configured: true, circuitOpen: false, calls: 3, failures: 0, p50Ms: 4, p95Ms: 9, inFlight: 0, queued: 0, kvCacheUsage: null },
        ],
      },
      account: { ok: true, book: 'SIM', equity: 100, cash: 40, dayPnl: 2, positions: [] },
      risk: { ok: true, mode: 'LIVE', killActive: false, killReason: '', buysToday: 1, marketBreaker: '' },
    },
    1_000_000,
  )
}

describe('buildStage', () => {
  it('列从左到右：数据源 < 扫描 < 四维 < 综合 < 裁决', () => {
    const stage = buildStage(demoGraph())
    const x = (id: string) => stage.nodes.find((item) => item.id === id)?.rect.x ?? -1
    expect(x('source:tdx')).toBeGreaterThanOrEqual(0)
    expect(x('source:tdx')).toBeLessThan(x('post:scan'))
    expect(x('post:scan')).toBeLessThan(x('dim:tech'))
    expect(x('dim:tech')).toBeLessThan(x('dim:synth'))
    expect(x('dim:synth')).toBeLessThan(x('post:desk'))
    expect(x('post:desk')).toBe(x('post:risk'))
    expect(stage.columns.map((item) => item.title)).toEqual(['01 接入', '02 研判', '03 裁决'])
  })

  it('promote：被提升的节点加宽到 280 并向左生长，出边曲线跟随新矩形', () => {
    const plain = buildStage(demoGraph())
    const scan = plain.nodes.find((item) => item.id === 'post:scan')!
    const promoted = buildStage(demoGraph(), { promote: 'post:scan' })
    const wide = promoted.nodes.find((item) => item.id === 'post:scan')!
    expect(wide.promoted).toBe(true)
    expect(wide.rect.w).toBe(280)
    expect(wide.rect.x).toBe(scan.rect.x - (280 - scan.rect.w))
    expect(wide.rect.y).toBe(scan.rect.y)
    /* 其余节点不动 */
    expect(promoted.nodes.find((item) => item.id === 'dim:tech')?.rect).toEqual(
      plain.nodes.find((item) => item.id === 'dim:tech')?.rect,
    )
    /* 出边从加宽后的右缘出发：曲线终点 x = 新矩形右边 */
    const edge = promoted.edges.find((item) => item.from === 'post:scan' && item.to === 'dim:tech')!
    for (const curve of edge.curves) expect(curve[0]).toBe(wide.rect.x + wide.rect.w)
  })

  it('promote 指向不存在的节点时布局不变', () => {
    const stage = buildStage(demoGraph(), { promote: 'book:nope' })
    expect(stage.nodes.every((item) => !item.promoted)).toBe(true)
  })

  it('thicken.into：进入该节点的束线全部标粗，其余一条不标', () => {
    const stage = buildStage(demoGraph(), { thicken: { into: ['post:scan'] } })
    const into = stage.edges.filter((edge) => edge.to === 'post:scan')
    expect(into.length).toBeGreaterThan(0)
    expect(into.every((edge) => edge.thick === true)).toBe(true)
    expect(stage.edges.filter((edge) => edge.to !== 'post:scan').every((edge) => edge.thick !== true)).toBe(true)
  })

  it('thicken.edges：只标精确的 from>to 边（尾盘 风控→账户）', () => {
    const stage = buildStage(demoGraph(), { thicken: { edges: ['post:risk>book:center'] } })
    expect(stage.edges.filter((edge) => edge.thick === true).map((edge) => edge.id)).toEqual(['post:risk>book:center'])
  })

  it('不传 thicken 时没有边被标粗（拓扑与常态观感不变）', () => {
    expect(buildStage(demoGraph()).edges.every((edge) => !edge.thick)).toBe(true)
  })

  it('空场景也可布局：骨干节点都在，骨架边保留但没有数据源来线', () => {
    const stage = buildStage(buildScene(emptyInput(), 1_000_000))
    const ids = stage.nodes.map((item) => item.id)
    for (const id of ['post:scan', 'dim:tech', 'dim:news', 'dim:capital', 'dim:overseas', 'dim:synth', 'post:desk', 'post:risk', 'book:center', 'model:large', 'model:small']) {
      expect(ids).toContain(id)
    }
    const pairs = stage.edges.map((edge) => `${edge.from}>${edge.to}`)
    expect(pairs.some((pair) => pair.startsWith('source:'))).toBe(false)
    for (const pair of ['post:scan>dim:tech', 'dim:tech>dim:synth', 'dim:synth>post:desk', 'post:desk>post:risk', 'post:risk>book:center', 'model:large>dim:synth']) {
      expect(pairs).toContain(pair)
    }
    expect(stage.bounds.w).toBeGreaterThan(0)
  })

  it('模型补边连到负责的专家，场景边两端在台上才保留', () => {
    const stage = buildStage(demoGraph())
    const pairs = stage.edges.map((edge) => `${edge.from}>${edge.to}`)
    expect(pairs).toContain('model:small>dim:tech')
    expect(pairs).toContain('model:large>dim:synth')
    expect(pairs).toContain('source:tdx>post:scan')
    expect(pairs).toContain('source:rss>dim:news')
    expect(pairs).toContain('dim:synth>post:desk')
    expect(pairs).toContain('post:risk>book:center')
    expect(stage.edges.every((edge) => edge.curves.length === 2)).toBe(true)
  })

  it('同一布局两次构建结果一致（确定性）', () => {
    const a = buildStage(demoGraph())
    const b = buildStage(demoGraph())
    expect(JSON.stringify(a.edges)).toBe(JSON.stringify(b.edges))
  })

  it('四维并行：扫描分别连四维，四维各自汇综合，不串行', () => {
    const stage = buildStage(demoGraph())
    const pairs = new Set(stage.edges.map((edge) => `${edge.from}>${edge.to}`))
    for (const dim of ['dim:tech', 'dim:news', 'dim:capital', 'dim:overseas']) {
      expect(pairs.has(`post:scan>${dim}`)).toBe(true)
      expect(pairs.has(`${dim}>dim:synth`)).toBe(true)
    }
    expect(pairs.has('dim:tech>dim:news')).toBe(false)
  })
})

describe('拖动与重连', () => {
  it('applyOffsets 平移节点，reCurve 之后曲线端点跟着牌走', () => {
    const stage = buildStage(demoGraph())
    const moved = applyOffsets(stage.nodes, new Map([['post:scan', { x: 40, y: 24 }]]))
    const scan = moved.find((item) => item.id === 'post:scan')
    const base = stage.nodes.find((item) => item.id === 'post:scan')
    expect(scan?.rect.x).toBe((base?.rect.x ?? 0) + 40)
    const rects = new Map(moved.map((item) => [item.id, item.rect]))
    const edge = stage.edges.find((item) => item.from === 'post:scan')
    expect(edge).toBeTruthy()
    if (!edge) return
    const curves = reCurve(edge, rects)
    expect(curves[0][0]).toBe((scan?.rect.x ?? 0) + (scan?.rect.w ?? 0))
  })
})

describe('视口变换', () => {
  const bounds = { x: 0, y: 0, w: 1200, h: 480 }

  it('fit 把场景居中且完整可见', () => {
    const view = fitTransform(bounds, 1000, 500)
    expect(view.scale).toBeLessThanOrEqual(1)
    const leftTop = worldToScreen(view, { x: 0, y: 0 })
    const rightBottom = worldToScreen(view, { x: 1200, y: 480 })
    expect(leftTop.x).toBeGreaterThanOrEqual(0)
    expect(leftTop.y).toBeGreaterThanOrEqual(0)
    expect(rightBottom.x).toBeLessThanOrEqual(1000)
    expect(rightBottom.y).toBeLessThanOrEqual(500)
  })

  it('world↔screen 互逆；zoomAt 保持光标下的世界点不动', () => {
    const view: ViewTransform = { x: 12, y: -30, scale: 0.8 }
    const world = { x: 300, y: 220 }
    const screen = worldToScreen(view, world)
    const back = screenToWorld(view, screen)
    expect(back.x).toBeCloseTo(world.x, 6)
    expect(back.y).toBeCloseTo(world.y, 6)
    const zoomed = zoomAt(view, screen, view.scale * 1.25)
    const after = screenToWorld(zoomed, screen)
    expect(after.x).toBeCloseTo(world.x, 6)
    expect(after.y).toBeCloseTo(world.y, 6)
    expect(zoomed.scale).toBeCloseTo(1, 6)
  })

  it('zoom 有上下限', () => {
    const view: ViewTransform = { x: 0, y: 0, scale: 1 }
    expect(zoomAt(view, { x: 0, y: 0 }, 99).scale).toBe(1.8)
    expect(zoomAt(view, { x: 0, y: 0 }, 0.01).scale).toBe(0.35)
  })
})

describe('命中与脉冲', () => {
  it('hitNode 命中牌面，空白处返回空串', () => {
    const stage = buildStage(demoGraph())
    const scan = stage.nodes.find((item) => item.id === 'post:scan')
    expect(scan).toBeTruthy()
    if (!scan) return
    expect(hitNode(stage.nodes, { x: scan.rect.x + 2, y: scan.rect.y + 2 })).toBe('post:scan')
    expect(hitNode(stage.nodes, { x: -500, y: -500 })).toBe('')
  })

  it('脉冲端点映射：服务不上台，brain 归大模型，market 到扫描', () => {
    const brain: Pulse = { id: 'p1', from: 'service:brain', to: 'post:model', identifier: { kind: 'trace', value: 't' }, at: 0 }
    expect(stagePulse(brain)).toEqual({ from: 'model:large', to: 'dim:synth' })
    const market: Pulse = { id: 'p2', from: 'service:market', to: 'post:scan', identifier: { kind: 'symbol', value: '600000' }, at: 0 }
    expect(stagePulse(market)).toEqual({ from: '', to: 'post:scan' })
    const signal: Pulse = { id: 'p3', from: 'post:model', to: 'post:desk', identifier: { kind: 'signal', value: '88' }, at: 0 }
    expect(stagePulse(signal)).toEqual({ from: 'dim:synth', to: 'post:desk' })
    const alert: Pulse = { id: 'p4', from: 'post:risk', to: 'book:center', identifier: { kind: 'rule', value: 'R01' }, at: 0 }
    expect(stagePulse(alert)).toEqual({ from: 'post:risk', to: 'book:center' })
    const unknown: Pulse = { id: 'p5', from: 'service:nats', to: 'service:redis', identifier: { kind: 'trace', value: 'x' }, at: 0 }
    expect(stagePulse(unknown)).toBeNull()
  })

  it('hash01 稳定且在 [0,1)', () => {
    expect(hash01('edge:a>b')).toBe(hash01('edge:a>b'))
    expect(hash01('edge:a>b')).not.toBe(hash01('edge:a>c'))
    expect(hash01('x')).toBeGreaterThanOrEqual(0)
    expect(hash01('x')).toBeLessThan(1)
  })

  it('cubicPoint 端点精确', () => {
    const curve: [number, number, number, number, number, number, number, number] = [0, 0, 10, 0, 20, 10, 30, 10]
    expect(cubicPoint(curve, 0)).toEqual({ x: 0, y: 0 })
    expect(cubicPoint(curve, 1)).toEqual({ x: 30, y: 10 })
  })
})
