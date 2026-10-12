/**
 * 运行状态的中文职责名与光点归类。
 * 监控驾驶舱底部终端带和详情弹层共用，避免两处各写一份。
 */

export interface DutyInfo {
  name: string
  role: string
}

const DUTY: Record<string, DutyInfo> = {
  admin: { name: '管理网关', role: '登录和接口转发' },
  datahub: { name: '数据采集', role: '行情与公告入库' },
  market: { name: '行情', role: '盘面、涨停、板块' },
  intel: { name: '情报', role: '新闻、晨报、知识库' },
  brain: { name: '智脑', role: '四维评分，不下单' },
  strategy: { name: '策略', role: '筛选候选，生成信号' },
  backtest: { name: '回测', role: '历史检验，不是实盘' },
  risk: { name: '风控进程', role: '规则拦截与急停' },
  trade: { name: '交易通道', role: '东方财富文件单' },
  notify: { name: '通知', role: '把告警送出去' },
  postgres: { name: '数据库', role: '账本和行情落库' },
  redis: { name: '缓存', role: '会话和热数据' },
  nats: { name: '消息总线', role: '服务之间传事件' },
  质量: { name: '数据质量', role: '缺数和过期' },
}

const HEALTHY = ['ok', 'up', 'running', 'healthy', 'ready', 'live']
const BROKEN = ['down', 'error', 'unavailable', 'failed', 'closed', 'dead', 'stopped']

export function dutyOf(title: string): DutyInfo {
  const key = title.trim().toLowerCase()
  if (DUTY[key]) return DUTY[key]
  if (title.startsWith('显存')) return { name: title, role: '模型所在的显卡' }
  if (title.startsWith('磁盘')) return { name: title, role: '本机剩余空间' }
  return { name: title, role: '运行状态' }
}

/** 状态不是健康词就算「要喊出来」，底部光点旁显示名字。 */
export function isLoud(status: string): boolean {
  const key = status.trim().toLowerCase()
  return key !== '' && !HEALTHY.includes(key)
}

/** 服务健康用青/暗/橙/红，与涨跌红绿无关。 */
export function statusDot(status: string): 'is-live' | 'is-idle' | 'is-gap' | 'is-down' | 'is-attention' {
  const key = status.trim().toLowerCase()
  if (HEALTHY.includes(key)) return 'is-live'
  if (BROKEN.includes(key)) return 'is-down'
  if (key === '' || key === 'unknown') return 'is-gap'
  return 'is-attention'
}
