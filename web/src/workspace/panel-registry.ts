/**
 * 面板注册表：dockview 面板 id → 视图懒加载入口。
 * P3 先把 9 个既有页面注册为面板；P4 的新业务面板追加到这里即可。
 * - singleton：同一 id 只开一个实例，重复打开 = 聚焦；
 * - group：默认落位提示（默认预设用），用户拖走后以持久化布局为准。
 */
import type { Component } from 'vue'

export interface PanelDef {
  id: string
  title: string
  /** 图标字（等宽渲染的短标记，P6 换 NtIcon 内联 SVG）。 */
  icon: string
  loader: () => Promise<{ default: Component }>
  group: 'center' | 'left' | 'right' | 'bottom'
  singleton: boolean
}

const defs: PanelDef[] = [
  { id: 'overview', title: '总览', icon: '览', loader: () => import('@/view/OverviewView.vue'), group: 'center', singleton: true },
  { id: 'signals', title: '信号', icon: '信', loader: () => import('@/view/SignalView.vue'), group: 'right', singleton: true },
  { id: 'market', title: '行情', icon: '情', loader: () => import('@/view/MarketView.vue'), group: 'center', singleton: true },
  { id: 'trade', title: '交易', icon: '易', loader: () => import('@/view/TradeView.vue'), group: 'bottom', singleton: true },
  { id: 'intel', title: '情报', icon: '报', loader: () => import('@/view/IntelView.vue'), group: 'right', singleton: true },
  { id: 'backtest', title: '回测', icon: '测', loader: () => import('@/view/BacktestView.vue'), group: 'center', singleton: true },
  { id: 'ops', title: '运维', icon: '维', loader: () => import('@/view/OpsView.vue'), group: 'bottom', singleton: true },
  { id: 'settings', title: '设置', icon: '设', loader: () => import('@/view/SettingsView.vue'), group: 'right', singleton: true },
]

const byId = new Map(defs.map((d) => [d.id, d]))

export function listPanels(): PanelDef[] {
  return defs
}

export function getPanel(id: string): PanelDef | undefined {
  return byId.get(id)
}
