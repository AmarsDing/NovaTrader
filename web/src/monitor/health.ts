/**
 * 顶栏健康总分（监控业务监控方案 §4.2/§6.3）：五项等权，扣分必须可解释。
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
  score: number
  tone: HealthTone
  parts: HealthPart[]
}

/** 五项各自的扣分上限（交易通道按方案的 25 走，总分单独截断到 0） */
const CAPS: Record<HealthPart['key'], number> = { fresh: 20, services: 20, model: 20, risk: 15, channel: 25 }

const HEALTHY = new Set(['ok', 'up', 'running', 'healthy', 'ready', 'live'])
const MARKET_SOURCE = /通达信|tdx|akshare|行情/i

export function healthTone(score: number): HealthTone {
  if (score >= 90) return 'accent'
  if (score >= 70) return 'text'
  if (score >= 50) return 'warn'
  return 'danger'
}

export function computeHealth(input: MonitorInput, now: number): HealthScore {
  const parts = [freshPart(input, now), servicesPart(input), modelPart(input), riskPart(input), channelPart(input)]
  const total = parts.reduce((sum, part) => sum + part.deduction, 0)
  const score = Math.max(0, Math.min(100, Math.round(100 - total)))
  return { score, tone: healthTone(score), parts }
}

/** 数据新鲜度：行情时钟距今 <10s 满分，每超 10s 扣 4，扣完 20 为止 */
function freshPart(input: MonitorInput, now: number): HealthPart {
  const stamps: number[] = []
  for (const item of input.sources?.ok ? input.sources.items : []) {
    if (!MARKET_SOURCE.test(item.name)) continue
    const at = Date.parse(item.lastAt)
    if (Number.isFinite(at)) stamps.push(at)
  }
  if (input.sentiment?.ok) {
    const at = Date.parse(input.sentiment.asOf)
    if (Number.isFinite(at)) stamps.push(at)
  }
  if (stamps.length === 0) {
    return { key: 'fresh', label: '数据新鲜度', deduction: 0, measured: false, note: '未判定：行情时钟未返回（数据源 lastAt 与情绪 asOf 都缺）' }
  }
  const ageSec = Math.max(0, Math.round((now - Math.max(...stamps)) / 1000))
  const deduction = Math.min(CAPS.fresh, ageSec < 10 ? 0 : Math.floor(ageSec / 10) * 4)
  return {
    key: 'fresh',
    label: '数据新鲜度',
    deduction,
    measured: true,
    note: deduction === 0 ? `行情距今 ${ageSec}s` : `行情距今 ${ageSec}s，超 10s 起每 10s 扣 4`,
  }
}

/** 服务存活：每个非健康服务扣 6，扣完 20 为止 */
function servicesPart(input: MonitorInput): HealthPart {
  if (!input.ops?.ok) {
    return { key: 'services', label: '服务存活', deduction: 0, measured: false, note: '未判定：运维状态未返回（GET /api/ops/status）' }
  }
  const broken = input.ops.services.filter((item) => !HEALTHY.has(item.status.trim().toLowerCase())).map((item) => item.name)
  if (broken.length === 0) return { key: 'services', label: '服务存活', deduction: 0, measured: true, note: `${input.ops.services.length} 个服务都在线` }
  const deduction = Math.min(CAPS.services, broken.length * 6)
  return { key: 'services', label: '服务存活', deduction, measured: true, note: `${broken.join('、')} 不在线，每个扣 6` }
}

/** 模型可用：熔断每层扣 12，P95 每超 1s 扣 2，合计扣完 20 为止 */
function modelPart(input: MonitorInput): HealthPart {
  if (!input.brainStats?.ok) {
    return { key: 'model', label: '模型可用', deduction: 0, measured: false, note: '未判定：模型统计未返回（GET /v1/brain/stats）' }
  }
  const reasons: string[] = []
  let deduction = 0
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
  if (deduction === 0) return { key: 'model', label: '模型可用', deduction: 0, measured: true, note: '无熔断，P95 在 5s 内' }
  return { key: 'model', label: '模型可用', deduction, measured: true, note: reasons.join('；') }
}

/** 风控在线：读不到状态扣 15 */
function riskPart(input: MonitorInput): HealthPart {
  if (!input.risk?.ok) {
    return { key: 'risk', label: '风控在线', deduction: CAPS.risk, measured: true, note: '风控状态读不到（GET /v1/risk/state），扣 15' }
  }
  if (input.risk.killActive) {
    return { key: 'risk', label: '风控在线', deduction: 0, measured: true, note: `急停中：${input.risk.killReason || '原因未写明'}` }
  }
  return { key: 'risk', label: '风控在线', deduction: 0, measured: true, note: `在线，模式 ${input.risk.mode || '未返回'}` }
}

/** 交易通道：模拟盘固定扣 5；实盘文件单探针未接入按不可达扣 25 */
function channelPart(input: MonitorInput): HealthPart {
  const mode = input.risk?.ok ? input.risk.mode.trim().toUpperCase() : ''
  const live = mode.includes('LIVE') || mode.includes('REAL')
  if (live) {
    return {
      key: 'channel',
      label: '交易通道',
      deduction: CAPS.channel,
      measured: true,
      note: '实盘模式：文件单探针未接入（GET /v1/ops/broker-files），按不可达扣 25',
    }
  }
  if (!mode) {
    return { key: 'channel', label: '交易通道', deduction: 5, measured: false, note: '交易模式未返回，按模拟盘扣 5；实盘通道探针需要 GET /v1/ops/broker-files' }
  }
  return { key: 'channel', label: '交易通道', deduction: 5, measured: true, note: '模拟盘：无实盘文件单通道，扣 5' }
}
