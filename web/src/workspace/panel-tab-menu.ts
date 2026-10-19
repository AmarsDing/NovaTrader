/**
 * G2 面板菜单：dockview tab 上下文菜单项纯函数（宿主交给 createDockview 的 getTabContextMenuItems）。
 * 需求根：对标提升方案 G2「F1 功能卡……入口在面板菜单」。形态纪律：
 * - 「功能卡（F1）」派发 nt:open-help（detail.panelId）——与 CommandPalette「帮助 <助记符>」入口
 *   同一事件通道，接收端唯一（WorkbenchView → FunctionIndexOverlay），本文件不引入任何新接线；
 * - 「弹出为独立窗口」刻意不用内建 'popout'：内建项直调 addPopoutGroup 会绕开 POPOUT_ALLOWED
 *   白名单；本项 disabled 走 canPopout，action 仍走 workbench.popoutPanel 统一把关（与 PanelTab ⧉ 同入口）；
 * - 「关闭面板」刻意不用内建 'close'：内建项标签渲染英文（Maximize/Restore/Close 一族，
 *   main.esm.mjs buildMaximizeItem 同族），中文菜单不夹英文词；自配项语义同 panel.api.close()（与 PanelTab × 一致）。
 * 对齐 command-grammar.ts 的纯层纪律：返回的只是菜单数据，副作用全部收在 action 闭包内；
 * 菜单 DOM/定位/Esc 由 dockview 自营弹出层管理（不进 useOverlay 栈）。
 */
import type { ContextMenuItem, GetTabContextMenuItemsParams } from 'dockview-core'
import { getWorkbench } from './inject'
import { canPopout } from './panel-registry'

/** tab 右键/长按菜单：功能卡 → 弹出（白名单把关）→ 关闭。 */
export function buildPanelTabMenu(params: GetTabContextMenuItemsParams): ContextMenuItem[] {
  const panelId = params.panel.id
  return [
    {
      label: '功能卡（F1）',
      action: () => window.dispatchEvent(new CustomEvent('nt:open-help', { detail: { panelId } })),
    },
    'separator',
    {
      label: '弹出为独立窗口',
      disabled: !canPopout(panelId),
      action: () => void getWorkbench()?.popoutPanel(panelId),
    },
    'separator',
    { label: '关闭面板', action: () => params.panel.api.close() },
  ]
}
