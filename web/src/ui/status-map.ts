/**
 * 全站状态册（方案 §7.1）：状态文案 + tone 的唯一出处，面板禁自封文案。
 *
 * 两族入口：
 * - linkStatus(key)：链路五态（live/ok/stale/down/off），枚举对齐 NtStatusDot 的 DotStatus；
 * - statusOf(key)：业务状态册，dot 键 = '分组.状态'（如 'alert.armed'）。
 *
 * 纪律：
 * - 新增状态必须先入册再使用，文案一字锁定（status-map.test.ts 逐条断言，改文案即改测试并经评审）；
 * - tone 与 NtBadge 的 BadgeTone 同枚举，别再造 tone 词表；
 * - lint 候选方向（§7.1）：禁面板 scoped 内散写含「色」的状态 class（.warn/.danger 文字级
 *   只允许经本册导出），由后续 G4 棘轮收编。
 */
import type { BadgeTone } from './NtBadge.vue'
import type { DotStatus } from './NtStatusDot.vue'

/** 册 tone 与 NtBadge 对齐，是唯一合法 tone 集合。 */
export type StatusTone = BadgeTone

export interface StatusEntry {
  /** 锁定文案（一字不差，测试锁死）。 */
  readonly text: string
  readonly tone: StatusTone
}

/**
 * 链路五态（§7.1 文案锁定）：「在线」仅指链路/心跳证据；「行情已过期」用 stale，
 * 不得混写「系统正常」。off「关闭」在 NtStatusDot 以 --nt-text-3 呈现，册内以 neutral 记。
 */
export const LINK_STATUS: Readonly<Record<DotStatus, StatusEntry>> = Object.freeze({
  live: { text: '在线', tone: 'success' },
  ok: { text: '正常', tone: 'success' },
  stale: { text: '陈旧', tone: 'warn' },
  down: { text: '中断', tone: 'danger' },
  off: { text: '关闭', tone: 'neutral' },
} satisfies Record<DotStatus, StatusEntry>)

/** 业务状态册（§7.1 首批，全册只读，入册才可引用）。 */
export const STATUS_MAP = Object.freeze({
  'alert.armed': { text: '布防中', tone: 'accent' },
  'alert.triggered': { text: '已触发', tone: 'warn' },
  'alert.released': { text: '已解除', tone: 'neutral' },
  'degraded.partial': { text: '部分降级', tone: 'warn' },
  'degraded.model': { text: '模型降级', tone: 'danger' },
  'degraded.data': { text: '数据缺失', tone: 'warn' },
  'degraded.retrieval': { text: '检索降级', tone: 'warn' },
  'auth.unauthorized': { text: '未授权', tone: 'danger' },
  'auth.sim': { text: '模拟SIM', tone: 'accent' },
  'auth.live': { text: '实盘LIVE·授权', tone: 'success' },
  'replay.active': { text: '回看', tone: 'accent' },
  'kb.pending': { text: '待补', tone: 'warn' },
  'kb.ingested': { text: '已入库', tone: 'accent' },
  'evolution.approved': { text: '已批准', tone: 'success' },
  'evolution.rejected': { text: '已拒绝', tone: 'danger' },
  'evolution.pending': { text: '待审', tone: 'warn' },
  'audit.success': { text: '成功', tone: 'success' },
  'audit.failure': { text: '失败', tone: 'danger' },
  'security.suspended': { text: '停牌', tone: 'neutral' },
  'security.st': { text: 'ST', tone: 'warn' },
} as const satisfies Record<string, StatusEntry>)

export type StatusKey = keyof typeof STATUS_MAP

/** 查业务状态册：键不在册内直接编译期报错，不给运行时兜底（防自封文案回流）。 */
export function statusOf(key: StatusKey): StatusEntry {
  return STATUS_MAP[key]
}

/** 查链路五态：枚举来自 NtStatusDot，面板直接传现有 status 值。 */
export function linkStatus(key: DotStatus): StatusEntry {
  return LINK_STATUS[key]
}
