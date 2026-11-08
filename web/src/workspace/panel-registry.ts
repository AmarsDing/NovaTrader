/**
 * 面板注册表：dockview 面板 id → 视图懒加载入口。
 * P3 先把 9 个既有页面注册为面板；P4 的新业务面板追加到这里即可。
 * - singleton：同一 id 只开一个实例，重复打开 = 聚焦；
 * - group：默认落位提示（默认预设用），用户拖走后以持久化布局为准；
 * - shortcut（G2）：彭博式助记符元数据，命令面板 `GP 600519` 语法与
 *   功能卡的唯一数据源。prefix/aliases 全局唯一由 registry-integrity 测试锁定。
 */
import type { Component } from 'vue'

export interface PanelShortcut {
  /** 助记符前缀（大写 2–4 位字母/数字），命令面板首段精确匹配。 */
  prefix: string
  /** 兼容别名（英文/历史叫法），与 prefix 一起全局唯一。 */
  aliases?: string[]
  /** 参数形态：ticker=6 位代码；side=买/卖；none=无参数。 */
  argKind: 'ticker' | 'side' | 'none'
  /** 参数示例（占位符/提示文案用）。 */
  argHint?: string
  /** 功能卡描述：这个面板显示什么、什么时候用（F1 卡片正文）。 */
  description: string
}

export interface PanelDef {
  id: string
  title: string
  /** 图标字（等宽渲染的短标记，P6 换 NtIcon 内联 SVG）。 */
  icon: string
  loader: () => Promise<{ default: Component }>
  group: 'center' | 'left' | 'right' | 'bottom'
  singleton: boolean
  /**
   * U10 核心面板标记：默认屏（desks.DEFAULT_LAYOUT）成员置 true。
   * 本批只落元数据；defaultHidden 等消费点后置，registry-integrity
   * 以「core 集合 === DEFAULT_LAYOUT ids 集合」「数量恒定」双断言锁死。
   */
  core?: boolean
  shortcut?: PanelShortcut
}

/** 允许弹出成独立窗口的面板（order-ticket 类写入面板禁弹，见 M12 P5）。 */
export const POPOUT_ALLOWED = new Set([
  'overview',
  'signals',
  'market',
  'market-board',
  'intel',
  'backtest',
  'limit-ladder',
  'intraday-alerts',
  'quote-sidebar',
  'morning-briefing',
  'emotion-review',
  'brain-monitor',
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
    core: true,
    shortcut: {
      prefix: 'JT',
      argKind: 'none',
      description: '一日作战总览：盘前/盘中/盘后脉冲、缺口提示与各模块快捷入口。',
    },
  },
  {
    id: 'signals',
    title: '信号',
    icon: '信',
    loader: () => import('@/view/SignalView.vue'),
    group: 'right',
    singleton: true,
    core: true,
    shortcut: {
      prefix: 'XG',
      argKind: 'none',
      description: '大模型四维评分产出的标准化交易信号（入场/止损/止盈）与状态流转。',
    },
  },
  {
    id: 'market',
    title: '行情',
    icon: '情',
    loader: () => import('@/view/MarketView.vue'),
    group: 'center',
    singleton: true,
    core: true,
    shortcut: {
      prefix: 'GP',
      aliases: ['HQ'],
      argKind: 'ticker',
      argHint: '600519',
      description: '个股 K 线与盘口行情：GP 600519 直接开图；行情源通达信。',
    },
  },
  {
    id: 'trade',
    title: '交易',
    icon: '易',
    loader: () => import('@/view/TradeView.vue'),
    group: 'bottom',
    singleton: true,
    shortcut: {
      prefix: 'JY',
      argKind: 'side',
      argHint: 'buy|sell',
      description: '下单与持仓：JY buy 预填买单、JY sell 预填卖单（与 B/S 热键同源）。',
    },
  },
  {
    id: 'trade-bar',
    title: '下单条',
    icon: '下',
    loader: () => import('@/panels/trade/trade-bar.panel.vue'),
    group: 'bottom',
    singleton: true,
    core: true,
    shortcut: {
      prefix: 'XD',
      argKind: 'side',
      argHint: 'buy|sell',
      description: '底条恒随的轻量下单：试算 → 提交两段拦截，当前委托/持仓一屏看；全量订单中心走 JY。',
    },
  },
  {
    id: 'intel',
    title: '情报',
    icon: '报',
    loader: () => import('@/view/IntelView.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'QB',
      argKind: 'ticker',
      argHint: '600519',
      description: '个股消息面聚合：公告、新闻与情绪线索，QB 600519 直达个股。',
    },
  },
  {
    id: 'backtest',
    title: '回测',
    icon: '测',
    loader: () => import('@/view/BacktestView.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'HC',
      argKind: 'none',
      description: '策略回测：历史区间绩效、交易明细与参数对比。',
    },
  },
  {
    id: 'ops',
    title: '运维',
    icon: '维',
    loader: () => import('@/view/OpsView.vue'),
    group: 'bottom',
    singleton: true,
    shortcut: {
      prefix: 'YW',
      argKind: 'none',
      description: '平台运维：任务、消费者积压与链路健康观测。',
    },
  },
  {
    id: 'settings',
    title: '配置',
    icon: '设',
    loader: () => import('@/view/SettingsView.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'PZ',
      argKind: 'none',
      description: '应用与外观配置入口（主题、密度、色觉等）。',
    },
  },
  {
    id: 'limit-ladder',
    title: '涨停梯队',
    icon: '停',
    loader: () => import('@/panels/market/limit-ladder.panel.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'DB',
      argKind: 'none',
      description: '连板高度、梯队结构与开板率，打板情绪一屏看全。',
    },
  },
  {
    // TSP 看板信息架构的 NT 重述（docs/NovaTrader/plan/2026-11-08-market-board-implementation-plan.md）。
    // prefix 原计划 KB，与 kb-search 冲突改 MB；非 core（默认屏四件不动）。
    id: 'market-board',
    title: '市场看板',
    icon: '板',
    loader: () => import('@/panels/market/market-board.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'MB',
      argKind: 'none',
      description: '市场全局看板：指数 / 情绪 / 广度 / 梯队 / 板块 / 异动九卡一屏总览。',
    },
  },
  {
    id: 'intraday-alerts',
    title: '异动流',
    icon: '异',
    loader: () => import('@/panels/market/intraday-alerts.panel.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'YD',
      argKind: 'none',
      description: '急拉（3min ≥1.5%）/ 封板 / 开板实时流，支持幅度筛选与候选池聚焦。',
    },
  },
  {
    id: 'quote-sidebar',
    title: '行情侧栏',
    icon: '券',
    loader: () => import('@/panels/market/quote-sidebar.panel.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'QL',
      argKind: 'ticker',
      argHint: '600519',
      description: '单券快照侧栏：报价/五档/概况恒随当前证券，QL 600519 直达。',
    },
  },
  {
    id: 'morning-briefing',
    title: '晨报',
    icon: '晨',
    loader: () => import('@/panels/brain/morning-briefing.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'CB',
      argKind: 'none',
      description: '隔夜外围、要闻摘要与今日观察清单。',
    },
  },
  {
    id: 'emotion-review',
    title: '情绪复盘',
    icon: '复',
    loader: () => import('@/panels/brain/emotion-review.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'ER',
      argKind: 'none',
      description: '收盘定稿读数 / 明日验证对账 / 程序版八格复盘。',
    },
  },
  {
    id: 'brain-monitor',
    title: '模型链路',
    icon: '脑',
    loader: () => import('@/panels/brain/brain-monitor.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'BR',
      argKind: 'none',
      description: '各 agent 调用链、延迟与失败观测。',
    },
  },
  {
    id: 'watchlist',
    title: '自选扫描',
    icon: '选',
    loader: () => import('@/panels/market/watchlist.panel.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'HY',
      argKind: 'none',
      description: '自选池内的信号与异动筛选。',
    },
  },
  {
    id: 'pool-manager',
    title: '股池黑名单',
    icon: '池',
    loader: () => import('@/panels/strategy/pool-manager.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'GC',
      argKind: 'none',
      description: '股池白名单休整、黑名单禁买。',
    },
  },
  {
    id: 'risk-chain',
    title: '风控链',
    icon: '控',
    loader: () => import('@/panels/risk/risk-chain.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'FK',
      argKind: 'none',
      description: 'R 系列规则逐条状态与闸门明细。',
    },
  },
  {
    id: 'risk-report',
    title: '风控日报',
    icon: '风',
    loader: () => import('@/panels/risk/risk-report.panel.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'FR',
      argKind: 'none',
      description: '日度风控事件与处置回顾。',
    },
  },
  {
    id: 'alert-rules',
    title: '告警规则',
    icon: '警',
    loader: () => import('@/panels/risk/alert-rules.panel.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'GJ',
      argKind: 'none',
      description: '价格三态（上破/下破/穿越）的列表、新建编辑与布防重置，附分级静默窗口。',
    },
  },
  {
    id: 'evolution',
    title: '影子进化',
    icon: '化',
    loader: () => import('@/panels/strategy/evolution.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'YJ',
      argKind: 'none',
      description: '策略影子运行的对照与晋级检查。',
    },
  },
  {
    id: 'notify-center',
    title: '通知中心',
    icon: '铃',
    loader: () => import('@/panels/notify/notify-center.panel.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'TZ',
      argKind: 'none',
      description: '告警与系统消息的历史列表。',
    },
  },
  {
    id: 'kb-search',
    title: '知识库',
    icon: '库',
    loader: () => import('@/panels/kb/kb-search.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'KB',
      argKind: 'none',
      description: '文档检索与相似案例回放。',
    },
  },
  {
    id: 'datasource-health',
    title: '数据源',
    icon: '源',
    loader: () => import('@/panels/datahub/datasource-health.panel.vue'),
    group: 'right',
    singleton: true,
    shortcut: {
      prefix: 'DY',
      argKind: 'none',
      description: '通达信 / Tushare / AkShare 等数据源接入状态。',
    },
  },
  {
    // 含 /api/config 凭证写面（token 只写不读），与 settings 同级按禁弹处理——不加入 POPOUT_ALLOWED。
    id: 'datasource-center',
    title: '数据源中心',
    icon: '中',
    loader: () => import('@/panels/datahub/datasource-center.panel.vue'),
    group: 'center',
    singleton: true,
    shortcut: {
      prefix: 'ZY',
      argKind: 'none',
      description: '源墙（域徽章/费用档/凭证）、域降级链排序与健康总览。',
    },
  },
  {
    id: 'audit-trail',
    title: '审计日志',
    icon: '审',
    loader: () => import('@/panels/admin/audit-trail.panel.vue'),
    group: 'bottom',
    singleton: true,
    shortcut: {
      prefix: 'SJ',
      argKind: 'none',
      description: '操作与系统事件留痕。',
    },
  },
]

const byId = new Map(defs.map((d) => [d.id, d]))

export function listPanels(): PanelDef[] {
  return defs
}

export function getPanel(id: string): PanelDef | undefined {
  return byId.get(id)
}
