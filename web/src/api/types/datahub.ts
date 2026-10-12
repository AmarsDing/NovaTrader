/** M01 数据源治理。经 admin 代理 /api/datahub/*。 */

/**
 * 数据源状态（SourceStatus）。字段与 proto 一一对应，不再只留拼好的 detail 串：
 * 监控详情窗（业务监控方案 §5.4）要按 成功数 / 失败数 / 失败率 / 平均延迟 / 最近失败时间 出。
 */
export interface DataSource {
  /** `域:源名`，与 setSourceEnabled 的路径参数一致 */
  id: string
  name: string
  /** 域（daily_bar / money_flow / news …），与质量问题的 domain 同一口径 */
  kind: string
  enabled: boolean
  status: string
  /** 后端 last_error，没有错误时退化成状态字 */
  detail: string
  /** 最近成功时间（后端 last_success_at） */
  lastAt: string
  successRate?: number | null
  latencyMs?: number | null
  quotaUsed?: number | null
  quotaLimit?: number | null
  active?: boolean
  /** 同域多源的优先级，数字小的先用 */
  priority?: number | null
  /** 是否官方源（通达信/Tushare 这类），区别于兜底源 */
  official?: boolean
  /** 连续失败次数：熔断判定的输入，比成功率更早报警 */
  consecutiveFailures?: number | null
  successCount?: number | null
  failureCount?: number | null
  avgLatencyMs?: number | null
  lastFailureAt?: string
  lastError?: string
  [key: string]: unknown
}

export interface BackfillTask {
  id: string
  source: string
  domain: string
  status: string
  progress: number | null
  message: string
  [key: string]: unknown
}

/**
 * 数据质量问题（QualityIssue）。`GET /api/datahub/quality` 返回的是**逐条问题**，
 * 不是按域汇总的 total/ok/missing/stale——旧的前端类型按汇总写，结果一直是空表。
 */
export interface QualityIssue {
  id: string
  /** 域，与 DataSource.kind 同口径 */
  domain: string
  /** 检查项名（缺数 / 过期 / 双源不一致 …） */
  checkName: string
  symbol: string
  tradeDate: string
  /** high / medium / low */
  severity: string
  /** 出问题的源 */
  source: string
  /** 双源不一致时的对照源 */
  otherSource: string
  message: string
  /** 命中条数 */
  hits: number | null
  lastSeen: string
  [key: string]: unknown
}
