import { describe, expect, it } from 'vitest'
import { ActivityLog } from './activity'
import {
  ageShort,
  awaitingSignal,
  cardFreshness,
  cardSilence,
  channelSilence,
  dayTone,
  decorate,
  parseStamp,
  secTone,
  silenceText,
  sourceKind,
  stampDay,
  tradingDaysBetween,
  type MonitorInputLike,
} from './freshness'
import type { SceneGraph, SceneNode } from './types'

/** 2026-10-12 周一 10:00（交易日盘中）；10-17 周六；10-16 周五 */
const MON = Date.parse('2026-10-12T10:00:00')
const SAT = Date.parse('2026-10-17T10:00:00')

function ago(base: number, seconds: number): string {
  const date = new Date(base - seconds * 1000)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function node(id: string, status: SceneNode['status'] = 'live'): SceneNode {
  return { id, ring: 'post', title: id, status, detail: '', rank: 'ambient', face: '', sub: '', tone: 'flat', inputs: [], outputs: [] }
}

function graphOf(...nodes: SceneNode[]): SceneGraph {
  return { nodes, edges: [], pulses: [], news: [], terminals: [], focusId: '', truncated: false }
}

describe('parseStamp / stampDay', () => {
  it('支持 RFC3339、日期时间、当天时钟三种格式', () => {
    expect(parseStamp('2026-10-12T09:30:00+08:00')).not.toBeNull()
    expect(parseStamp(ago(MON, 30))).toBe(MON - 30_000)
    expect(parseStamp('')).toBeNull()
    expect(parseStamp('不是时间')).toBeNull()
    expect(stampDay('2026-10-12 15:00:00')).toBe(20261012)
    expect(stampDay('')).toBeNull()
  })
})

describe('ageShort / silenceText / 色调', () => {
  it('秒、分、时各归各位', () => {
    expect(ageShort(3)).toBe('3s')
    expect(ageShort(300)).toBe('5m')
    expect(ageShort(7200)).toBe('2h')
    expect(silenceText(30)).toBe('30 秒没有新数据')
    expect(silenceText(300)).toBe('5 分钟没有动静')
  })

  it('§6.2 阈值：行情 10s/60s，情绪 60s/300s，账户 30s/180s，模型 60s/600s', () => {
    expect(secTone('tape', 5)).toBe('ok')
    expect(secTone('tape', 30)).toBe('warn')
    expect(secTone('tape', 61)).toBe('danger')
    expect(secTone('sentiment', 60)).toBe('ok')
    expect(secTone('sentiment', 301)).toBe('danger')
    expect(secTone('account', 30)).toBe('ok')
    expect(secTone('account', 181)).toBe('danger')
    expect(secTone('model', 60)).toBe('ok')
    expect(secTone('model', 601)).toBe('danger')
  })

  it('日粒度：当日正常、前一交易日变橙、更早变红', () => {
    expect(dayTone(0)).toBe('ok')
    expect(dayTone(1)).toBe('warn')
    expect(dayTone(2)).toBe('danger')
  })

  it('tradingDaysBetween 只跳周末：周五到周一是 1 个交易日', () => {
    expect(tradingDaysBetween(20261009, 20261012)).toBe(1)
    expect(tradingDaysBetween(20261012, 20261012)).toBe(0)
    expect(tradingDaysBetween(20261008, 20261012)).toBe(2)
  })
})

describe('sourceKind', () => {
  it('通达信/akshare 判行情，东财判情绪，外围判 overseas，其余按盘后', () => {
    expect(sourceKind('tdx', '通达信')).toBe('tape')
    expect(sourceKind('akshare')).toBe('tape')
    expect(sourceKind('eastmoney', '东方财富')).toBe('sentiment')
    expect(sourceKind('global', '外围行情')).toBe('overseas')
    expect(sourceKind('tushare')).toBe('post')
  })
})

describe('cardFreshness', () => {
  it('行情源按 lastAt 算年龄：3 秒前正常、半分钟变橙、一分半变红', () => {
    const input: MonitorInputLike = {
      now: MON,
      sources: { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'up', detail: '', lastAt: ago(MON, 3) }] },
    }
    expect(cardFreshness(input, MON, true).get('source:tdx')).toEqual({ text: '3s 前', tone: 'ok' })
    input.sources = { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'up', detail: '', lastAt: ago(MON, 30) }] }
    expect(cardFreshness(input, MON, true).get('source:tdx')?.tone).toBe('warn')
    input.sources = { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'up', detail: '', lastAt: ago(MON, 90) }] }
    expect(cardFreshness(input, MON, true).get('source:tdx')?.tone).toBe('danger')
  })

  it('没有时间戳的账户牌按本次采样年龄算，接口挂住时年龄自己涨', () => {
    const input: MonitorInputLike = { now: MON - 5_000, sampledAt: MON - 5_000, account: { ok: true } }
    const fresh = cardFreshness(input, MON, true)
    expect(fresh.get('book:center')).toEqual({ text: '采样 5s 前', tone: 'ok' })
    const hung: MonitorInputLike = { now: MON - 200_000, account: { ok: true } }
    expect(cardFreshness(hung, MON, true).get('book:center')?.tone).toBe('danger')
  })

  it('无关 WS 持续活跃也不能刷新 REST 账户、风控和模型统计的角标', () => {
    const input: MonitorInputLike = {
      now: MON,
      sampledAt: MON - 240_000,
      lastPush: MON,
      account: { ok: true },
      risk: { ok: true },
      brainStats: { ok: true },
    }
    const badges = cardFreshness(input, MON, true)
    expect(badges.get('book:center')).toEqual({ text: '采样 4m 前', tone: 'danger' })
    expect(badges.get('post:risk')).toEqual({ text: '采样 4m 前', tone: 'danger' })
    expect(badges.get('model:large')).toEqual({ text: '采样 4m 前', tone: 'warn' })
  })

  it('REST 成功时钟不能给失败或尚未读取的数据块贴上新鲜角标', () => {
    const input: MonitorInputLike = {
      now: MON,
      sampledAt: MON,
      account: { ok: false },
      risk: null,
      brainStats: { ok: false },
      sentiment: { ok: false, asOf: ago(MON, 2), stale: false },
    }
    const badges = cardFreshness(input, MON, true)
    for (const id of ['book:center', 'post:risk', 'model:large', 'post:scan']) expect(badges.has(id)).toBe(false)
    expect(cardFreshness({ sampledAt: 0, now: MON, account: { ok: true } }, MON, true).has('book:center')).toBe(false)
  })

  it('数据自身时间优先于 REST 采样；缺少行情时间不冒充刚读到的新行情', () => {
    const input: MonitorInputLike = { now: MON, sampledAt: MON, sentiment: { ok: true, asOf: ago(MON, 400), stale: false } }
    expect(cardFreshness(input, MON, true).get('post:scan')).toEqual({ text: '6m 前', tone: 'danger' })
    input.sentiment!.asOf = ''
    expect(cardFreshness(input, MON, true).has('post:scan')).toBe(false)
  })

  it('非交易日一律按盘后阈值：周五的数据周六看是「上一交易日」', () => {
    const input: MonitorInputLike = {
      now: SAT,
      sources: { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'up', detail: '', lastAt: '2026-10-16 15:00:00' }] },
    }
    expect(cardFreshness(input, SAT, false).get('source:tdx')).toEqual({ text: '上一交易日', tone: 'warn' })
  })

  it('盘后数据同日正常、更早变红', () => {
    const input: MonitorInputLike = {
      now: MON,
      quality: { ok: true, items: [{ tradeDate: '2026-10-12' }] },
      sources: { ok: true, items: [] },
    }
    expect(cardFreshness(input, MON, true).get('source:quality')?.tone).toBe('ok')
    const old: MonitorInputLike = {
      now: MON,
      quality: { ok: true, items: [{ tradeDate: '2026-10-08' }] },
      sources: { ok: true, items: [] },
    }
    expect(cardFreshness(old, MON, true).get('source:quality')?.tone).toBe('danger')
  })
})

describe('cardSilence', () => {
  it('行情源 30 秒没新数据转 attention，并写出秒数', () => {
    const input: MonitorInputLike = {
      now: MON,
      sources: { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'up', detail: '', lastAt: ago(MON, 45) }] },
    }
    const out = cardSilence(input, { now: MON, trading: true, activity: new ActivityLog() })
    expect(out.get('source:tdx')?.text).toBe('45 秒没有新数据')
    expect(out.get('source:tdx')?.alert).toBe(false)
  })

  it('行情源 30 秒内有新数据就不报', () => {
    const input: MonitorInputLike = {
      now: MON,
      sources: { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'up', detail: '', lastAt: ago(MON, 10) }] },
    }
    expect(cardSilence(input, { now: MON, trading: true, activity: new ActivityLog() }).size).toBe(0)
  })

  it('源本身已经 down 的不再按静默报（不重复报警）', () => {
    const input: MonitorInputLike = {
      now: MON,
      sources: { ok: true, items: [{ id: 'tdx', name: '通达信', status: 'down', detail: '', lastAt: ago(MON, 600) }] },
    }
    expect(cardSilence(input, { now: MON, trading: true, activity: new ActivityLog() }).has('source:tdx')).toBe(false)
  })

  it('扫描：盘中 5 分钟没动静才报，盘后不报', () => {
    const input: MonitorInputLike = { now: MON, sentiment: { ok: true, asOf: ago(MON, 400), stale: false } }
    const out = cardSilence(input, { now: MON, trading: true, activity: new ActivityLog() })
    expect(out.get('post:scan')?.text).toBe('6 分钟没有动静')
    const freshScan: MonitorInputLike = { now: MON, sentiment: { ok: true, asOf: ago(MON, 60), stale: false } }
    expect(cardSilence(freshScan, { now: MON, trading: true, activity: new ActivityLog() }).has('post:scan')).toBe(false)
  })

  it('模型静默要同时满足：有待确认信号、观测满 10 分钟、活动条 10 分钟全黑', () => {
    const input: MonitorInputLike = {
      now: MON,
      signals: { ok: true, items: [{ id: 's1', status: 'risk_checking', time: ago(MON, 700) }] },
    }
    expect(awaitingSignal(input)).toBe(true)
    const young = new ActivityLog()
    young.observe('model:large', 'v1', MON)
    /* 只观测了 30 秒，不下结论 */
    expect(cardSilence(input, { now: MON + 30_000, trading: true, activity: young }).has('model:large')).toBe(false)
    const old = new ActivityLog()
    old.observe('model:large', 'v1', MON - 700_000)
    const out = cardSilence(input, { now: MON, trading: true, activity: old })
    expect(out.get('model:large')?.text).toBe('11 分钟没有动静')
  })

  it('没有待确认信号时模型再安静也不算异常', () => {
    const input: MonitorInputLike = { now: MON, signals: { ok: true, items: [{ id: 's1', status: 'filled', time: ago(MON, 700) }] } }
    const old = new ActivityLog()
    old.observe('model:large', 'v1', MON - 900_000)
    expect(cardSilence(input, { now: MON, trading: true, activity: old }).has('model:large')).toBe(false)
  })
})

describe('channelSilence', () => {
  it('委托挂起超过 60 秒且期间没有新成交才报', () => {
    const log = new ActivityLog()
    log.observe('order:o1', 'pending', MON - 90_000)
    const input: MonitorInputLike = {
      now: MON,
      orders: { ok: true, items: [{ id: 'o1', symbol: '600519', side: 'buy', status: 'pending' }] },
      fills: { ok: true, items: [] },
    }
    expect(channelSilence(input, log, MON, true)?.orderId).toBe('o1')
    /* 最近 30 秒有成交 → 通道是通的，不报 */
    const withFill: MonitorInputLike = {
      ...input,
      fills: { ok: true, items: [{ time: ago(MON, 30) }] },
    }
    expect(channelSilence(withFill, log, MON, true)).toBeNull()
  })

  it('观测不足 60 秒不报；盘后不报', () => {
    const log = new ActivityLog()
    log.observe('order:o1', 'pending', MON - 30_000)
    const input: MonitorInputLike = {
      now: MON,
      orders: { ok: true, items: [{ id: 'o1', symbol: '600519', side: 'buy', status: 'pending' }] },
      fills: { ok: true, items: [] },
    }
    expect(channelSilence(input, log, MON, true)).toBeNull()
    const old = new ActivityLog()
    old.observe('order:o1', 'pending', MON - 90_000)
    expect(channelSilence(input, old, MON, false)).toBeNull()
  })
})

describe('decorate', () => {
  it('静默只升级 live/idle 到 attention 并改写副行', () => {
    const graph = graphOf(node('a', 'live'), node('b', 'down'))
    const silence = new Map([
      ['a', { text: '30 秒没有新数据', alert: false }],
      ['b', { text: '999 秒没有新数据', alert: false }],
    ])
    const out = decorate(graph, new Map(), silence, new Map())
    expect(out.nodes[0].status).toBe('attention')
    expect(out.nodes[0].sub).toBe('30 秒没有新数据')
    /* down 的牌保持 down，不被静默改写 */
    expect(out.nodes[1].status).toBe('down')
    expect(out.nodes[1].sub).toBe('')
  })

  it('角标与活动格贴到牌面，缺的不编', () => {
    const graph = graphOf(node('a'))
    const fresh = new Map([['a', { text: '3s 前', tone: 'ok' as const }]])
    const cells = new Map<string, (boolean | null)[]>([['a', [true, false, null]]])
    const out = decorate(graph, fresh, new Map(), cells)
    expect(out.nodes[0].fresh?.text).toBe('3s 前')
    expect(out.nodes[0].activity).toEqual([true, false, null])
  })
})
