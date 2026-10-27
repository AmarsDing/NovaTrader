/**
 * 闸门中文名册（W3c 册纪律：NtBlockerCard / NtPreflightChecklist / preflight 工具的唯一出处，
 * 未登记代码原样显——宁可露码也不编名）。state 文案含 ok 档；显隐分工由消费件自定。
 */
export const GATE_LABEL: Record<string, string> = {
  risk_engine: '风险引擎',
  risk_config: '风控配置',
  risk_killswitch: '急停开关',
  sim_admission: '模拟准入',
  broker_gw: '文件单通道',
  datahub_sources: '数据源',
  llm_config: '模型配置',
}

/** 闸门状态文案。「未证实」不冒绿：没取到≠0。 */
export const GATE_STATE_LABEL: Record<string, string> = {
  ok: '已就位',
  warn: '待处理',
  down: '不可用',
  unknown: '未证实',
}
