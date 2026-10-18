import { describe, expect, it } from 'vitest'
import { TAPE_MAX_ROWS, TapeSession, auditDomain, buySide, compareRows, rowFromPulse, stampOf, tapeRowsFrom } from './tape'
import { healthTone } from './health'
import type { TapeRow } from './tape'
import type { HealthPart, HealthScore } from './health'
import type { CardSilence } from './freshness'
import type { Pulse } from './types'

/**
 * S4 事件流 TAPE 的测试（监控业务监控方案 §4.6 / §6.1 / §6.3）。
 * 盯四件事：六域归类与标签色键、点行能定位到哪个节点、状态事件不刷屏、40 行上限。
 * 时刻一律用不带时区的「YYYY-MM-DD HH:MM:SS」（按本地解析），CI 换时区也不会翻。
 */

const T_NOTICE = Date.parse('2026-10-12 14:32:07')
const T_PULSE = Date.parse('2026-10-12 14:32:09')
const T_NOW = Date.parse('2026-10-12 14:32:10')

function pulse(id: string, from: string, to: string, kind: Pulse['identifier']['kind'], value: string, at: number): Pulse {
  return { id, from, to, identifier: { kind, value }, at }
}

function health(score: number, deductions: [HealthPart['key'], number][]): HealthScore {
  const labels: Record<HealthPart['key'], string> = {
    fresh: '市场结果时效',
    services: '服务存活',
    model: '模型可用',
    risk: '风控在线',
    channel: '交易通道',
  }
  const map = new Map(deductions)
  const keys: HealthPart['key'][] = ['fresh', 'services', 'model', 'risk', 'channel']
  return {
    score,
    tone: healthTone(score),
    measuredCount: 5,
    totalCount: 5,
    parts: keys.map((key) => ({ key, label: labels[key], deduction: map.get(key) ?? 0, note: '', measured: true })),
  }
}

function silence(ids: string[], text = '10 分钟没有动静'): Map<string, CardSilence> {
  return new Map(ids.map((id) => [id, { text, alert: false }]))
}

describe('tape · 时间与方向', () => {
  it('时间戳解析成 HH:MM:SS，解析不了不编时刻', () => {
    expect(stampOf('2026-10-12 14:32:07').time).toBe('14:32:07')
    expect(stampOf('2026-10-12 14:32:07').at).toBe(T_NOTICE)
    expect(stampOf('14:32:07').time).toBe('14:32:07')
    /* 长串但不可解析：照排原文的时分秒段，排序时刻记 0（沉底），不猜 */
    const weird = stampOf('not-a-date 99:99:99')
    expect(weird.at).toBe(0)
    expect(weird.time).toBe('99:99:99')
    expect(stampOf('').time).toBe('时刻未知')
    expect(stampOf(null).at).toBe(0)
  })

  it('买卖方向：中英文都认，认不出来不算买', () => {
    expect(buySide('buy')).toBe(true)
    expect(buySide('BUY')).toBe(true)
    expect(buySide('买入')).toBe(true)
    expect(buySide('sell')).toBe(false)
    expect(buySide('')).toBe(false)
    expect(buySide(null)).toBe(false)
  })
})

describe('tape · 六域归类', () => {
  it('脉冲四条路径：信号/数据/风控/模型，selectId 恒为终点节点', () => {
    expect(rowFromPulse(pulse('p1', 'post:model', 'post:desk', 'signal', '88', 1000))).toMatchObject({
      domain: 'signal',
      selectId: 'post:desk',
      ident: '88',
    })
    expect(rowFromPulse(pulse('p2', 'service:market', 'post:scan', 'symbol', '600519', 1000))).toMatchObject({
      domain: 'data',
      selectId: 'post:scan',
    })
    expect(rowFromPulse(pulse('p3', 'post:risk', 'book:center', 'rule', 'R01', 1000))).toMatchObject({
      domain: 'risk',
      selectId: 'book:center',
    })
    expect(rowFromPulse(pulse('p4', 'service:brain', 'post:model', 'trace', 'tr-9', 1000))).toMatchObject({
      domain: 'model',
      text: '模型链路',
      selectId: 'post:model',
    })
    expect(rowFromPulse(pulse('p5', 'service:brain', 'post:model', 'symbol', '600519', 1000)).text).toBe('模型调用')
  })

  it('审计按动作与对象归域，归不进专门域的算数据且不可定位', () => {
    expect(auditDomain('kill_switch', 'account')).toMatchObject({ domain: 'risk', selectId: 'post:risk' })
    expect(auditDomain('order_submit', '600519 买入')).toMatchObject({ domain: 'fill', selectId: 'book:center', tone: 'up' })
    expect(auditDomain('fill', '600519 卖出')).toMatchObject({ domain: 'fill', tone: 'down' })
    expect(auditDomain('signal_confirm', 's1')).toMatchObject({ domain: 'signal', selectId: 'post:desk' })
    expect(auditDomain('brain_call', 'large')).toMatchObject({ domain: 'model', selectId: 'post:model' })
    expect(auditDomain('login', 'admin')).toEqual({ domain: 'data', selectId: '', tone: 'flat' })
  })

  it('通知 = 告警域，级别放在标识符列；接口失败不产行', () => {
    const rows = tapeRowsFrom({
      notices: { ok: true, items: [{ id: 'n1', title: '数据源超时', level: 'warn', time: '2026-10-12 14:32:07' }] },
      audit: { ok: false, items: [] },
    })
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ domain: 'alert', text: '数据源超时', ident: 'warn', time: '14:32:07' })
    expect(tapeRowsFrom({ notices: { ok: false, items: [{ id: 'x', title: 'x', level: '', time: '' }] } })).toHaveLength(0)
  })

  it('成交行写方向与量价，红涨绿跌；缺字段写「未返回」不填 0', () => {
    const rows = tapeRowsFrom({
      fills: {
        ok: true,
        items: [
          { id: 'f1', symbol: '600519', side: 'buy', price: 1682, qty: 100, time: '2026-10-12 14:31:52' },
          { id: 'f2', symbol: '000001', side: 'sell', price: null, qty: null, time: '' },
        ],
      },
    })
    expect(rows[0]).toMatchObject({ domain: 'fill', tone: 'up', ident: '600519', selectId: 'book:center', time: '14:31:52' })
    expect(rows[0].text).toBe('买入 100 @ 1,682.00')
    expect(rows[1].tone).toBe('down')
    expect(rows[1].text).toBe('卖出 数量未返回 @ 价格未返回')
    expect(rows[1].time).toBe('时刻未知')
  })

  it('倒序排列，同刻按 id 升序（确定性）', () => {
    const row = (id: string, at: number): TapeRow => ({ id, at, time: '', domain: 'data', text: '', ident: '', selectId: '', tone: 'flat' })
    const sorted = [row('late', 10), row('newest', 20), row('late2', 10)].sort(compareRows)
    expect(sorted.map((item) => item.id)).toEqual(['newest', 'late', 'late2'])
  })
})

describe('TapeSession · 累积与去重', () => {
  it('同一条脉冲反复入帧只记一行', () => {
    const session = new TapeSession()
    const one = [pulse('p1', 'post:model', 'post:desk', 'signal', '88', 1000)]
    expect(session.absorb({ pulses: one }, 1000)).toHaveLength(1)
    expect(session.absorb({ pulses: one }, 2000)).toHaveLength(1)
  })

  it('上限 40 行：多出来的按时间倒序保留最新', () => {
    const session = new TapeSession()
    const many = Array.from({ length: 60 }, (_, index) =>
      pulse(`p${index}`, 'service:market', 'post:scan', 'symbol', `S${index}`, 1000 + index),
    )
    const rows = session.absorb({ pulses: many }, 2000)
    expect(rows).toHaveLength(TAPE_MAX_ROWS)
    expect(rows[0].ident).toBe('S59')
    expect(rows[TAPE_MAX_ROWS - 1].ident).toBe('S20')
  })

  it('§6.3：健康分跌破 70 记一行，必须写扣分项，不写「系统异常」', () => {
    const session = new TapeSession()
    const rows = session.absorb({ health: health(62, [['fresh', 16], ['model', 6]]) }, 5000)
    expect(rows).toHaveLength(1)
    expect(rows[0].domain).toBe('alert')
    expect(rows[0].text).toBe('健康 62：市场结果时效 -16，模型可用 -6')
    expect(rows[0].text).not.toContain('系统异常')
    /* 同一个分数同一组扣分项持续存在 → 不刷屏 */
    expect(session.absorb({ health: health(62, [['fresh', 16], ['model', 6]]) }, 6000)).toHaveLength(1)
  })

  it('健康分变化再记一行；回到 70 以上清零，再跌破是新事件', () => {
    const session = new TapeSession()
    session.absorb({ health: health(62, [['fresh', 16], ['model', 6]]) }, 5000)
    const worse = session.absorb({ health: health(54, [['fresh', 20], ['model', 6]]) }, 6000)
    expect(worse).toHaveLength(2)
    expect(worse[0].text).toBe('健康 54：市场结果时效 -20，模型可用 -6')
    expect(session.absorb({ health: health(96, []) }, 7000)).toHaveLength(2)
    const again = session.absorb({ health: health(68, [['channel', 12]]) }, 8000)
    expect(again).toHaveLength(3)
    expect(again[0].text).toBe('健康 68：交易通道 -12')
  })

  it('缺测没有总分，不生成健康事件，也不能当成恢复后重复触发同一告警', () => {
    const session = new TapeSession()
    const alert = health(62, [['fresh', 16], ['model', 6]])
    const unknown: HealthScore = { ...health(100, []), score: null, tone: 'warn', measuredCount: 0 }
    expect(session.absorb({ health: unknown }, 4000)).toHaveLength(0)
    expect(session.absorb({ health: alert }, 5000)).toHaveLength(1)
    expect(session.absorb({ health: unknown }, 6000)).toHaveLength(1)
    expect(session.absorb({ health: alert }, 7000)).toHaveLength(1)
  })

  it('§6.1：只有模型域的静默进流，同一静默期只记一行', () => {
    const session = new TapeSession()
    const rows = session.absorb({ silence: silence(['model:large', 'source:tdx', 'post:scan']) }, 9000)
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ domain: 'model', selectId: 'model:large', ident: 'large' })
    expect(rows[0].text).toBe('静默 10 分钟没有动静')
    expect(session.absorb({ silence: silence(['model:large']) }, 10_000)).toHaveLength(1)
  })

  it('静默解除后再次静默 = 新的一行', () => {
    const session = new TapeSession()
    session.absorb({ silence: silence(['model:small']) }, 9000)
    expect(session.absorb({ silence: new Map() }, 10_000)).toHaveLength(1)
    expect(session.absorb({ silence: silence(['model:small']) }, 11_000)).toHaveLength(2)
  })

  it('§6.1：WS 断开与恢复各记一行，首帧只记基线', () => {
    const session = new TapeSession()
    expect(session.absorb({ live: true }, 1000)).toHaveLength(0)
    expect(session.absorb({ live: true }, 2000)).toHaveLength(0)
    const down = session.absorb({ live: false }, 3000)
    expect(down).toHaveLength(1)
    expect(down[0]).toMatchObject({ domain: 'data', text: 'WS 断开，页面不是实时' })
    expect(session.absorb({ live: false }, 4000)).toHaveLength(1)
    const up = session.absorb({ live: true }, 5000)
    expect(up).toHaveLength(2)
    expect(up[0].text).toBe('WS 恢复，推送回到实时')
  })

  it('live 缺省 = 这一帧没观测，不当作断开', () => {
    const session = new TapeSession()
    session.absorb({ live: true }, 1000)
    expect(session.absorb({}, 2000)).toHaveLength(0)
  })

  it('事件与轮询行合流后按时间倒序，通知行不会因为反复轮询而重复', () => {
    const session = new TapeSession()
    const snapshot = {
      notices: { ok: true, items: [{ id: 'n1', title: '数据源超时', level: 'warn', time: '2026-10-12 14:32:07' }] },
      pulses: [pulse('p1', 'post:model', 'post:desk', 'signal', '88', T_PULSE)],
    }
    const first = session.absorb(snapshot, T_NOW)
    expect(first.map((row) => row.id)).toEqual(['p:p1', 'n:n1'])
    expect(first[1].at).toBe(T_NOTICE)
    const second = session.absorb(snapshot, T_NOW + 10_000)
    expect(second).toHaveLength(2)
  })

  it('会话累积超上限时挤掉最旧的事件，视图仍只看 40 行', () => {
    const session = new TapeSession(TAPE_MAX_ROWS)
    for (let round = 0; round < 4; round += 1) {
      const batch = Array.from({ length: 20 }, (_, index) =>
        pulse(`r${round}-${index}`, 'service:market', 'post:scan', 'symbol', `S${round}-${index}`, round * 1000 + index),
      )
      session.absorb({ pulses: batch }, round * 1000 + 500)
    }
    const rows = session.absorb({ pulses: [pulse('last', 'post:risk', 'book:center', 'rule', 'R09', 99_999)] }, 100_000)
    expect(rows).toHaveLength(TAPE_MAX_ROWS)
    expect(rows[0].ident).toBe('R09')
    /* 最旧那批已经出流，不会在后续帧里复活 */
    expect(rows.some((row) => row.ident === 'S0-0')).toBe(false)
  })
})
