import { request } from '@/api/http'
import { asList, pick, type RawRecord } from '@/utils/format'

export interface AuditRow {
  id: number
  actor: string
  role: string
  action: string
  target: string
  detail: string
  ok: boolean
  createdAt: string
}

export function normalizeRow(raw: RawRecord): AuditRow {
  return {
    id: Number(pick<number>(raw, 'id') || 0),
    actor: pick<string>(raw, 'actor') || '',
    role: pick<string>(raw, 'role') || '',
    action: pick<string>(raw, 'action') || '',
    target: pick<string>(raw, 'target') || '',
    detail: pick<string>(raw, 'detail') || '',
    ok: !!(raw.ok ?? raw.OK),
    createdAt: pick<string>(raw, 'created_at', 'createdAt') || '',
  }
}

export async function listAudit(query: Record<string, unknown> = {}): Promise<AuditRow[]> {
  const data = await request('/api/v1/audit', { query })
  return asList(data, 'items', 'rows').map(normalizeRow)
}
