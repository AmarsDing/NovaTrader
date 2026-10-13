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

/* ---------- S7 P1：GPU 与文件单目录（只读） ---------- */

export interface GpuInfo {
  name: string
  memUsedMiB: number
  memTotalMiB: number
  /** -1 = nvidia-smi 返回 N/A，界面写「不可得」，不填 0 */
  utilPct: number
  tempC: number
  powerW: number
}

export interface OpsGpu {
  gpus: GpuInfo[]
  gpuAvailable: boolean
  gpuNote: string
  /** 缺任一段时为 null，界面写「未接入」 */
  brainInFlight: number | null
  brainQueued: number | null
  brainTokensPerHour: number | null
  brainCallsToday: number | null
}

export async function getOpsGpu(): Promise<OpsGpu> {
  const data = await request('/api/ops/gpu')
  const raw = (data ?? {}) as RawRecord
  return {
    gpus: asList<RawRecord>(raw, 'gpus').map((g) => ({
      name: pick<string>(g, 'name') || '',
      memUsedMiB: num(pick(g, 'memUsedMiB', 'mem_used_mib')) ?? 0,
      memTotalMiB: num(pick(g, 'memTotalMiB', 'mem_total_mib')) ?? 0,
      utilPct: num(pick(g, 'utilPct', 'util_pct')) ?? -1,
      tempC: num(pick(g, 'tempC', 'temp_c')) ?? -1,
      powerW: num(pick(g, 'powerW', 'power_w')) ?? -1,
    })),
    gpuAvailable: !!(raw.gpuAvailable ?? raw.gpu_available),
    gpuNote: pick<string>(raw, 'gpuNote', 'gpu_note') || '',
    brainInFlight: num(pick(raw, 'brainInFlight', 'brain_in_flight')),
    brainQueued: num(pick(raw, 'brainQueued', 'brain_queued')),
    brainTokensPerHour: num(pick(raw, 'brainTokensPerHour', 'brain_tokens_per_hour')),
    brainCallsToday: num(pick(raw, 'brainCallsToday', 'brain_calls_today')),
  }
}

export interface OpsBrokerFiles {
  available: boolean
  note: string
  pending: number | null
  written: number | null
  consumed: number | null
  leftover: number | null
  lastOk: string
}

/** 东财 CSV 文件单目录状态。broker-gw 边车当前不存在，available=false 时全是 null。 */
export async function getOpsBrokerFiles(): Promise<OpsBrokerFiles> {
  const data = await request('/api/ops/broker-files')
  const raw = (data ?? {}) as RawRecord
  return {
    available: !!raw.available,
    note: pick<string>(raw, 'note') || '',
    pending: num(pick(raw, 'pending')),
    written: num(pick(raw, 'written')),
    consumed: num(pick(raw, 'consumed')),
    leftover: num(pick(raw, 'leftover')),
    lastOk: pick<string>(raw, 'lastOk', 'last_ok') || '',
  }
}
