import { describe, expect, it } from 'vitest'
import { buildChildren, buildScene, pushPulse, type MonitorInput, type Pulse } from './scene-model'

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

function pulse(id: string, at: number): Pulse {
  return { id, from: 'post:model', to: 'post:desk', identifier: { kind: 'signal', value: id }, at }
}

describe('buildScene', () => {
  it('全空输入给出缺口节点且 detail 非空', () => {
    const graph = buildScene(emptyInput(), 1_000_000)
    const posts = graph.nodes.filter((node) => node.id.startsWith('post:'))
    const sources = graph.nodes.filter((node) => node.id.startsWith('source:'))
    const services = graph.nodes.filter((node) => node.id.startsWith('service:'))
    expect(posts.map((node) => node.id)).toEqual([
      'post:scan',
      'post:model',
      'post:news',
      'post:capital',
      'post:overseas',
      'post:risk',
      'post:desk',
      'post:miss',
    ])
    expect(sources).toHaveLength(1)
    expect(sources[0]).toMatchObject({ id: 'source:none', face: '未返回' })
    expect(services).toHaveLength(0)
    expect(graph.nodes.find((node) => node.id === 'book:center')).toMatchObject({ rank: 'focus', face: '账户未返回' })
    expect(graph.nodes.find((node) => node.id === 'model:large')).toMatchObject({ face: '统计未返回' })
    expect(graph.nodes.find((node) => node.id === 'model:token')).toMatchObject({ face: '令牌未接入' })
    expect(graph.nodes.find((node) => node.id === 'dim:synth')).toMatchObject({ face: '未调用' })
    expect(graph.nodes.find((node) => node.id === 'dim:tech')).toMatchObject({ face: '未调用', sub: '信号未返回' })
    expect(graph.nodes.every((node) => node.detail.length > 0)).toBe(true)
    expect(graph.truncated).toBe(false)
    expect(graph.news).toEqual([])
    expect(graph.terminals.every((term) => term.empty.length > 0)).toBe(true)
  })

  it('通达信连到行情服务，行情服务连到扫描岗', () => {
    const graph = buildScene(
      {
        ...emptyInput(),
        ops: { ok: true, services: [{ name: 'market', status: 'ok', detail: '' }], sources: [] },
        sources: {
          ok: true,
          items: [{ id: 'tdx', name: 'tdx', status: 'up', detail: '', lastAt: '' }],
        },
      },
      1_000_000,
    )
    expect(graph.edges).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ from: 'source:tdx', to: 'post:scan' }),
        expect.objectContaining({ from: 'source:tdx', to: 'dim:tech' }),
      ]),
    )
    expect(graph.edges.some((edge) => edge.from.startsWith('source:') && edge.to.startsWith('source:'))).toBe(false)
  })

  it('降级信号的模型说明以规则分开头', () => {
    const graph = buildScene(
      {
        ...emptyInput(),
        signals: {
          ok: true,
          items: [
            {
              id: 's1',
              symbol: '600000',
              name: '浦发',
              score: 70,
              highValue: false,
              degraded: true,
              status: 'done',
              evidence: [],
              dims: {},
              entry: 10,
              stop: 9,
              take: 12,
              time: '',
            },
          ],
        },
      },
      1_000_000,
    )
    const model = graph.nodes.find((node) => node.id === 'post:model')
    expect(model?.detail.startsWith('规则分')).toBe(true)
    expect(model?.detail).toContain('600000')
    expect(model?.detail).toContain('70')
  })

  it('待确认信号把主管标成 attention', () => {
    const graph = buildScene(
      {
        ...emptyInput(),
        signals: {
          ok: true,
          items: [
            {
              id: 's2',
              symbol: '000001',
              name: '平安',
              score: 80,
              highValue: false,
              degraded: false,
              status: 'approved',
              evidence: [],
              dims: {},
              entry: null,
              stop: null,
              take: null,
              time: '',
            },
          ],
        },
      },
      1_000_000,
    )
    const desk = graph.nodes.find((node) => node.id === 'post:desk')
    expect(desk?.status).toBe('attention')
    expect(desk?.detail).toContain('待确认 1 笔')
  })

  it('证据编号进模型输入', () => {
    const graph = buildScene(
      {
        ...emptyInput(),
        signals: {
          ok: true,
          items: [
            {
              id: 's3',
              symbol: '600001',
              name: '邯郸',
              score: 60,
              highValue: false,
              degraded: false,
              status: 'done',
              evidence: ['F:ma20', 'N:99'],
              dims: {},
              entry: null,
              stop: null,
              take: null,
              time: '',
            },
          ],
        },
      },
      1_000_000,
    )
    const model = graph.nodes.find((node) => node.id === 'post:model')
    expect(model?.inputs).toEqual(
      expect.arrayContaining([
        { kind: 'F', value: 'ma20' },
        { kind: 'N', value: '99' },
      ]),
    )
  })

  it('外围全空时标未调用', () => {
    const graph = buildScene(
      {
        ...emptyInput(),
        sectors: {
          ok: true,
          rows: [{ name: '半导体', avgPct: 1.2, overseas: null, leader: '600000' }],
        },
      },
      1_000_000,
    )
    const overseas = graph.nodes.find((node) => node.id === 'post:overseas')
    expect(overseas?.status).toBe('gap')
    expect(overseas?.detail).toBe('外围未调用')
  })

  it('权益窗用红涨绿跌的 tone，缺数字不把整窗当失败', () => {
    const down = buildScene(
      {
        ...emptyInput(),
        account: {
          ok: true,
          book: 'SIM',
          equity: 100,
          cash: 40,
          dayPnl: -1,
          positions: [],
        },
      },
      1_000_000,
    )
    const downRow = down.terminals.find((term) => term.id === 'term:equity')?.rows.find((row) => row.label === '当日')
    expect(downRow?.tone).toBe('down')
    expect(down.terminals.find((term) => term.id === 'term:equity')?.empty).toBe('')

    const up = buildScene(
      {
        ...emptyInput(),
        account: { ok: true, book: 'SIM', equity: 100, cash: 40, dayPnl: 2, positions: [] },
      },
      1_000_000,
    )
    expect(up.terminals.find((term) => term.id === 'term:equity')?.rows.find((row) => row.label === '当日')?.tone).toBe('up')

    const flat = buildScene(
      {
        ...emptyInput(),
        account: { ok: true, book: 'SIM', equity: null, cash: 0, dayPnl: null, positions: [] },
      },
      1_000_000,
    )
    const equity = flat.terminals.find((term) => term.id === 'term:equity')
    const day = equity?.rows.find((row) => row.label === '当日')
    const book = equity?.rows.find((row) => row.label === '权益')
    expect(day?.tone).toBe('flat')
    expect(day?.value).toBe('—')
    expect(book?.value).toBe('—')
    expect(equity?.empty).toBe('')
  })
})

describe('pushPulse', () => {
  it('丢掉过期脉冲并保持最多 24 条', () => {
    const now = 10_000
    const fresh = Array.from({ length: 24 }, (_, index) => pulse(`p${index}`, now - index))
    const capped = pushPulse(fresh, pulse('new', now), now)
    expect(capped).toHaveLength(24)
    expect(capped.some((item) => item.id === 'new')).toBe(true)
    expect(capped.some((item) => item.id === 'p0')).toBe(false)

    const dropped = pushPulse([pulse('old', now - 2401), pulse('keep', now - 10)], pulse('newer', now), now)
    expect(dropped.map((item) => item.id)).toEqual(['keep', 'newer'])
  })
})

describe('牌面', () => {
  it('风控详情和终端的授权与账户分开，L3 不冒充自动实盘', () => {
    const input = emptyInput()
    input.account = { ok: true, book: 'LIVE', equity: 100, cash: 100, dayPnl: 0, positions: [] }
    input.risk = { ok: true, mode: 'L3', killActive: false, killReason: '', buysToday: 0, marketBreaker: '' }
    const cards = buildChildren(input, 'post:risk')
    expect(cards.find((card) => card.id === 'risk:mode')).toMatchObject({ title: '运行授权', face: expect.stringContaining('未开放') })
    expect(cards.find((card) => card.id === 'risk:book')).toMatchObject({ title: '账户账本', face: 'LIVE' })
    const scene = buildScene(input, input.now)
    expect(scene.nodes.find((node) => node.id === 'post:risk')?.detail).toContain('运行授权 L3 未开放')
    expect(scene.terminals.find((term) => term.id === 'term:risk')?.rows.find((row) => row.label === '账户账本')?.value).toBe('LIVE')
  })
  it('单个宕掉的服务只把名字升到醒目牌', () => {
    const graph = buildScene(
      {
        ...emptyInput(),
        ops: { ok: true, services: [{ name: 'nats', status: 'down', detail: '未连接' }], sources: [] },
      },
      1_000_000,
    )
    expect(graph.nodes.find((node) => node.id === 'service:nats')).toMatchObject({ rank: 'brief', face: 'nats', sub: '' })
  })

  it('点账户牌展开持仓，而不是底栏表格', () => {
    const cards = buildChildren(
      {
        ...emptyInput(),
        account: {
          ok: true,
          book: 'SIM',
          equity: 10,
          cash: 4,
          dayPnl: 1,
          positions: [{ symbol: '600000', quantity: 100, avgCost: 8, lastPrice: 9 }],
        },
      },
      'book:center',
    )
    expect(cards[0]).toMatchObject({ id: 'symbol:600000', tone: 'up' })
  })
})
