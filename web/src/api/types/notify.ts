/** M10 通知中心。经 admin 代理 /api/notify/*。 */

export interface NotifyMessage {
  id: string
  kind: string
  level: string
  title: string
  body: string
  time: string
  read: boolean
  /** 关联股票代码（如 600519.SH）；来源事件没有该键时为 undefined（不从正文推断）。 */
  symbol?: string
  /** 明确方向字段（buy/sell），来源事件没有该键时为 undefined（不解析正文）。 */
  side?: string
  /** 明确方向字段（up/down），来源事件没有该键时为 undefined（不解析正文）。 */
  direction?: string
  [key: string]: unknown
}
