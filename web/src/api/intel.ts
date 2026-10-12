import { request } from '@/api/http'
import { asList, num, pick } from '@/utils/format'
import type { HotWord, IntelItem, IntelTimeline } from './types/intel'

export type { HotWord, IntelItem, IntelTimeline } from './types/intel'

export async function timeline(code: string): Promise<IntelTimeline> {
  const data = await request(`/api/intel/stocks/${encodeURIComponent(code)}/timeline`, { query: { limit: 50 } })
  return {
    sentiment: num(pick(data, 'compositeSentiment', 'composite_sentiment')),
    items: asList(data, 'items').map((raw) => ({
      id: pick<string>(raw, 'id') || '',
      title: pick<string>(raw, 'title') || '',
      time: pick<string>(raw, 'publishTime', 'publish_time') || '',
      sentiment: num(pick(raw, 'effectiveSentiment', 'effective_sentiment', 'sentiment')),
      importance: num(pick(raw, 'importance')),
      source: pick<string>(raw, 'source') || '',
      kind: pick<string>(raw, 'kind') || '',
    })),
  }
}

export async function hotWords(): Promise<HotWord[]> {
  const data = await request('/api/intel/hotwords', { query: { limit: 20 } })
  return asList(data, 'words', 'items').map((raw) => ({
    type: pick<string>(raw, 'type') || '',
    target: pick<string>(raw, 'target') || '',
    count: num(pick(raw, 'count')),
    score: num(pick(raw, 'score')),
  }))
}

/**
 * 按编号取一条情报（GET /v1/intel/items/{id}）。
 * 监控详情窗的证据链只有 `N:123` 这样的编号，标题/来源/时间要靠这一条补齐（§5.2）。
 * 取不到返回 null，调用方写「未返回」，不猜标题。
 */
export async function getIntelItem(id: string | number): Promise<IntelItem | null> {
  const key = String(id).trim()
  if (!key) return null
  const raw = await request(`/api/intel/items/${encodeURIComponent(key)}`)
  if (!raw || typeof raw !== 'object') return null
  const item = raw as Record<string, unknown>
  const title = pick<string>(item, 'title') || ''
  if (!title) return null
  return {
    id: String(pick(item, 'id') ?? key),
    title,
    time: pick<string>(item, 'publishTime', 'publish_time') || '',
    sentiment: num(pick(item, 'effectiveSentiment', 'effective_sentiment', 'sentiment')),
    importance: num(pick(item, 'importance')),
    source: pick<string>(item, 'source') || '',
    kind: pick<string>(item, 'kind') || '',
  }
}
