import { describe, expect, it } from 'vitest'
import { resolveHud } from './hud'
import { hudCells } from './timeband'
import { emptyMonitorInput } from './load-input'
import type { MonitorInput } from './types'

/**
 * HUD 十格解析的测试。fixture 从 emptyMonitorInput 起步逐片覆盖，
 * 断言主词/副行/涨跌色，缺口格必须点名缺的接口（不写 0 不画横线）。
 */
function fixture(): MonitorInput {
  const input = emptyMonitorInput()
  input.sentiment = {
    ok: true,
    phase: '尾盘',
    up: 812,
    down: 4330,
    asOf: new Date(2026, 9, 12, 14, 47).toISOString(),
    stale: false,
    brokenRate: 41.2,
    maxHeight: 7,
  }
  input.limits = {
    ok: true,
    rows: [
      { symbol: 'SZ000001', consecutive: 2, status: 'sealed' },
      { symbol: 'SH600000', consecutive: 7, status: 'sealed' },
    ],
  }
  input.sectors = {
    ok: true,
    rows: [
      { name: '固态电池', avgPct: 3.42, overseas: 1.2, leader: 'SZ000001' },
      { name: '算力', avgPct: 1.1, overseas: null, leader: '' },
    ],
  }
  input.signals = {
    ok: true,
    items: [
      {
        id: 's1',
        symbol: 'SZ000001',
        name: '固态电池龙头',
        score: 91,
        highValue: true,
        degraded: false,
        status: 'risk_checking',
        evidence: [],
        dims: {},
        entry: 12.3,
        stop: 11.8,
        take: 13.9,
        time: '14:40',
      },
      {
        id: 's2',
        symbol: 'SH600000',
        name: '旧信号',
        score: 62,
        highValue: false,
        degraded: false,
        status: 'filled',
        evidence: [],
        dims: {},
        entry: null,
        stop: null,
        take: null,
        time: '10:12',
      },
    ],
  }
  input.candidates = {
    ok: true,
    items: [
      { symbol: 'SZ300001', name: '漏选甲', strategy: 's', missReason: '评分 84 未过线', finalScore: 84 },
      { symbol: 'SZ300002', name: '漏选乙', strategy: 's', missReason: '评分 84 未过线', finalScore: 84 },
      { symbol: 'SZ300003', name: '漏选丙', strategy: 's', missReason: '涨停后才入选', finalScore: 88 },
    ],
  }
  input.risk = { ok: true, mode: 'SIM', killActive: false, killReason: '', buysToday: 2, marketBreaker: '' }
  input.brainStats = {
    ok: true,
    tiers: [
      { tier: 'large', model: '本地 32B', configured: true, circuitOpen: false, calls: 41, failures: 0, p95Ms: 1840 },
    ],
  }
  input.ops = {
    ok: true,
    services: [{ name: 'brain', status: 'ok', detail: '' }],
    sources: [],
    resources: [
      { name: '磁盘 F', status: 'ok', detail: '' },
      { name: '显存 GPU0', status: 'ok', detail: '显存 GPU0 已用 6104 MiB / 共 8188 MiB' },
    ],
  }
  input.account = {
    ok: true,
    book: 'SIM',
    equity: 512340.5,
    cash: 100000,
    dayPnl: -1234.5,
    positions: [
      { symbol: 'SZ000001', quantity: 1000, avgCost: 12.0, lastPrice: 12.3 },
      { symbol: 'SH600000', quantity: 500, avgCost: 9.5, lastPrice: 9.2 },
    ],
  }
  input.briefing = {
    ok: true,
    generatedAt: '2026-10-12 08:35',
    stale: false,
    items: [{ title: '固态电池扩散到设备端', body: '', symbol: '', kind: 'theme' }],
  }
  input.hotWords = { ok: true, items: [{ type: 'theme', target: '固态电池', count: 17 }] }
  input.tasks = {
    ok: true,
    items: [
      { id: 't1', name: '回测 A', status: 'running', message: '' },
      { id: 't2', name: '回测 B', status: 'done', message: '' },
    ],
  }
  input.quality = {
    ok: true,
    items: [
      { domain: 'kline', tradeDate: '2026-10-10', missing: 12, stale: 0, detail: '' },
      { domain: 'tick', tradeDate: '2026-10-10', missing: 3, stale: 0, detail: '' },
    ],
  }
  return input
}

describe('resolveHud · 默认十格', () => {
  const cells = resolveHud(fixture(), hudCells('morning'), Date.UTC(2026, 9, 12, 6, 47))

  it('十格齐：三行结构（标签/主词/副行）+ 主词非空', () => {
    expect(cells).toHaveLength(10)
    for (const cell of cells) {
      expect(cell.label.length).toBeGreaterThan(0)
      expect(cell.face.length).toBeGreaterThan(0)
    }
  })

  it('阶段/涨停/炸板率读情绪与涨停接口', () => {
    expect(cells[0].face).toBe('尾盘')
    expect(cells[0].sub).toBe('涨 812 跌 4330')
    expect(cells[1].face).toBe('2')
    expect(cells[1].sub).toBe('最高 7 板')
    expect(cells[2].face).toBe('41%')
  })

  it('主线取板块第一行，信号数待风控审批的笔数', () => {
    expect(cells[3].face).toBe('固态电池')
    expect(cells[4].face).toBe('1 笔')
    expect(cells[4].sub).toBe('最高 91 分')
  })

  it('漏选取候选数与原因众数', () => {
    expect(cells[5].face).toBe('3')
    expect(cells[5].sub).toBe('评分 84 未过线')
  })

  it('风控/大模型/GPU/当日盈亏', () => {
    expect(cells[6].face).toBe('守着')
    expect(cells[6].sub).toBe('今日买单 2')
    expect(cells[7].face).toBe('41 次')
    expect(cells[7].sub).toContain('P95 1840ms')
    expect(cells[8].face).toBe('75%')
    expect(cells[9].face).toBe('-1,234.50')
    expect(cells[9].tone).toBe('down')
    expect(cells[9].hero).toBe(true)
  })
})

describe('resolveHud · 时段特化格', () => {
  it('盘前三格替换为缺口格与外围格，点名缺的接口', () => {
    const cells = resolveHud(fixture(), hudCells('preopen'), Date.UTC(2026, 9, 12, 6, 47))
    expect(cells[0].key).toBe('gap:perf')
    expect(cells[0].face).toBe('未接入')
    expect(cells[0].sub).toContain('/v1/strategy/performance')
    expect(cells[2].key).toBe('overnight:overseas')
    expect(cells[2].face).toBe('固态电池')
    expect(cells[2].sub).toBe('外围 1.2%')
  })

  it('竞价三格全部点名竞价接口', () => {
    const cells = resolveHud(fixture(), hudCells('auction'), Date.UTC(2026, 9, 12, 6, 47))
    expect(cells.slice(0, 3).map((cell) => cell.key)).toEqual(['gap:auction-amount', 'gap:auction-limit', 'gap:auction-rush'])
    for (const cell of cells.slice(0, 3)) expect(cell.sub).toContain('/v1/market/auction')
  })

  it('午休：半场盈亏红绿、半场涨停数、下午关注取晨报头条', () => {
    const cells = resolveHud(fixture(), hudCells('lunch'), Date.UTC(2026, 9, 12, 6, 47))
    expect(cells[0].face).toBe('-1,234.50')
    expect(cells[0].tone).toBe('down')
    expect(cells[1].face).toBe('2')
    expect(cells[2].face).toBe('固态电池扩散到设备端')
  })

  it('尾盘：持仓浮盈、距止损最近、距收盘倒计时', () => {
    const at = new Date(2026, 9, 12, 14, 47).getTime()
    const cells = resolveHud(fixture(), hudCells('closing'), at)
    /* 浮盈 = (12.3-12.0)*1000 + (9.2-9.5)*500 = 300 - 150 = +150 */
    expect(cells[0].face).toBe('+150.00')
    expect(cells[0].tone).toBe('up')
    /* 距止损：SZ000001 (12.3-11.8)/12.3=4.1%；SH600000 无 stop 对齐 */
    expect(cells[1].face).toBe('4.1%')
    expect(cells[1].sub).toContain('SZ000001')
    expect(cells[2].face).toBe('13 分')
  })

  it('盘后：胜率/归因/相关性三格缺口，点名接口', () => {
    const cells = resolveHud(fixture(), hudCells('postmarket'), Date.UTC(2026, 9, 12, 6, 47))
    expect(cells[0].sub).toContain('/v1/strategy/performance')
    expect(cells[2].sub).toContain('/v1/strategy/score-vs-pnl')
  })

  it('休市：回测任务数、数据补全条数、库体积缺口', () => {
    const cells = resolveHud(fixture(), hudCells('offday'), Date.UTC(2026, 9, 12, 6, 47))
    expect(cells[0].face).toBe('2')
    expect(cells[0].sub).toBe('1 个在跑')
    expect(cells[1].face).toBe('15')
    expect(cells[2].face).toBe('未接入')
    expect(cells[2].sub).toContain('库体积')
  })
})

describe('resolveHud · 空输入降级', () => {
  it('emptyMonitorInput 全部返回「未返回/未接入」而非 0 或横线', () => {
    const cells = resolveHud(emptyMonitorInput(), hudCells('morning'), 0)
    for (const cell of cells) {
      expect(cell.face).not.toBe('0')
      expect(cell.face).not.toBe('-')
      expect(cell.face).not.toBe('')
    }
    expect(cells[8].face).toBe('未接入')
  })

  it('账户在但无持仓：浮盈格说「无持仓」', () => {
    const input = fixture()
    input.account = { ok: true, book: 'SIM', equity: 1, cash: 1, dayPnl: 0, positions: [] }
    const cells = resolveHud(input, hudCells('closing'), new Date(2026, 9, 12, 14, 47).getTime())
    expect(cells[0].face).toBe('无持仓')
    expect(cells[1].face).toBe('无持仓')
  })
})
