import { request } from '@/api/http'
import { asList, pick, type RawRecord } from '@/utils/format'
import type { OpsNode, OpsStatus } from './types/admin'

export type { OpsModel, OpsNode, OpsStatus } from './types/admin'

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
