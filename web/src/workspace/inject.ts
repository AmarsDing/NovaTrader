/**
 * 工作台注入契约：DockHost 注册，面板/命令面板/顶栏读取。
 * 用模块级注册表，避免「CommandBar 先于 DockHost setup → inject 永远 undefined」。
 */
import { inject, type InjectionKey } from 'vue'
import type { PanelDef } from './panel-registry'

export interface WorkbenchApi {
  /** 打开（已开则聚焦）面板；params 经 panel-bus 交给面板组件。 */
  openPanel: (id: string, params?: Record<string, unknown>) => void
  /** 当前布局快照（dockview toJSON）。 */
  captureLayout: () => unknown
  /** 套用布局（容错：非法结构回退默认预设）；persist=false 用于启动恢复。 */
  applyLayout: (layout: unknown, opts?: { persist?: boolean }) => void
  /** 回到默认预设。 */
  resetToDefault: () => void
  /** 把面板弹成独立窗口；不允许的 id 静默拒绝。 */
  popoutPanel: (id: string) => void
  /** 注册表里的全部面板。 */
  panels: PanelDef[]
}

export const WorkbenchKey: InjectionKey<WorkbenchApi> = Symbol('nt.workbench')

let registered: WorkbenchApi | undefined

/** DockHost 挂载时注册；卸载时注销。 */
export function registerWorkbench(api: WorkbenchApi): void {
  registered = api
}

export function unregisterWorkbench(api?: WorkbenchApi): void {
  if (!api || registered === api) registered = undefined
}

/** 取当前工作台 API（优先模块注册，兼容 inject）。 */
export function getWorkbench(): WorkbenchApi | undefined {
  return registered ?? inject(WorkbenchKey, undefined)
}

/** @deprecated 等价 getWorkbench；保留旧名以免大面积改调用方。 */
export function useWorkbench(): WorkbenchApi | undefined {
  return getWorkbench()
}
