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

/** 允许弹出成独立窗口的面板（order-ticket 类写入面板禁弹，见 M12 P5）。 */
export const POPOUT_ALLOWED = new Set([
  'overview',
  'signals',
  'market',
  'intel',
  'backtest',
  'limit-ladder',
  'morning-briefing',
  'brain-monitor',
  'model-stats',
  'watchlist',
  'pool-manager',
  'risk-chain',
  'risk-report',
  'evolution',
  'notify-center',
  'kb-search',
  'datasource-health',
  'audit-trail',
])

export function canPopout(id: string): boolean {
  return POPOUT_ALLOWED.has(id)
}

const defs: PanelDef[] = [
  {
    id: 'overview',
    title: '决策台',
    icon: '策',
    loader: () => import('@/view/OverviewView.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'signals',
    title: '信号',
    icon: '信',
    loader: () => import('@/view/SignalView.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'market',
    title: '行情',
    icon: '情',
    loader: () => import('@/view/MarketView.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'trade',
    title: '交易',
    icon: '易',
    loader: () => import('@/view/TradeView.vue'),
    group: 'bottom',
    singleton: true,
  },
  {
    id: 'intel',
    title: '情报',
    icon: '报',
    loader: () => import('@/view/IntelView.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'backtest',
    title: '回测',
    icon: '测',
    loader: () => import('@/view/BacktestView.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'ops',
    title: '运维',
    icon: '维',
    loader: () => import('@/view/OpsView.vue'),
    group: 'bottom',
    singleton: true,
  },
  {
    id: 'settings',
    title: '配置',
    icon: '设',
    loader: () => import('@/view/SettingsView.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'limit-ladder',
    title: '涨停梯队',
    icon: '停',
    loader: () => import('@/panels/market/limit-ladder.panel.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'morning-briefing',
    title: '晨报',
    icon: '晨',
    loader: () => import('@/panels/brain/morning-briefing.panel.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'brain-monitor',
    title: '模型链路',
    icon: '脑',
    loader: () => import('@/panels/brain/brain-monitor.panel.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'model-stats',
    title: '模型统计',
    icon: '模',
    loader: () => import('@/panels/brain/model-stats.panel.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'watchlist',
    title: '自选扫描',
    icon: '选',
    loader: () => import('@/panels/market/watchlist.panel.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'pool-manager',
    title: '股池黑名单',
    icon: '池',
    loader: () => import('@/panels/strategy/pool-manager.panel.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'risk-chain',
    title: '风控链',
    icon: '控',
    loader: () => import('@/panels/risk/risk-chain.panel.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'risk-report',
    title: '风控日报',
    icon: '风',
    loader: () => import('@/panels/risk/risk-report.panel.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'evolution',
    title: '影子进化',
    icon: '化',
    loader: () => import('@/panels/strategy/evolution.panel.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'notify-center',
    title: '通知中心',
    icon: '铃',
    loader: () => import('@/panels/notify/notify-center.panel.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'kb-search',
    title: '知识库',
    icon: '库',
    loader: () => import('@/panels/kb/kb-search.panel.vue'),
    group: 'center',
    singleton: true,
  },
  {
    id: 'datasource-health',
    title: '数据源',
    icon: '源',
    loader: () => import('@/panels/datahub/datasource-health.panel.vue'),
    group: 'right',
    singleton: true,
  },
  {
    id: 'audit-trail',
    title: '审计日志',
    icon: '审',
    loader: () => import('@/panels/admin/audit-trail.panel.vue'),
    group: 'bottom',
    singleton: true,
  },
]

const byId = new Map(defs.map((d) => [d.id, d]))

export function listPanels(): PanelDef[] {
  return defs
}

export function getPanel(id: string): PanelDef | undefined {
  return byId.get(id)
}
