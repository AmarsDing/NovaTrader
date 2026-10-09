import { request } from '@/api/http'
import { asList, pick, type RawRecord } from '@/utils/format'
import type { AuditEntry } from './types/admin'

export type { AuditEntry } from './types/admin'

/**
 * 审计日志读取（M11）。
 * 注意：admin 网关的 GET /api/v1/audit 是 P4 后端增量，
 * 接口未就绪时会返回 404，由调用方按 messageOf 提示兜底。
 */
export async function listAudit(query: Record<string, unknown> = {}): Promise<AuditEntry[]> {
  const data = await request('/api/v1/audit', { query })
  return asList(data, 'items', 'entries', 'audit').map(normalizeEntry)
}

function normalizeEntry(raw: RawRecord): AuditEntry {
  return {
    id: pick<string>(raw, 'id') || '',
    at: pick<string>(raw, 'at', 'time', 'createdAt', 'created_at') || '',
    actor: pick<string>(raw, 'actor', 'operator', 'user') || '',
    action: pick<string>(raw, 'action') || '',
    target: pick<string>(raw, 'target', 'object') || '',
    detail: pick<string>(raw, 'detail', 'message') || '',
  }
}
