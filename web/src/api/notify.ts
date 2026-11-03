import { request } from '@/api/http'
import { asList, pick, type RawRecord } from '@/utils/format'
import { causeKey } from '@/ui/alert-levels'
import type { NotifyMessage } from './types/notify'

export type { NotifyMessage } from './types/notify'

export async function listMessages(query: Record<string, unknown> = {}): Promise<NotifyMessage[]> {
  const data = await request('/api/notify/messages', { query })
  return asList(data, 'items', 'messages').map(normalizeMessage)
}

export function getMessage(id: string): Promise<unknown> {
  return request(`/api/notify/messages/${encodeURIComponent(id)}`)
}

/**
 * 服务端已读（批次 A5）：POST /notify/v1/messages/read {ids}——proto HTTP 绑定早已就绪，
 * 此前只落 localStorage（换机丢）。调用方过滤本地合成 id（ws-/feed- 前缀不在服务端收件箱），
 * 空数组不要调用（空 ids 的服务端语义未在契约钉死）。
 */
export function markRead(ids: string[]): Promise<unknown> {
  return request('/api/notify/messages/read', { method: 'POST', json: { ids } })
}

/**
 * REST 条归一（M10 错配修复 W2-a）：proto MessageReply 只有 priority/category/dedup_key——
 * level：原 level/severity 优先，缺省回落 priority（critical 命中 funnel L4；high/medium 落 L1，与册现状一致，留账）；
 * kind：原 kind/type 缺省回落 category（signal/risk/intel/market/fill/briefing/review），供类别过滤与分级；
 * causeKey：由 dedup_key 推导，与 WS 推送条同口径，通知中心 feed/api 合并去重靠它。
 */
function normalizeMessage(raw: RawRecord): NotifyMessage {
  const priority = pick<string>(raw, 'priority') || ''
  const category = pick<string>(raw, 'category') || ''
  const dedupKey = pick<string>(raw, 'dedupKey', 'dedup_key') || ''
  return {
    id: pick<string>(raw, 'id') || '',
    kind: pick<string>(raw, 'kind', 'type') || category,
    level: pick<string>(raw, 'level', 'severity') || priority,
    title: pick<string>(raw, 'title') || '',
    body: pick<string>(raw, 'body', 'content') || '',
    time: pick<string>(raw, 'time', 'createdAt', 'created_at') || '',
    read: !!(raw.read || raw.readAt || raw.read_at),
    priority,
    category,
    dedupKey,
    causeKey: causeKey({ dedupKey }),
  }
}
