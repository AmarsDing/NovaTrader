/**
 * 工作台注入契约：DockHost provide，面板/命令面板 inject。
 * 面板组件不直接依赖 dockview，只依赖这里的类型。
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
  /** 注册表里的全部面板。 */
  panels: PanelDef[]
}

export const WorkbenchKey: InjectionKey<WorkbenchApi> = Symbol('nt.workbench')

export function useWorkbench(): WorkbenchApi | undefined {
  return inject(WorkbenchKey)
}
