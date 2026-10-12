/**
 * 面板参数总线：openPanel(id, params) 与面板组件之间的传参通道。
 * dockview 的 params 会进布局持久化，路由深链剥回 `/` 后 query 会丢，
 * 所以打开面板时先登记参数，面板组件挂载后按 id 一次性取走。
 * 面板已挂载（已有订阅者）时参数立即推给订阅者，避免「只切页签、
 * 参数滞留到下次挂载」的旧缺陷。
 */
type ParamsHandler = (params: Record<string, unknown>) => void

const pending = new Map<string, Record<string, unknown>>()
const subscribers = new Map<string, Set<ParamsHandler>>()

export function stagePanelParams(id: string, params: Record<string, unknown>): void {
  if (!params || !Object.keys(params).length) return
  const subs = subscribers.get(id)
  if (subs && subs.size > 0) {
    for (const handler of subs) handler({ ...params })
    return
  }
  pending.set(id, { ...params })
}

export function consumePanelParams(id: string): Record<string, unknown> | undefined {
  const params = pending.get(id)
  if (params) pending.delete(id)
  return params
}

/** 面板挂载后调用并保存返回值以便卸载时退订；已挂载面板的 openPanel 参数会立即推给 handler。 */
export function onPanelParams(id: string, handler: ParamsHandler): () => void {
  let set = subscribers.get(id)
  if (!set) {
    set = new Set()
    subscribers.set(id, set)
  }
  set.add(handler)
  return () => {
    set.delete(handler)
    if (!set.size) subscribers.delete(id)
  }
}
