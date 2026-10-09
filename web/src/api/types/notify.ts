/** M10 通知中心。经 admin 代理 /api/notify/*。 */

export interface NotifyMessage {
  id: string
  kind: string
  level: string
  title: string
  body: string
  time: string
  read: boolean
  [key: string]: unknown
}
