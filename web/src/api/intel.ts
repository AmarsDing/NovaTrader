import { request } from '@/api/http'
import { asList, num, pick } from '@/utils/format'
import type { HotWord, IntelTimeline } from './types/intel'

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
