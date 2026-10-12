/**
 * S6 束线流量宽度与流向点速度（方案 §3.2「束线流量映射」/§4.4.3/§7/§9）。
 *
 * 流量口径 = 每分钟条数。来源只有两种，估不出一律 null（画固定 1.3px，不猜）：
 *   1. 累计计数器差分——数据源 successCount、模型档 calls 都是单调递增的进程内计数
 *      （backend/pkg/llm/stats.go 的 tierStats.calls 只加不减），两次轮询差分除以分钟数；
 *      计数回退（后端重启清零）视为重置，旧样本作废重新攒。
 *   2. WS 脉冲计数——风控→账户这类没有计数器的边，数 60 秒窗口内的脉冲条数。
 *      窗口没攒满之前返回 null，不拿半窗数据外推。
 *
 * 采样缓冲全在前端内存（§9）：不落库、不上传，页面刷新即失。
 */

export interface EdgeFlow {
  /** 每分钟条数；null = 没有可信观测（固定宽度，不猜） */
  rate: number | null
  /** 断流：观测窗满且计数没动。流向点停住转 --nt-warn α0.35（§3.2） */
  stalled: boolean
  /** 模型熔断：该边转 --nt-danger α0.4 并停流向点（§4.4.3） */
  circuit: boolean
}

/** 无流量数据时的固定宽度（§3.2 束线常态：宽 1.3） */
export const FLOW_FIXED_WIDTH = 1.3
/** 流量观测窗口：60 秒（「每分钟条数」的分钟） */
export const FLOW_WINDOW_MS = 60_000
/** 差分至少要跨这么长才算数：轮询 10s 一拍，两拍起步，不然一拍的抖动会被放大成速率 */
const MIN_SPAN_MS = 20_000
/** 环境流向点周期（§3.2：由速率映射 1200–8000ms）与估不出时的固定周期（§11 行 6） */
export const AMBIENT_FAST_MS = 1200
export const AMBIENT_SLOW_MS = 8000
export const AMBIENT_FIXED_MS = 5200
/** 高分信号脉冲阈值（§3.2：≥85 升级，发光预算内的五个对象之一） */
export const HIGH_SCORE = 85
/** 急停态要转红加粗的边（§3.2/§4.4.3：风控→账户） */
export const KILL_EDGE_ID = 'post:risk>book:center'

/**
 * 束线宽度 = `0.8 + min(2.4, log10(每分钟条数+1) * 0.9)` px；null → 固定 1.3px（§3.2）。
 * 0 条/分给 0.8px（比常态细但还在），负数/NaN 按 0 处理，不外推不猜。
 */
export function flowWidth(rate: number | null): number {
  if (rate == null || !Number.isFinite(rate)) return FLOW_FIXED_WIDTH
  const safe = rate > 0 ? rate : 0
  return 0.8 + Math.min(2.4, Math.log10(safe + 1) * 0.9)
}

/**
 * 流向点周期：每个数量级快 ~2.27s，1200–8000ms 封顶；
 * 估不出保持固定 5200ms（§11 行 6：估不出时保持固定周期）。
 */
export function ambientPeriod(rate: number | null): number {
  if (rate == null || !Number.isFinite(rate)) return AMBIENT_FIXED_MS
  const safe = rate > 0 ? rate : 0
  const perDecade = (AMBIENT_SLOW_MS - AMBIENT_FAST_MS) / 3
  const period = AMBIENT_SLOW_MS - Math.log10(safe + 1) * perDecade
  return Math.min(AMBIENT_SLOW_MS, Math.max(AMBIENT_FAST_MS, period))
}

interface CounterSample {
  at: number
  value: number
}

/**
 * 累计计数器 → 每分钟增量。sample() 每轮轮询喂一次（successCount / calls），rate() 随时读。
 * 环形缓冲只留窗口内的样本（至少两颗），计数回退 = 后端重启，整段作废重新攒。
 */
export class CounterFlow {
  private readonly windowMs: number
  private readonly rings = new Map<string, CounterSample[]>()

  constructor(windowMs = FLOW_WINDOW_MS) {
    this.windowMs = windowMs
  }

  sample(key: string, value: number | null, at: number): void {
    if (value == null || !Number.isFinite(value)) return
    const ring = this.rings.get(key) ?? []
    const prev = ring[ring.length - 1]
    if (prev && value < prev.value) {
      /* 计数回退：后端重启清零，旧差分不再可信 */
      this.rings.set(key, [{ at, value }])
      return
    }
    ring.push({ at, value })
    const cutoff = at - this.windowMs
    while (ring.length > 2 && ring[0].at < cutoff) ring.shift()
    this.rings.set(key, ring)
  }

  /**
   * 窗口内首末样本差分 / 分钟数。样本不足两颗、跨度不够、或太久没喂新样本（页面藏起、
   * 轮询停了）都返回 null——观测过期时宁可回到固定宽度，不拿旧速率冒充现在。
   */
  rate(key: string, at: number): number | null {
    const ring = this.rings.get(key)
    if (!ring || ring.length < 2) return null
    const first = ring[0]
    const last = ring[ring.length - 1]
    const span = last.at - first.at
    if (span < MIN_SPAN_MS) return null
    if (at - last.at > this.windowMs * 2) return null
    return Math.max(0, (last.value - first.value) / (span / 60_000))
  }
}

/**
 * WS 脉冲 → 每分钟条数。只对显式 observe() 过的边作答：没声明观测的边永远 null，
 * 不拿 0 冒充「没有流量」。窗口没攒满同样 null（不拿半窗外推）。
 */
export class PulseFlow {
  private readonly windowMs: number
  private readonly hits = new Map<string, number[]>()
  private readonly observed = new Set<string>()
  private startedAt = 0

  constructor(windowMs = FLOW_WINDOW_MS) {
    this.windowMs = windowMs
  }

  /** 声明这条边在脉冲口径的观测范围内，从此刻起攒窗口 */
  observe(key: string, at: number): void {
    this.observed.add(key)
    if (!this.startedAt) this.startedAt = at
  }

  record(key: string, at: number): void {
    if (!this.startedAt) this.startedAt = at
    const list = this.hits.get(key) ?? []
    list.push(at)
    const cutoff = at - this.windowMs
    this.hits.set(key, list.filter((time) => time > cutoff))
  }

  rate(key: string, at: number): number | null {
    if (!this.observed.has(key) || !this.startedAt) return null
    if (at - this.startedAt < this.windowMs) return null
    const cutoff = at - this.windowMs
    const count = (this.hits.get(key) ?? []).filter((time) => time > cutoff).length
    return count / (this.windowMs / 60_000)
  }
}

/** 模型档 → 它负责的维度边（与 canvas-stage 的 MODEL_LINKS 同口径） */
export const MODEL_EDGE_KEYS: Record<'small' | 'large', string[]> = {
  small: ['model:small>dim:tech', 'model:small>dim:capital'],
  large: ['model:large>dim:news', 'model:large>dim:overseas', 'model:large>dim:synth'],
}

interface TierLike {
  tier: string
  circuitOpen?: boolean
  calls?: number | null
}

/** 与 scene-model.buildModelNodes 同口径挑档：先按 tier 名，再按位置兜底 */
export function tierFor<T extends TierLike>(tiers: T[], which: 'small' | 'large'): T | undefined {
  if (which === 'large') return tiers.find((tier) => tier.tier === 'large') ?? tiers[0]
  return tiers.find((tier) => tier.tier === 'small') ?? tiers[1]
}
