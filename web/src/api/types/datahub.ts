/** M01 数据源治理。经 admin 代理 /api/datahub/*。 */

export interface DataSource {
  id: string
  name: string
  kind: string
  enabled: boolean
  status: string
  detail: string
  lastAt: string
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

export interface QualityReport {
  domain: string
  tradeDate: string
  total: number | null
  ok: number | null
  missing: number | null
  stale: number | null
  detail: string
  [key: string]: unknown
}
