/** admin 网关自有接口（运维总览 / 审计 / 登录）。不经代理。 */

export interface OpsNode {
  name: string
  status: string
  detail: string
}

export interface OpsModel extends OpsNode {
  latency: string
}

export interface OpsStatus {
  services: OpsNode[]
  sources: OpsNode[]
  models: OpsModel[]
  resources: OpsNode[]
}

export interface BackupResult {
  path: string
  size: number | null
  database: string
  durationMs: number | null
  at: string
}
