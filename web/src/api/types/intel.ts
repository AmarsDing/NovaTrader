/** M03 情报流与热词。经 admin 代理 /api/intel/*。 */

export interface IntelItem {
  id: string
  title: string
  time: string
  sentiment: number | null
  importance: number | null
  source: string
  kind: string
  /** 公告/研报原文外链：后端 proto 已下发 url 字段，前端此前丢弃；无链为 undefined，UI 走内链通道。 */
  url?: string
}

export interface IntelTimeline {
  sentiment: number | null
  items: IntelItem[]
}

export interface HotWord {
  type: string
  target: string
  count: number | null
  score: number | null
}

export interface Alias {
  alias: string
  symbol: string
  name: string
}
