import { stagePulse } from './canvas-stage'
import type { DetailPanel, DetailRow } from './detail'
import { FLOW_WINDOW_MS, KILL_EDGE_ID, MODEL_EDGE_KEYS, tierFor, type EdgeFlow } from './flow'
import { sourceNodeId } from './freshness'
import type { MonitorInput, Pulse, SceneEdge, SceneGraph, SceneNode } from './types'
import { observationSections, topicsForNode } from './observability'

/** 本会话仅保留最近一分钟的真实推送标识；与 2.4 秒动画寿命分开。 */
export const EDGE_PULSE_WINDOW_MS = 60_000
export const EDGE_PULSE_LIMIT = 80

export function retainEdgePulses(current: Pulse[], incoming: Pulse[], now: number): Pulse[] {
  const unique = new Map([...current, ...incoming].map((pulse) => [pulse.id, pulse]))
  return [...unique.values()]
    .filter((pulse) => pulse.at >= now - EDGE_PULSE_WINDOW_MS && pulse.at <= now)
    .sort((a, b) => b.at - a.at || a.id.localeCompare(b.id))
    .slice(0, EDGE_PULSE_LIMIT)
}

/** 模型边由画布追加，仅接受既有路由清单及真实存在的端点，不能凭任意 id 造边。 */
export function edgeOf(graph: SceneGraph, id: string): SceneEdge | null {
  const known = graph.edges.find((edge) => edge.id === id)
  if (known) return known
  if (!Object.values(MODEL_EDGE_KEYS).flat().includes(id)) return null
  const [from, to] = id.split('>')
  if (!graph.nodes.some((node) => node.id === from) || !graph.nodes.some((node) => node.id === to)) return null
  return { id, from, to }
}

export interface EdgeInspection {
  edge: SceneEdge
  title: string
  panel: DetailPanel
}

/** 连线是结构依赖；差分速率与视觉映射脉冲均不证明逐记录的因果传输。 */
export function buildEdgeDetail(
  graph: SceneGraph,
  input: MonitorInput,
  id: string,
  flow: EdgeFlow | undefined,
  history: Pulse[],
  now: number,
): EdgeInspection | null {
  const edge = edgeOf(graph, id)
  if (!edge) return null
  const upstream = graph.nodes.find((node) => node.id === edge.from)
  const downstream = graph.nodes.find((node) => node.id === edge.to)
  const title = `${upstream?.title || edge.from} → ${downstream?.title || edge.to}`
  const observation = flowObservation(edge, input, flow)
  const matches = retainEdgePulses(history, [], now).filter((pulse) => {
    if (pulse.from === edge.from && pulse.to === edge.to) return true
    const mapped = stagePulse(pulse)
    return mapped?.from === edge.from && mapped.to === edge.to
  })
  return {
    edge,
    title,
    panel: {
      kind: 'edge',
      sections: [
        {
          key: 'path', title: '通道关系', rows: [
            { key: 'from', label: '上游', value: upstream?.title || edge.from, note: edge.from },
            { key: 'to', label: '下游', value: downstream?.title || edge.to, note: edge.to },
            { key: 'relation', label: '关系依据', value: '画布结构依赖', note: '配置依赖不表示该股或该信号实际使用了这条路径。' },
          ],
        },
        { key: 'observation', title: '观测与计量', rows: observation, foot: '来源请求、模型档调用、WS 脉冲是三种不同口径，不能相加或解释为逐记录吞吐。' },
        ...observationSections(input.observability, [...new Set([...topicsForNode(edge.from), ...topicsForNode(edge.to)])]),
        {
          key: 'pulses', title: '最近真实推送标识', table: {
            key: 'pulses', columns: ['页面接收时间', '标识符', '事件 / 链路', '源时间 / 主题'],
            rows: matches.map((pulse) => [new Date(pulse.at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }), `${pulse.identifier.kind}:${pulse.identifier.value}`, `${pulse.eventId || '事件未知'} / ${pulse.traceId || '链路未知'}`, `${pulse.sourceTime || '源时间未知'} / ${pulse.subject || `${pulse.from} → ${pulse.to}`}`]),
            empty: '本会话近 60 秒未见匹配推送；不能据此认定业务没有数据。',
          },
          foot: '仅本次会话，最多保留80条全局推送。页面接收时刻与信封源时间分列；event/trace/subject来自上游外壳，缺失保持未知。画布路径仍是映射，不能仅凭此证明消费确认。',
        },
        {
          key: 'identifiers', title: '上下游节点当前标识符', table: {
            key: 'identifiers', columns: ['节点', '方向', '标识符'],
            rows: [...identifierRows(upstream), ...identifierRows(downstream)],
            empty: '节点当前没有输入输出标识符；原始消息内容未接入。',
          },
          foot: '这是当前节点快照里的输入/输出引用，不是已关联到同一次传输的数据样本。',
        },
        {
          key: 'gaps', title: '尚未观测', rows: [
            { key: 'payload', label: '原始数据内容', value: '未接入', note: '当前只有标识符，未提供此边的原始或标准化 payload。' },
            { key: 'records', label: '数据记录数 / 字节数', value: '未观测', note: '需要生产者与消费者的逐边计量。' },
            { key: 'latency', label: '处理耗时 / P95', value: '未观测', note: '不能用模型档 P95 代替这条通道的耗时。' },
            { key: 'queue', label: '错误 / 积压 / 消费确认', value: '未观测', note: '不能用全局 NATS lag 或节点错误冒充本通道指标。' },
          ],
        },
      ],
    },
  }
}

function flowObservation(edge: SceneEdge, input: MonitorInput, flow: EdgeFlow | undefined): DetailRow[] {
  let source = '当前没有此通道的计数器'
  let unit = '未观测'
  let coverage = '只有结构关系，没有逐边生产/消费观测'
  let rateLabel = '通道速率'
  let cumulative: number | null = null
  let counterAvailable = false
  let window = '未观测'
  let state = '未观测'
  if (edge.from.startsWith('source:')) {
    source = 'GET /api/datahub/sources 的 successCount 差分'
    unit = '成功请求 / 分钟'
    coverage = '整个数据源的成功请求；同源多条边复用计数，不能归因到某个消费者'
    rateLabel = '来源请求速率参考'
    const item = input.sources?.ok ? input.sources.items.find((row) => sourceNodeId(row.id, row.name) === edge.from) : null
    cumulative = item?.successCount ?? null
    counterAvailable = cumulative != null && Number.isFinite(cumulative)
    window = `最近 ${FLOW_WINDOW_MS / 1000} 秒采样，至少跨 20 秒；计数重置后重新观测`
    state = flow?.stalled ? '来源计数 / 活动静默参考，不能证明消费者断流' : '采集成功时间与报价发生时间分别解释'
  } else if (Object.values(MODEL_EDGE_KEYS).flat().includes(edge.id)) {
    const tier = edge.from === 'model:small' ? 'small' : 'large'
    source = `GET /api/brain/stats 的 ${tier} 档 calls 差分`
    unit = '模型档调用 / 分钟'
    coverage = '该档全部任务调用；同档多条边复用计数，没有按专家拆分'
    rateLabel = '模型档调用速率参考'
    cumulative = input.brainStats?.ok ? tierFor(input.brainStats.tiers, tier)?.calls ?? null : null
    counterAvailable = cumulative != null && Number.isFinite(cumulative)
    window = `最近 ${FLOW_WINDOW_MS / 1000} 秒采样，至少跨 20 秒；计数重置后重新观测`
    state = flow?.circuit ? '该模型档熔断' : '实际任务状态请继续查看模型节点'
  } else if (edge.id === KILL_EDGE_ID) {
    source = '页面收到并映射到风控 → 账户的 WS 告警脉冲'
    unit = '告警脉冲 / 分钟'
    coverage = '仅页面已接收的告警推送，不代表风控检查、订单或成交次数'
    rateLabel = 'WS 告警脉冲速率'
    window = '滚动 60 秒；挂载满一个窗口后才给速率'
    state = '推送标识不等于交易执行或消费确认'
    counterAvailable = true
  }
  const rate = flow?.rate
  const measured = counterAvailable && rate != null && Number.isFinite(rate) && rate >= 0 && unit !== '未观测'
  return [
    { key: 'source', label: '观测来源', value: source },
    { key: 'unit', label: '计量单位', value: unit },
    { key: 'window', label: '统计窗口', value: window },
    { key: 'coverage', label: '覆盖范围', value: coverage },
    { key: 'rate', label: rateLabel, value: measured ? `${rate.toFixed(2)} ${unit}` : '未观测：缺少计数或观测窗口不足' },
    { key: 'cumulative', label: '来源累计计数', value: cumulative == null ? '未观测' : String(cumulative), note: '进程内累计；不是此边实际传输记录数。' },
    { key: 'state', label: '状态依据', value: state },
  ]
}

function identifierRows(node: SceneNode | undefined): string[][] {
  if (!node) return []
  return [
    ...node.inputs.map((item) => [node.title, '输入', `${item.kind}:${item.value}`]),
    ...node.outputs.map((item) => [node.title, '输出', `${item.kind}:${item.value}`]),
  ]
}
