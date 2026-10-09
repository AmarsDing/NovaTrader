import { request } from '@/api/http'
import { asList, pick, type RawRecord } from '@/utils/format'
import type { NotifyMessage } from './types/notify'

export type { NotifyMessage } from './types/notify'

export async function listMessages(query: Record<string, unknown> = {}): Promise<NotifyMessage[]> {
  const data = await request('/api/notify/messages', { query })
  return asList(data, 'items', 'messages').map(normalizeMessage)
}

export function getMessage(id: string): Promise<unknown> {
  return request(`/api/notify/messages/${encodeURIComponent(id)}`)
}

function normalizeMessage(raw: RawRecord): NotifyMessage {
  return {
    id: pick<string>(raw, 'id') || '',
    kind: pick<string>(raw, 'kind', 'type') || '',
    level: pick<string>(raw, 'level', 'severity') || '',
    title: pick<string>(raw, 'title') || '',
    body: pick<string>(raw, 'body', 'content') || '',
    time: pick<string>(raw, 'time', 'createdAt', 'created_at') || '',
    read: !!(raw.read || raw.readAt || raw.read_at),
  }
}
