/**
 * 面板参数总线：openPanel(id, params) 与面板组件之间的传参通道。
 * dockview 的 params 会进布局持久化，路由深链剥回 `/` 后 query 会丢，
 * 所以打开面板时先登记参数，面板组件挂载后按 id 一次性取走。
 */
const pending = new Map<string, Record<string, unknown>>()

export function stagePanelParams(id: string, params: Record<string, unknown>): void {
  if (params && Object.keys(params).length) pending.set(id, { ...params })
}

export function consumePanelParams(id: string): Record<string, unknown> | undefined {
  const params = pending.get(id)
  if (params) pending.delete(id)
  return params
}
