/**
 * 配置字段元数据旁挂层（§9.1 字段卡配套，53 会话收窄版）。
 * 本件为旁挂元数据层：不 import、不改动 config-catalog.ts / ConfigCenter.vue（53 会话禁件）。
 * defaultOf 内收 config-catalog 归 53 会话批次，届时由主会话合并（留账）。
 */

/**
 * 已知键默认值种子表（先收限频 / 刷新间隔类数值默认；未收录返回 undefined）。
 * 值与 config-catalog 的 fallback 口径一致：保留字串形态，比对走 JSON.stringify（见 NtConfigField）。
 */
const KNOWN_DEFAULTS: Record<string, unknown> = {
  'app.ds.tushare.qps': '2',
  'app.ds.tushare.timeout_sec': '15',
  'app.ds.eastmoney.qps': '4',
  'app.ds.eastmoney.timeout_sec': '10',
  'app.ds.sina.qps': '8',
  'app.ds.sina.timeout_sec': '5',
  'app.ds.tencent.qps': '8',
  'app.ds.tencent.timeout_sec': '5',
  'app.ds.cninfo.qps': '2',
  'app.ds.cninfo.timeout_sec': '15',
  'app.ds.tdx_tcp.timeout_sec': '8',
  'app.ds.akshare.timeout_sec': '30',
}

/** 字段默认值：未收录返回 undefined（字段卡据此不渲染「已修改」徽注，不编造默认）。 */
export function defaultOf(key: string): unknown {
  return KNOWN_DEFAULTS[key]
}

/**
 * 危险键名单（保存前置「试算 → 影响预览」强制门槛，§9.2）。
 * 注记：名单留空待 53 会话接入 ConfigCenter 时登记；本批只占位导出，不预写键名。
 */
export const DANGER_KEYS: string[] = []

/** 生效范围章口径（§9.1）：默认「保存后生效」；immediate / on-restart 名单归 catalog 批次登记（留账）。 */
export function effectiveScopeOf(_key: string): 'immediate' | 'on-save' | 'on-restart' {
  return 'on-save'
}
