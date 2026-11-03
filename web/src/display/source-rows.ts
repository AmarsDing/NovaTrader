/**
 * 数据源行归一（W0-3b / FE-D1）：NtSourceTable 的单一事实层。
 * 此前三处口径各自裁剪——health/center 两面板逐字复制 statusOf（center 自承「拧成面板内小函数」）、
 * OpsView 与 ConfigCenter 的网关行各写各的状态映射——改一处漏三处。本模块收口：
 * 状态灯、状态词（册取）、行归一全部唯一出处，四个消费面（health/center/Ops/ConfigCenter）只喂数据。
 */
import type { DataSource } from '@/api/datahub'
import type { OpsNode } from '@/api/ops'
import { linkStatus, statusOf } from '@/ui/status-map'
import type { DotStatus } from '@/ui/NtStatusDot.vue'
import { serverNow } from '@/utils/server-clock'

/** 数据源新鲜度线：与两面板原 STALE_MS 同值（30min），面板头 NtFreshness 与本灯共用一条线。 */
export const SOURCE_STALE_MS = 30 * 60 * 1000

/**
 * 数据源状态灯（唯一出处，原两面板逐字复制）：enabled + 断路态 + lastAt 新鲜度 → 链路五态。
 * off=关停、down=断路/不可用/报错、stale=半开或无最近成功/新鲜度超线、live=30min 内有成功采集。
 */
export function sourceStatus(s: Pick<DataSource, 'enabled' | 'status' | 'lastAt'>): DotStatus {
  if (!s.enabled) return 'off'
  const state = (s.status || '').toLowerCase()
  if (state === 'open' || state === 'unavailable' || state.includes('down') || state.includes('error')) return 'down'
  if (state === 'half_open') return 'stale'
  if (!s.lastAt) return 'stale'
  const at = new Date(s.lastAt).getTime()
  if (!Number.isFinite(at)) return 'stale'
  /* P2-10：lastAt 是服务端戳——新鲜度与后端时源对齐（未校准回退本地钟）。 */
  return serverNow() - at < SOURCE_STALE_MS ? 'live' : 'stale'
}

/** health 变体行（listSources 读面归一）：NtSourceTable 直接消费，面板不再拼单元格。 */
export interface SourceHealthRow {
  key: string
  name: string
  domain: string
  status: DotStatus
  /** 状态点带字（§7.2-1 禁裸色点）：label = `名称 · 册内状态词`。 */
  dotLabel: string
  successRate: number | null
  latencyMs: number | null
  successCount: number | null
  failureCount: number | null
  consecutiveFailures: number | null
  lastAt: string
  enabled: boolean
}

export function toHealthRow(s: DataSource): SourceHealthRow {
  const status = sourceStatus(s)
  return {
    key: s.id,
    name: s.name || s.id,
    domain: s.kind,
    status,
    dotLabel: `${s.name || s.id} · ${linkStatus(status).text}`,
    successRate: s.successRate ?? null,
    latencyMs: s.latencyMs ?? null,
    successCount: s.successCount ?? null,
    failureCount: s.failureCount ?? null,
    consecutiveFailures: s.consecutiveFailures ?? null,
    lastAt: s.lastAt,
    enabled: s.enabled,
  }
}

/**
 * 网关四态 → 链路五态点 + 册内状态词（唯一出处；原 OpsView dotStatus/nodeStatusText 两份）。
 * ok 族=live 绿；warn=stale 点取 warn 色（半开/覆盖率未证实等降级类——此前落 default 画红，
 * 把「降级」「没测过」谎报成「中断」已修）；unknown/空=off 中性灰（未观测/未配置等结构性缺省，
 * 不冒充「关闭」语义）；down 与其余异常=红。精确归因由行内 detail 列承载。
 */
export function opsNodeStatus(status: string): { dot: DotStatus; text: string } {
  const text = String(status || '').toLowerCase()
  const dot: DotStatus = ['ok', 'up', 'running', 'healthy', 'online'].includes(text)
    ? 'live'
    : text === 'warn'
      ? 'stale'
      : text === 'unknown' || !text
        ? 'off'
        : 'down'
  /* 状态词只从册取（§7.1，一字锁定）：warn=册键 degraded.partial「部分降级」，unknown=册键 ops.unknown「未知」。 */
  const word =
    text === 'warn' ? statusOf('degraded.partial').text : text === 'unknown' ? statusOf('ops.unknown').text : linkStatus(dot).text
  return { dot, text: word }
}

/** ops 变体行（opsStatus 网关读面归一）：OpsView 数据源块与 ConfigCenter 链路页清单共用。 */
export interface OpsSourceRow {
  key: string
  name: string
  status: DotStatus
  /** 册内状态词（点带字，§7.2-1 禁裸色点）。 */
  statusText: string
  /** 网关原始字面值原样直读（数据原样，非自封文案）。 */
  rawStatus: string
  detail: string
}

export function toOpsSourceRow(n: OpsNode): OpsSourceRow {
  const st = opsNodeStatus(n.status)
  return { key: n.name, name: n.name, status: st.dot, statusText: st.text, rawStatus: n.status, detail: n.detail }
}
