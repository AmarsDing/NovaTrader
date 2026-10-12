import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import type { BackupResult, OpsNode, OpsStatus } from './types/admin'

export type { BackupResult, OpsModel, OpsNode, OpsStatus } from './types/admin'

/** 备份二次确认口令，与服务端 gateway.BackupConfirm 一致。 */
export const BACKUP_CONFIRM = 'BACKUP'

/**
 * 触发一次数据库备份（FR-11-08：二次确认由 confirm 承载）。
 * pg_dump 可能跑几分钟，这里放宽到 10 分钟，超过 admin 服务器的 600s 上限也就到顶了。
 */
export async function runBackup(confirm: string): Promise<BackupResult> {
  const raw = await request('/api/ops/backup', {
    method: 'POST',
    json: { confirm },
    timeoutMs: 600000,
  })
  return {
    path: pick<string>(raw, 'path') || '',
    size: num(pick(raw, 'size')),
    database: pick<string>(raw, 'database') || '',
    durationMs: num(pick(raw, 'durationMs', 'duration_ms')),
    at: pick<string>(raw, 'at') || '',
  }
}

export async function opsStatus(): Promise<OpsStatus> {
  const data = await request('/api/ops/status')
  return {
    services: asList(data, 'services').map(normalizeNode),
    sources: asList(data, 'sources', 'dataSources', 'data_sources').map(normalizeNode),
    models: asList(data, 'models').map((raw) => ({
      ...normalizeNode(raw),
      latency: pick<string>(raw, 'latencyMs', 'latency_ms', 'latency') || '',
    })),
    resources: asList(data, 'resources').map(normalizeNode),
  }
}

function normalizeNode(raw: RawRecord): OpsNode {
  return {
    name: pick<string>(raw, 'name') || '',
    status: pick<string>(raw, 'status') || '',
    detail: pick<string>(raw, 'detail', 'message') || '',
  }
}
