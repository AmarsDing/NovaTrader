/** M03 情报流与热词。经 admin 代理 /api/intel/*。 */

export interface IntelItem {
  id: string
  title: string
  time: string
  sentiment: number | null
  importance: number | null
  source: string
  kind: string
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
