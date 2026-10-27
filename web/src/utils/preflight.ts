/**
 * W3c 启用模态的前置检查构造器（R-UI-30/36：清单式前置检查 + 逐项状态，条件不齐禁提交）。
 * 闸清单按「指定闸缺报也成一行未证实」生长——没取到≠0，缺报同锁/同提示，不静默当就位。
 */
import type { ReadinessGate } from '@/api/ops'
import { GATE_LABEL } from '@/utils/gate-labels'

/** 清单行合同（唯一出处在此，display 件汇入复用不重复定义）。 */
export interface PreflightItem {
  key: string
  label: string
  state: 'ok' | 'warn' | 'down' | 'unknown'
  /** false=提示项（不锁提交）；缺省 true=必堵。 */
  blocking?: boolean
  why?: string
  deeplink?: string
  progress?: { done: number; total: number }
}

export interface GateSpec {
  /** 必堵项：任一非 ok 锁死提交（fail-closed）。 */
  blocking: string[]
  /** 提示项：只呈现状态不锁提交。 */
  advisory: string[]
}

/**
 * 计划授权（策略启用）闸谱：SIM 条件自动=自动执行链路，风控三闸必堵；
 * 数据源/模型仅提示——扫描链断了扫描必空跑，但不伤现状仓位，交用户按提示处理。
 */
export const PLAN_AUTH_GATES: GateSpec = {
  blocking: ['risk_engine', 'risk_config', 'risk_killswitch'],
  advisory: ['datahub_sources', 'llm_config'],
}

/**
 * 实盘总闸的申请提示闸谱：授权只校验准入证据（服务端 fail-closed），执行链三闸仅提示。
 * 文件单通道不摆在这里：通道真相行取自 admission 信封（同页旗标），双源行并行只会互相矛盾
 * ——broker_gw 就绪口行专供交易条的下单前置卡。
 */
export const LIVE_GATE_ADVISORY: GateSpec = { blocking: [], advisory: ['risk_engine', 'risk_config', 'risk_killswitch'] }

export function gateItems(gates: ReadinessGate[], spec: GateSpec): PreflightItem[] {
  const mk = (gate: string, blocking: boolean): PreflightItem => {
    const hit = gates.find((g) => g.gate === gate)
    if (!hit) return { key: gate, label: GATE_LABEL[gate] || gate, state: 'unknown', blocking, why: '就绪口未回该闸门状态——按未证实处理' }
    return { key: gate, label: GATE_LABEL[gate] || gate, state: hit.state, blocking, why: hit.why, deeplink: hit.deeplink, progress: hit.progress }
  }
  return [...spec.blocking.map((g) => mk(g, true)), ...spec.advisory.map((g) => mk(g, false))]
}

/** 必堵项里仍未就位的行——宿主锁提交与清单汇总行共用此判定式（防双口径漂移）；提示项不参与。 */
export function blockingGaps(items: PreflightItem[]): PreflightItem[] {
  return items.filter((i) => i.blocking !== false && i.state !== 'ok')
}
