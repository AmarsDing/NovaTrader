import type { ObservabilitySnapshot } from '@/api/observability'
import type { DetailSection } from './detail'

export function topicsForNode(id: string): string[] {
  if (id === 'book:center' || id === 'service:trade') return ['position']
  if (id === 'post:risk' || id === 'service:risk') return ['alert']
  if (id === 'post:desk' || id === 'service:strategy') return ['signal']
  if (id === 'service:market' || id === 'post:scan') return ['market']
  if (id.startsWith('model:') || id.startsWith('dim:') || id === 'post:model' || id === 'service:brain') return ['brain']
  if (id === 'service:nats' || id === 'service:admin') return ['signal', 'position', 'alert', 'market', 'brain']
  return []
}

/** 这是相关主题到网关的观测，不能把一份计数分摊给画布内部假想的边。 */
export function observationSections(snapshot: ObservabilitySnapshot | null | undefined, topics: string[]): DetailSection[] {
  if (!topics.length) return []
  if (!snapshot?.available) return [{ key: 'ingress-unknown', title: '真实事件观测', rows: [
    { key: 'available', label: '网关收入口径', value: '未接入或观测合同不可用', note: snapshot?.note || '需要 GET /api/ops/observability，缺失不填0。' },
  ] }]
  const rows = snapshot.edges.filter((edge) => topics.includes(edge.topic))
  const samples = snapshot.samples.filter((sample) => topics.includes(sample.topic)).slice(0, 12)
  const routes = snapshot.registeredRoutes.filter((route) => topics.includes(route.topic))
  const number = (value: number | null) => value == null ? '未知' : String(value)
  const binding: Record<string, string> = { declared: '已登记 · 待绑定', bound: '绑定成功', failed: '绑定失败' }
  return [
    { key: 'ingress-basis', title: '相关主题 · 网关真实接收', rows: [
      { key: 'scope', label: '观测边界', value: 'admin网关收到的事件', note: '不能证明画布两端业务成功，也不是下游消费确认。' },
      { key: 'window', label: '计量窗口', value: `${snapshot.windowStart || '未知'} → ${snapshot.asOf || '未知'}`, note: snapshot.windowBasis },
      { key: 'coverage', label: '窗口覆盖', value: `${snapshot.windowComplete ? '完整窗口' : '窗口尚不完整'} · 已观测${snapshot.windowObservedSeconds == null ? '未知' : snapshot.windowObservedSeconds.toFixed(1)}秒`, note: `目标${number(snapshot.windowSeconds)}秒；当前秒仍在累计，启动阶段不能按完整窗口推算速率。` },
      { key: 'connection', label: 'NATS连接', value: snapshot.natsConnected ? '已连接' : '未连接', note: `实例 ${snapshot.serverEpoch || '未知'}；计数重启归零。` },
      { key: 'capacity', label: '观测容量', value: `样例最多${number(snapshot.sampleCapacity)}条 / 计数器${number(snapshot.counterCapacity)}组`, note: `聚合溢出${number(snapshot.counterOverflowTotal)}次；样例${snapshot.samplesTruncated ? '已截断' : '未截断'}。` },
    ] },
    { key: 'ingress-routes', title: '网关订阅登记', table: {
      key: 'ingress-routes', columns: ['主题 / 传输', '绑定状态', '消费者 / 更新时间'],
      rows: routes.map((route) => [`${route.subject} / ${route.transport}`, binding[route.bindingState] || '未知', `${route.durable || '非持久订阅'} / ${route.bindingUpdatedAt || '未知'}`]),
      empty: '没有返回相关订阅登记，不能推定已订阅。',
    }, foot: '绑定成功表示本实例订阅建立；须结合连接与事件时间判断，不能代替业务就绪或处理成功。' },
    { key: 'ingress-counters', title: '相关主题计数', table: {
      key: 'ingress-counters', columns: ['生产者 / 主题', '本窗接收 / 字节 / 错误', '累计接收 / 接纳', '最近接收'],
      rows: rows.map((edge) => [`${edge.source} / ${edge.subject}`, `${number(edge.windowCount)} / ${number(edge.windowBytes)} B / ${number(edge.windowErrors)}`, `${number(edge.receivedTotal)} / ${number(edge.acceptedTotal)}`, edge.lastReceivedAt || '未见事件']),
      empty: '已登记但本实例窗口未见该主题事件；不能断言上游没有处理。',
    }, foot: '累计为本admin实例收入口径；接纳不是业务ACK，重投可能重复计数，不是唯一订单数。' },
    { key: 'ingress-samples', title: '最近真实事件 · 脱敏样例', table: {
      key: 'ingress-samples', columns: ['接收 / 源时刻', 'event / trace / cause', '生产者 / 主题', '白名单数据'],
      rows: samples.map((sample) => [`${sample.receivedAt} / ${sample.sourceTime || '源时间未知'}`, `${sample.eventId || '未知'} / ${sample.traceId || '未知'} / ${sample.causationId || '父事件未知'}`, `${sample.source} / ${sample.subject}`, JSON.stringify(sample.payload)]),
      empty: '本次保留窗口没有相关样例。',
    }, foot: '最多显示12条；数据为服务端白名单投影，账户资金/持仓与原文不下发。源时间未知时不据此计算端到端延迟。' },
  ]
}
