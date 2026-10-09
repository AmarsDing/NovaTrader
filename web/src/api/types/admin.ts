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

export interface AuditEntry {
  id: string
  at: string
  actor: string
  action: string
  target: string
  detail: string
  [key: string]: unknown
}
