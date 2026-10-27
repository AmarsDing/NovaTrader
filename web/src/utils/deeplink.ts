/**
 * W3b 深链解析器（R-UI-36「每个 blocker 显去哪修并跳得过去」）：readiness deeplink
 * （'/ops#services' 形）→ 工作台面板 / 配置浮层档位。路径→面板唯一登记表——未登记路径
 * 不跳（宁可露码不指错门）；面板内锚只有配置档有精确语义（nt:open-config 事件合同，
 * ConfigCenter 挂点与 workspace/function-index buildSettingsSpecs 同源），其余闸门跳
 * 承载面板即是修复位（目标段落在面板首屏结构化陈列，不伪造段内滚动）。
 */
import { getWorkbench } from '@/workspace/inject'

/** 路径 → 承载面板 id（风控域修复位=风控链面板；数据源健康=数据源面板；交易执行=交易面板）。 */
const PANEL_OF: Record<string, string> = {
  '/ops': 'ops',
  '/risk': 'risk-chain',
  '/trade': 'trade',
  '/datahub': 'datasource-health',
}

/** 「去处理」跳转。返回 false = 未登记路径（不静默跳错门，调用方不补假动作）。 */
export function openDeeplink(deeplink: string): boolean {
  const [path, anchor] = deeplink.split('#')
  if (path === '/config') {
    // 配置浮层事件合同：category 由 ConfigCenter 按 nav 档校验（未知档回落默认分组）。
    window.dispatchEvent(new CustomEvent('nt:open-config', anchor ? { detail: { category: anchor } } : {}))
    return true
  }
  const panel = PANEL_OF[path]
  if (!panel) return false
  getWorkbench()?.openPanel(panel)
  return true
}
