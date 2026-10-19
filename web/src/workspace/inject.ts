/**
 * 工作台注入契约：DockHost 注册，面板/命令面板/顶栏读取。
 * 用模块级注册表，避免「CommandBar 先于 DockHost setup → inject 永远 undefined」。
 */
import { inject, type InjectionKey } from 'vue'
import type { PanelDef } from './panel-registry'
import type { DeskDef } from './desks'

export interface WorkbenchApi {
  /** 打开（已开则聚焦）面板；params 经 panel-bus 交给面板组件。 */
  openPanel: (id: string, params?: Record<string, unknown>) => void
  /** 当前布局快照（dockview toJSON）。 */
  captureLayout: () => unknown
  /** 套用布局（容错：非法结构回退默认预设）；persist=false 用于启动恢复。 */
  applyLayout: (layout: unknown, opts?: { persist?: boolean; record?: boolean }) => void
  /** 回到默认预设。 */
  resetToDefault: () => void
  /** 套用交易时段桌面包（G6）；未知 id 返回 false。切桌销毁浮窗。 */
  applyDesk: (id: string) => boolean
  /** 撤销上一次程序化整版切换（容量 20）；无可撤销返回 false。 */
  undoLayout: () => boolean
  /** 重做（任何新切换清空重做栈）；无可重做返回 false。 */
  redoLayout: () => boolean
  /** 把面板弹成独立窗口；不允许的 id 静默拒绝。 */
  popoutPanel: (id: string) => void
  /** 当前 dock 内聚焦面板 id（G5 面板上下文发问用；无 dock/无面板时 undefined）。 */
  activePanelId: () => string | undefined
  /** 注册表里的全部面板。 */
  panels: PanelDef[]
  /** 交易时段桌面包（G6）。 */
  desks: DeskDef[]
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
