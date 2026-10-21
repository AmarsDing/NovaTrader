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

/** 业务状态册（§7.1，首批 20 键 + 第二批散值入册 36 键 + 第三批 board 域 3 键；全册只读，入册才可引用）。 */
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
  // —— 第二批（§7.1 散值入册，36 键）——
  // risk 风控域
  'risk.killswitch': { text: '已触发', tone: 'danger' },
  'risk.breaker': { text: '行情熔断', tone: 'warn' },
  'risk.precheck.pass': { text: '预检通过', tone: 'success' },
  'risk.precheck.blocked': { text: '预检拦截', tone: 'danger' },
  'risk.active': { text: '生效中', tone: 'danger' },
  'risk.standby': { text: '未触发', tone: 'neutral' },
  'risk.unverified': { text: '状态待核对', tone: 'warn' },
  'risk.unverified_active': { text: '待核对·上次已触发', tone: 'warn' },
  // evolution 策略演化域
  'evolution.admitted': { text: '已达准入', tone: 'success' },
  // doc 文档生命周期域
  'doc.active': { text: '有效', tone: 'success' },
  'doc.superseded': { text: '被替换', tone: 'warn' },
  'doc.expired': { text: '已过期', tone: 'warn' },
  'doc.deleted': { text: '已删除', tone: 'danger' },
  // quality 数据质量域
  'quality.high': { text: '高', tone: 'warn' },
  'quality.medium': { text: '中', tone: 'neutral' },
  'quality.low': { text: '低', tone: 'neutral' },
  // quote 行情域：quote.suspended 与 security.suspended 文案同为「停牌」但分级不同——
  // quote=行情域（盘中停牌，danger 紧急）；security=档案域（个股属性标注，neutral 中性），两键并存勿合并。
  'quote.suspended': { text: '停牌', tone: 'danger' },
  'quote.nolimit': { text: '无涨跌幅', tone: 'accent' },
  'quote.stale': { text: '已过期', tone: 'warn' },
  // signal 交易信号域
  'signal.pending': { text: '待审核', tone: 'warn' },
  'signal.risk_checking': { text: '风控审核', tone: 'warn' },
  'signal.approved': { text: '已通过', tone: 'success' },
  'signal.rejected': { text: '已拦截', tone: 'danger' },
  'signal.executing': { text: '执行中', tone: 'accent' },
  'signal.done': { text: '已有成交', tone: 'success' },
  'signal.closed': { text: '已平仓', tone: 'neutral' },
  'signal.expired': { text: '已过期', tone: 'warn' },
  'signal.cancelled': { text: '已取消', tone: 'neutral' },
  // order 订单域
  'order.pending': { text: '待报', tone: 'neutral' },
  'order.submitted': { text: '已报', tone: 'accent' },
  'order.pending_confirm': { text: '待人工确认', tone: 'warn' },
  'order.partial': { text: '部成', tone: 'warn' },
  'order.filled': { text: '已成', tone: 'success' },
  'order.cancelled': { text: '已撤', tone: 'neutral' },
  'order.rejected': { text: '已拒绝', tone: 'danger' },
  'order.expired': { text: '已过期', tone: 'warn' },
  // —— 第三批（board 板块标识域，3 键；U4 梯队角标经 display/NtBoardTag 消费）——
  // tone 即波动档语义：20% 档（创业板/科创板）accent，30% 档（北交所）warn。
  // 主板无键不打标——主板是默认态，角标只标记非常规档。
  'board.cyb': { text: '创业板', tone: 'accent' },
  'board.kcb': { text: '科创板', tone: 'accent' },
  'board.bj': { text: '北交所', tone: 'warn' },
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
