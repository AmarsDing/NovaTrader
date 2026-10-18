import { describe, expect, it } from 'vitest'
import { buildEdgeDetail, EDGE_PULSE_LIMIT, edgeOf, retainEdgePulses } from './edge-detail'
import { emptyMonitorInput } from './load-input'
import { buildScene } from './scene-model'
import type { EdgeFlow } from './flow'
import type { MonitorInput, Pulse, SceneGraph } from './types'

const NOW = Date.parse('2026-10-14T10:00:00+08:00')
const observed: EdgeFlow = { rate: 6, stalled: false, circuit: false }

function fixture(): { input: MonitorInput; graph: SceneGraph } {
  const input = emptyMonitorInput()
  input.sources = { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'up', detail: '', lastAt: '', successCount: 12 }] }
  input.brainStats = { ok: true, tiers: [{ tier: 'large', model: 'large-demo', configured: true, circuitOpen: false, calls: 23, failures: 0, p95Ms: 4000 }] }
  return { input, graph: buildScene(input, NOW) }
}

function pulse(id: string, from: string, to: string, at = NOW - 5000): Pulse {
  return { id, from, to, at, identifier: { kind: 'signal', value: `signal-${id}` } }
}

function rowValue(detail: ReturnType<typeof buildEdgeDetail>, key: string): string | undefined {
  return detail?.panel.sections.find((section) => section.key === 'observation')?.rows?.find((row) => row.key === key)?.value
}

describe('edgeOf', () => {
  it('数据源边与画布追加的模型边都能检视，未知关系不会被造出来', () => {
    const { graph } = fixture()
    expect(edgeOf(graph, 'source:tdx>post:scan')).toMatchObject({ from: 'source:tdx', to: 'post:scan' })
    expect(graph.edges.some((edge) => edge.id === 'model:large>dim:synth')).toBe(false)
    expect(edgeOf(graph, 'model:large>dim:synth')).toMatchObject({ from: 'model:large', to: 'dim:synth' })
    expect(edgeOf(graph, 'model:small>dim:news')).toBeNull()
    expect(edgeOf({ ...graph, nodes: graph.nodes.filter((node) => node.id !== 'model:large') }, 'model:large>dim:synth')).toBeNull()
  })
})

describe('buildEdgeDetail', () => {
  it('源成功请求是参考速率，并非该边传输记录数', () => {
    const { input, graph } = fixture()
    const detail = buildEdgeDetail(graph, input, 'source:tdx>post:scan', observed, [], NOW)
    expect(detail?.title).toBe('通达信 → 扫描')
    expect(rowValue(detail, 'unit')).toBe('成功请求 / 分钟')
    expect(rowValue(detail, 'rate')).toBe('6.00 成功请求 / 分钟')
    expect(rowValue(detail, 'cumulative')).toBe('12')
    expect(rowValue(detail, 'coverage')).toContain('不能归因到某个消费者')
    expect(rowValue(detail, 'window')).toContain('至少跨 20 秒')
    expect(detail?.panel.sections.find((section) => section.key === 'gaps')?.rows?.every((row) => ['未接入', '未观测'].includes(row.value))).toBe(true)
  })

  it('模型档调用与专家独立吞吐分开，档位P95不会冒充通道P95', () => {
    const { input, graph } = fixture()
    const detail = buildEdgeDetail(graph, input, 'model:large>dim:synth', { ...observed, circuit: true }, [], NOW)
    expect(rowValue(detail, 'unit')).toBe('模型档调用 / 分钟')
    expect(rowValue(detail, 'cumulative')).toBe('23')
    expect(rowValue(detail, 'coverage')).toContain('没有按专家拆分')
    expect(rowValue(detail, 'state')).toContain('熔断')
    expect(detail?.panel.sections.find((section) => section.key === 'gaps')?.rows?.find((row) => row.key === 'latency')?.value).toBe('未观测')
  })

  it('风控到账户仅统计页面告警脉冲，零观测和窗口不足分别表达', () => {
    const { input, graph } = fixture()
    const waiting = buildEdgeDetail(graph, input, 'post:risk>book:center', { ...observed, rate: null }, [], NOW)
    expect(rowValue(waiting, 'rate')).toContain('未观测')
    const zero = buildEdgeDetail(graph, input, 'post:risk>book:center', { ...observed, rate: 0 }, [], NOW)
    expect(rowValue(zero, 'rate')).toBe('0.00 告警脉冲 / 分钟')
    expect(rowValue(zero, 'coverage')).toContain('不代表风控检查、订单或成交次数')
  })

  it('没有计数的边即使收到外部零值也不能伪造零吞吐', () => {
    const { input, graph } = fixture()
    const detail = buildEdgeDetail(graph, input, 'dim:synth>post:desk', { ...observed, rate: 0 }, [], NOW)
    expect(rowValue(detail, 'rate')).toContain('未观测')
    expect(rowValue(detail, 'unit')).toBe('未观测')
    expect(buildEdgeDetail(graph, input, 'missing>unknown', observed, [], NOW)).toBeNull()
  })

  it('本次模型或数据源统计失败时，不能把旧窗口速率冒充当前可观测', () => {
    const { input, graph } = fixture()
    input.brainStats = null
    input.sources = null
    expect(rowValue(buildEdgeDetail(graph, input, 'model:large>dim:synth', observed, [], NOW), 'rate')).toContain('未观测')
    expect(rowValue(buildEdgeDetail(graph, input, 'source:tdx>post:scan', observed, [], NOW), 'rate')).toContain('未观测')
  })

  it('2.4秒动画过期后仍可看到该边真实推送标识；无关推送不混入', () => {
    const { input, graph } = fixture()
    const history = [
      pulse('matched', 'post:model', 'post:desk'),
      pulse('unrelated', 'post:risk', 'book:center'),
      pulse('expired', 'post:model', 'post:desk', NOW - 61_000),
    ]
    const detail = buildEdgeDetail(graph, input, 'dim:synth>post:desk', undefined, history, NOW)
    const section = detail?.panel.sections.find((item) => item.key === 'pulses')
    expect(section?.table?.rows).toHaveLength(1)
    expect(section?.table?.rows[0][1]).toBe('signal:signal-matched')
    expect(section?.table?.rows[0][2]).toBe('事件未知 / 链路未知')
    expect(section?.table?.rows[0][3]).toContain('源时间未知 / post:model → post:desk')
    expect(section?.foot).toContain('页面接收时刻与信封源时间分列')
    expect(section?.foot).toContain('不能仅凭此证明消费确认')
  })

  it('上下游快照标识符有方向与来源，不冒充同一次传输的payload', () => {
    const { input, graph } = fixture()
    graph.nodes.find((node) => node.id === 'source:tdx')!.outputs = [{ kind: 'symbol', value: '600519.SH' }]
    graph.nodes.find((node) => node.id === 'post:scan')!.inputs = [{ kind: 'F', value: 'ma20' }]
    const detail = buildEdgeDetail(graph, input, 'source:tdx>post:scan', undefined, [], NOW)
    const identifiers = detail?.panel.sections.find((section) => section.key === 'identifiers')
    expect(identifiers?.table?.rows).toContainEqual(['通达信', '输出', 'symbol:600519.SH'])
    expect(identifiers?.table?.rows).toContainEqual(['扫描', '输入', 'F:ma20'])
    expect(identifiers?.foot).toContain('不是已关联到同一次传输')
  })
})

describe('retainEdgePulses', () => {
  it('会话脉冲去重、限时、限量，不保留未来/无效时间，也不修改原输入', () => {
    const one = pulse('one', 'post:model', 'post:desk')
    const current = [one]
    const recent = retainEdgePulses(current, [one, pulse('old', '', '', NOW - 61_000), pulse('future', '', '', NOW + 1), pulse('invalid', '', '', NaN)], NOW)
    expect(recent).toEqual([one])
    expect(current).toEqual([one])
    const many = Array.from({ length: 100 }, (_, index) => pulse(String(index), '', '', NOW - index))
    expect(retainEdgePulses([], many, NOW)).toHaveLength(EDGE_PULSE_LIMIT)
    expect(retainEdgePulses([], many, NOW)[0].id).toBe('0')
  })
})
