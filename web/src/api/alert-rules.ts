/**
 * G3 盯盘价格告警规则读面（M08 引擎评估「active → triggered」，M10 通知出站）。
 * 路由 /api/risk/alert/rules{,/,/edit,/rearm,/delete} 与 risk.proto 的 /risk/v1/* 一一对应；
 * 五个写口一律回全量列表（AlertRulesReply），前端直接整换，不客户端手改本地行。
 */
import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'

export const ALERT_CONDITIONS = ['above', 'below', 'crosses'] as const
export type AlertCondition = (typeof ALERT_CONDITIONS)[number]

/** 与交易开仓条/自选同口径：六位代码 + 交易所后缀（不自动推断后缀，避免 SH/SZ 猜错而永不触发）。 */
export const ALERT_SYMBOL_RE = /^\d{6}\.(SZ|SH|BJ)$/i

export interface AlertRule {
  id: string
  symbol: string
  /** above/below 严格比较（贴线不响）；crosses 现价与上一笔 last_price 分居目标价两侧即响——语义以 pkg/alert.rule.Step() 为准 */
  condition: string
  targetPrice: number | null
  note: string
  /** active 布防中 / triggered 已触发（再响需编辑或重置布防） */
  status: string
  /** crosses 基线价；服务端每次成功评估落库 */
  lastPrice: number | null
  armedAt: string
  triggeredAt: string
  createdAt: string
  updatedAt: string
}

export interface AlertRuleDraft {
  symbol: string
  condition: string
  targetPrice: string
  note?: string
}

export function normalizeAlertRule(raw: unknown): AlertRule | null {
  if (!raw || typeof raw !== 'object') return null
  const r = raw as RawRecord
  const id = pick<string>(r, 'id')
  if (!id) return null
  return {
    id,
    symbol: pick<string>(r, 'symbol') || '',
    condition: pick<string>(r, 'condition') || '',
    targetPrice: num(pick(r, 'targetPrice', 'target_price')),
    note: pick<string>(r, 'note') || '',
    status: pick<string>(r, 'status') || 'active',
    lastPrice: num(pick(r, 'lastPrice', 'last_price')),
    armedAt: pick<string>(r, 'armedAt', 'armed_at') || '',
    triggeredAt: pick<string>(r, 'triggeredAt', 'triggered_at') || '',
    createdAt: pick<string>(r, 'createdAt', 'created_at') || '',
    updatedAt: pick<string>(r, 'updatedAt', 'updated_at') || '',
  }
}

export function normalizeRuleList(data: unknown): AlertRule[] {
  return asList(data, 'rules')
    .map(normalizeAlertRule)
    .filter((r): r is AlertRule => r !== null)
}

export async function listAlertRules(): Promise<AlertRule[]> {
  return normalizeRuleList(await request('/api/risk/alert/rules'))
}

export async function createAlertRule(draft: AlertRuleDraft, operator: string): Promise<AlertRule[]> {
  return normalizeRuleList(await request('/api/risk/alert/rules', {
    method: 'POST',
    json: {
      symbol: draft.symbol.trim().toUpperCase(),
      condition: draft.condition,
      target_price: Number(draft.targetPrice),
      note: draft.note || '',
      operator,
    },
  }))
}

export async function editAlertRule(id: string, draft: AlertRuleDraft, operator: string): Promise<AlertRule[]> {
  return normalizeRuleList(await request('/api/risk/alert/rules/edit', {
    method: 'POST',
    json: {
      id,
      symbol: draft.symbol.trim().toUpperCase(),
      condition: draft.condition,
      target_price: Number(draft.targetPrice),
      note: draft.note || '',
      operator,
    },
  }))
}

export async function rearmAlertRule(id: string, operator: string): Promise<AlertRule[]> {
  return normalizeRuleList(await request('/api/risk/alert/rules/rearm', {
    method: 'POST',
    json: { id, operator },
  }))
}

export async function deleteAlertRule(id: string, operator: string): Promise<AlertRule[]> {
  return normalizeRuleList(await request('/api/risk/alert/rules/delete', {
    method: 'POST',
    json: { id, operator },
  }))
}

/**
 * 草案纯校验（表单与单测共用）：symbol 为「六位.交易所」全口径（大小写不敏感，面板统一大写提交）、
 * condition 三态白名单、目标价有限正数。
 * 返回 '' 为合法，否则为第一处错误文案——与 risk-chain 表单的 error 行同语义。
 * 语义校验（同码同条件重复等）是服务端职责，不前端臆造。
 */
export function validateRuleDraft(draft: AlertRuleDraft): string {
  if (!ALERT_SYMBOL_RE.test(draft.symbol.trim())) return '代码须形如 600519.SH / 000001.SZ'
  if (!(ALERT_CONDITIONS as readonly string[]).includes(draft.condition)) return '条件须为 above / below / crosses 之一'
  const price = Number(draft.targetPrice)
  if (!Number.isFinite(price) || price <= 0) return '目标价须为大于 0 的有限数'
  return ''
}
