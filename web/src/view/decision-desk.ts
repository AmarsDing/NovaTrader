/**
 * 决策台用的纯函数：缺口判断。
 * 界面只负责取数和排版，规则放在这里方便单测。
 * 策略名 / 漏选原因的中文口径集中在 utils/format.ts，这里只做转发，保持旧引用不破。
 */

export { strategyLabel, missLabel } from '@/utils/format'

export const PAPER_DAYS_REQUIRED = 20

export type GapLevel = 'block' | 'warn'

export interface DeskGap {
  id: string
  level: GapLevel
  text: string
  /** 点进去看细节的面板 id。 */
  panel: string
}

export interface DeskInput {
  sourcesFailed: boolean
  sourcesEmpty: boolean
  sourcesBlocked: number
  noTape: boolean
  briefingMissing: boolean
  briefingStale: boolean
  anyDegraded: boolean
  paperDays: number | null
  killActive: boolean
  live: boolean
}

/** 断路器打开、源不可用、明确报错，都算这条源现在不能用。 */
export function sourceBlocked(status: string): boolean {
  const state = status.toLowerCase()
  return state === 'open' || state === 'unavailable' || state.includes('down') || state.includes('error')
}

export function countBlockedSources(rows: { enabled: boolean; status: string }[]): number {
  return rows.filter((row) => row.enabled && sourceBlocked(row.status)).length
}

/**
 * 按阻断程度排列。没有缺口时返回空数组，界面显示「链路齐」。
 * 模拟盘天数未知不算缺口：账户服务没通时，上面的数据源/快照会先说话。
 */
export function buildGaps(input: DeskInput): DeskGap[] {
  const gaps: DeskGap[] = []
  if (input.killActive) {
    gaps.push({
      id: 'kill',
      level: 'block',
      text: '急停已触发，买入不会执行',
      panel: 'risk-chain',
    })
  }
  if (input.sourcesFailed) {
    gaps.push({
      id: 'sources',
      level: 'block',
      text: '数据源服务没有应答，行情和新闻都不可信',
      panel: 'datasource-health',
    })
  } else if (input.sourcesEmpty) {
    gaps.push({
      id: 'sources-empty',
      level: 'warn',
      text: '没有登记数据源',
      panel: 'datasource-health',
    })
  } else if (input.sourcesBlocked > 0) {
    gaps.push({
      id: 'sources-blocked',
      level: 'warn',
      text: `${input.sourcesBlocked} 个数据源不可用。通达信插件、Tushare 或新闻令牌没接上时，盘面和情报都是空的`,
      panel: 'datasource-health',
    })
  }
  if (input.noTape) {
    gaps.push({
      id: 'tape',
      level: 'block',
      text: '全市场快照未入库，盘中选股没有价格',
      panel: 'datasource-health',
    })
  }
  if (!input.live) {
    gaps.push({
      id: 'live',
      level: 'warn',
      text: '行情通道不是实时，屏上数字停在上次推送',
      panel: 'ops',
    })
  }
  if (input.briefingMissing) {
    gaps.push({
      id: 'brief',
      level: 'warn',
      text: '今日晨报没有生成。模型地址未配时只有降级晨报，并且要到 08:30',
      panel: 'morning-briefing',
    })
  } else if (input.briefingStale) {
    gaps.push({
      id: 'brief-stale',
      level: 'warn',
      text: '晨报已过期，不要拿它当今天的结论',
      panel: 'morning-briefing',
    })
  }
  if (input.anyDegraded) {
    gaps.push({
      id: 'ai',
      level: 'warn',
      text: '信号走规则分。四维模型未在线，消息面和资金面没有复核',
      panel: 'brain-monitor',
    })
  }
  if (input.paperDays != null && input.paperDays < PAPER_DAYS_REQUIRED) {
    gaps.push({
      id: 'paper',
      level: 'warn',
      text: `模拟盘 ${input.paperDays}/${PAPER_DAYS_REQUIRED} 个交易日，未满不准实盘`,
      panel: 'trade',
    })
  }
  return gaps
}
