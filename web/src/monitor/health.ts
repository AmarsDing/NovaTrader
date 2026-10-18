/**
 * 顶栏健康总分（监控业务监控方案 §4.2/§6.3）：五项扣分必须可解释。
 * 纯函数：给定 MonitorInput 与当前时刻，算出总分与五项扣分明细。
 * 测不到的项记 measured=false 并说明缺哪个接口，不编扣分、不装健康。
 */
import type { MonitorInput } from './types'

export type HealthTone = 'accent' | 'text' | 'warn' | 'danger'

export interface HealthPart {
  key: 'fresh' | 'services' | 'model' | 'risk' | 'channel'
  label: string
  /** 本题扣分（≥0）。测不到记 0 并在 note 说明 */
  deduction: number
  /** 扣分原因或「未判定」说明；点名缺的接口 */
  note: string
  measured: boolean
}

export interface HealthScore {
  /** 五项均有观测才给总分；未知项不能按满分计算。 */
  score: number | null
  tone: HealthTone
  measuredCount: number
  totalCount: number
  parts: HealthPart[]
}

/** 五项各自的扣分上限（交易通道按方案的 25 走，总分单独截断到 0） */
const CAPS: Record<HealthPart['key'], number> = { fresh: 20, services: 20, model: 20, risk: 15, channel: 25 }

const HEALTHY = new Set(['ok', 'up', 'running', 'healthy', 'ready', 'live'])

/** 运行授权与账本身份分别解释；保留的 L3 不表示全自动实盘已开放。 */
export function runAuthorizationText(mode: string): string {
  const key = mode.trim().toUpperCase()
  const names: Record<string, string> = {
    L0: 'L0 观察',
    L1: 'L1 模拟',
    L2: 'L2 实盘人工确认',
    L3: 'L3 未开放（按 L2 人工确认）',
  }
  return names[key] ?? (key ? `${key}（未知运行授权）` : '未返回')
}

export function healthTone(score: number): HealthTone {
  if (score >= 90) return 'accent'
  if (score >= 70) return 'text'
  if (score >= 50) return 'warn'
  return 'danger'
}

export function computeHealth(input: MonitorInput, now: number): HealthScore {
  const parts = [freshPart(input, now), servicesPart(input), modelPart(input), riskPart(input), channelPart(input)]
  const measuredCount = parts.filter((part) => part.measured).length
  const total = parts.reduce((sum, part) => sum + part.deduction, 0)
  const score = measuredCount === parts.length ? Math.max(0, Math.min(100, Math.round(100 - total))) : null
  const knownFailure = parts.some((part) => part.measured && part.deduction >= CAPS[part.key])
  return { score, tone: score == null ? (knownFailure ? 'danger' : 'warn') : healthTone(score), measuredCount, totalCount: parts.length, parts }
}

/** 这里只观测情绪结果生成时效；源 lastAt 是采集调用成功时间，不能代替报价的源时钟。 */
function freshPart(input: MonitorInput, now: number): HealthPart {
  const part = { key: 'fresh' as const, label: '市场结果时效' }
  const result = input.sentiment?.ok ? input.sentiment : null
  const generatedAt = result ? Date.parse(result.asOf) : NaN
  if (!Number.isFinite(generatedAt)) {
    return { ...part, deduction: 0, measured: false, note: '未判定：情绪结果 asOf 未返回；源数据时钟未接入，采集成功 lastAt 不能代替报价时间' }
  }
  const ageSec = Math.max(0, Math.round((now - generatedAt) / 1000))
  const deduction = result?.stale ? CAPS.fresh : Math.min(CAPS.fresh, ageSec < 10 ? 0 : Math.floor(ageSec / 10) * 4)
  return {
    ...part,
    deduction,
    measured: true,
    note: `${result?.stale ? '情绪结果已标记陈旧，扣 20；' : ''}结果生成距今 ${ageSec}s${!result?.stale && deduction > 0 ? '，超 10s 起每 10s 扣 4' : ''}；不代表逐证券报价时效`,
  }
}

/** 服务存活：每个非健康服务扣 6，扣完 20 为止 */
function servicesPart(input: MonitorInput): HealthPart {
  if (!input.ops?.ok || input.ops.services.length === 0) {
    return { key: 'services', label: '服务存活', deduction: 0, measured: false, note: '未判定：运维状态未返回（GET /api/ops/status）' }
  }
  const broken = input.ops.services.filter((item) => !HEALTHY.has(item.status.trim().toLowerCase())).map((item) => item.name)
  if (broken.length === 0) return { key: 'services', label: '服务存活', deduction: 0, measured: true, note: `${input.ops.services.length} 个服务都在线` }
  const deduction = Math.min(CAPS.services, broken.length * 6)
  return { key: 'services', label: '服务存活', deduction, measured: true, note: `${broken.join('、')} 不在线，每个扣 6` }
}

/** 模型可用：熔断每层扣 12，P95 每超 1s 扣 2，合计扣完 20 为止 */
function modelPart(input: MonitorInput): HealthPart {
  if (!input.brainStats?.ok || input.brainStats.tiers.length === 0) {
    return { key: 'model', label: '模型可用', deduction: 0, measured: false, note: '未判定：模型统计未返回（GET /v1/brain/stats）' }
  }
  const reasons: string[] = []
  let deduction = 0
  const unconfigured = input.brainStats.tiers.filter((tier) => !tier.configured)
  if (unconfigured.length > 0) {
    deduction += Math.min(CAPS.model, unconfigured.length * 12)
    reasons.push(`${unconfigured.map((tier) => tier.tier || '某层').join('、')} 未配置`)
  }
  const open = input.brainStats.tiers.filter((tier) => tier.circuitOpen)
  for (const tier of open) {
    deduction += 12
    reasons.push(`${tier.tier || tier.model || '某层'} 熔断 -12`)
  }
  const p95 = Math.max(0, ...input.brainStats.tiers.map((tier) => tier.p95Ms ?? 0))
  if (p95 > 5000) {
    const over = Math.ceil((p95 - 5000) / 1000) * 2
    deduction += over
    reasons.push(`P95 ${Math.round(p95 / 1000)}s 超 5s，-${over}`)
  }
  deduction = Math.min(CAPS.model, deduction)
  if (deduction === 0) {
    const hasLatency = input.brainStats.tiers.some((tier) => tier.calls != null && tier.calls > 0 && tier.p95Ms != null)
    return { key: 'model', label: '模型可用', deduction: 0, measured: true, note: hasLatency ? '已配置，无熔断，P95 在 5s 内' : '已配置，无熔断；延迟样本未返回' }
  }
  return { key: 'model', label: '模型可用', deduction, measured: true, note: reasons.join('；') }
}

/** 风控是否在线以状态观测为据；请求失败不能当成已测得的业务状态。 */
function riskPart(input: MonitorInput): HealthPart {
  if (!input.risk?.ok) {
    return { key: 'risk', label: '风控在线', deduction: 0, measured: false, note: '未判定：风控状态未返回（GET /api/risk/state）' }
  }
  if (input.risk.killActive) {
    return { key: 'risk', label: '风控在线', deduction: 0, measured: true, note: `急停中：${input.risk.killReason || '原因未写明'}` }
  }
  return { key: 'risk', label: '风控在线', deduction: 0, measured: true, note: `在线，运行授权 ${runAuthorizationText(input.risk.mode)}` }
}

/** 账本身份决定通道；L0–L3 是运行授权，不能拿它猜 SIM/LIVE。 */
export function channelPart(input: MonitorInput): HealthPart {
  const part = { key: 'channel' as const, label: '交易通道' }
  const book = input.account?.ok ? input.account.book.trim().toUpperCase() : ''
  if (book === 'SIM') return { ...part, deduction: 5, measured: true, note: 'SIM 模拟盘：不下实盘文件单，扣 5' }
  if (book !== 'LIVE') return { ...part, deduction: 0, measured: false, note: '未判定：账户账本未返回或无法识别，不能判断 SIM/LIVE' }

  const trade = input.ops?.ok ? input.ops.services.find((item) => item.name.trim().toLowerCase() === 'trade') : undefined
  if (trade && ['down', 'error', 'unavailable', 'failed', 'closed'].includes(trade.status.trim().toLowerCase())) {
    return { ...part, deduction: CAPS.channel, measured: true, note: `LIVE 实盘：交易服务失联（${trade.status}），扣 25` }
  }
  const broker = input.opsP1?.ok ? input.opsP1.brokerFiles : null
  if (!broker) return { ...part, deduction: 0, measured: false, note: '未判定：文件单探针未返回（GET /api/ops/broker-files）' }
  if (!broker.available) return { ...part, deduction: CAPS.channel, measured: true, note: `LIVE 实盘：文件单未接入，扣 25；${broker.note || '探针报告不可用'}` }
  return { ...part, deduction: 0, measured: true, note: 'LIVE 实盘：文件单探针可达；成交与对账仍以真实回报为准' }
}
