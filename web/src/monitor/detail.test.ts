/**
 * S5 详情窗字段深化（detail.ts）的测试。
 * 纪律断言：缺口必须点名缺的接口、不出现 0 填充与破折号、红涨绿跌只给金额/涨跌幅、
 * 复盘只做事实陈述不假设收益。
 */
import { describe, expect, it } from 'vitest'
import {
  RULES,
  buildDetail,
  detailKindFor,
  equityStats,
  fillVwap,
  intelIdsOf,
  ringTone,
  scoreHistogram,
  slippageBp,
} from './detail'
import { emptyMonitorInput } from './load-input'
import type { MonitorInput } from './types'

const NOW = new Date(2026, 9, 13, 10, 30).getTime()

function fixture(): MonitorInput {
  const input = emptyMonitorInput()
  input.now = NOW
  input.account = {
    ok: true,
    book: 'SIM',
    equity: 100000,
    cash: 40000,
    dayPnl: null,
    realized: 1200,
    unrealized: -300,
    positions: [
      { symbol: '600519', quantity: 100, avgCost: 10, lastPrice: 10.5 },
      { symbol: '000001', quantity: 1000, avgCost: 11, lastPrice: 10.8 },
    ],
  }
  input.signals = {
    ok: true,
    items: [
      {
        id: 's1',
        symbol: '600519',
        name: '贵州茅台',
        score: 88,
        highValue: true,
        degraded: false,
        status: 'risk_checking',
        evidence: ['F:ma20=+2.3%', 'N:123', 'R07'],
        dims: { technical: 78, news: 62 },
        entry: 10.4,
        stop: 10.2,
        take: 11.0,
        time: '10:12',
        ruleScore: 72,
        aiScore: 88,
        reason: '首板弱转强',
        entryLow: 10.3,
        entryHigh: 10.5,
        atr: 0.21,
        positionPct: 0.08,
        validUntil: new Date(NOW + 30 * 60000).toISOString(),
        holdingDays: 3,
      },
    ],
  }
  input.risk = {
    ok: true,
    mode: 'SIM',
    killActive: false,
    killReason: '',
    buysToday: 8,
    marketBreaker: '',
    params: { 'risk.max_daily_buys': '10', 'risk.max_single_pct': '0.05', 'risk.max_total_pct': '0.8', 'risk.daily_loss_pct': '0.02', 'risk.stop_pct': '0.05' },
  }
  input.riskReport = {
    ok: true,
    date: '2026-10-13',
    summary: '拦截 2',
    rejects: [
      { rule: 'R10', name: '当日开仓笔数', count: 2 },
      { rule: 'R03', name: '可交易', count: 1 },
    ],
    events: [{ kind: 'kill_switch', active: true, mode: 'SIM', source: 'manual', reason: '手动急停', operator: 'admin', at: '2026-10-13 09:45' }],
  }
  input.orders = {
    ok: true,
    items: [
      { id: 'o1', symbol: '600519', side: 'buy', status: 'FILLED', price: 10.4, volume: 100, filled: 100, reason: 'signal:s1' },
      { id: 'o2', symbol: '000001', side: 'sell', status: 'PARTIAL', price: 11.0, volume: 1000, filled: 500, reason: '' },
    ],
  }
  input.fills = {
    ok: true,
    items: [
      { id: 'o1', symbol: '600519', side: 'buy', price: 10.42, qty: 60, time: '10:20:01' },
      { id: 'o1', symbol: '600519', side: 'buy', price: 10.44, qty: 40, time: '10:20:03' },
      { id: 'o2', symbol: '000001', side: 'sell', price: 10.98, qty: 500, time: '10:21:11' },
    ],
  }
  input.brainStats = {
    ok: true,
    tiers: [
      { tier: 'large', model: 'qwen-32b', configured: true, circuitOpen: false, calls: 41, failures: 1, p50Ms: 900, p95Ms: 1840, inFlight: 2, queued: 0, kvCacheUsage: 63 },
      { tier: 'small', model: 'qwen-4b', configured: true, circuitOpen: true, calls: 214, failures: 9, p50Ms: 40, p95Ms: 86, inFlight: 0, queued: 3, kvCacheUsage: 41 },
    ],
  }
  input.sources = {
    ok: true,
    items: [
      {
        id: 'tdx',
        name: '通达信',
        status: 'live',
        detail: '成功率 98%',
        lastAt: new Date(NOW - 5000).toISOString(),
        kind: 'daily_bar',
        enabled: true,
        successRate: 0.98,
        successCount: 490,
        failureCount: 10,
        latencyMs: 120,
        avgLatencyMs: 210,
        consecutiveFailures: 0,
        quotaLimit: 0,
        lastError: '',
      },
    ],
  }
  input.quality = {
    ok: true,
    items: [
      { id: 'q1', domain: 'daily_bar', checkName: 'daily_bar_missing', symbol: '000001', tradeDate: '2026-10-13', severity: 'high', source: 'tdx', otherSource: '', message: '缺当日日线', hits: 12, lastSeen: '2026-10-13 10:00' },
      { id: 'q2', domain: 'daily_bar', checkName: 'tick_stale', symbol: '600519', tradeDate: '2026-10-13', severity: 'medium', source: 'tdx', otherSource: '', message: '快照过期', hits: 3, lastSeen: '2026-10-13 10:05' },
    ],
  }
  input.candidates = {
    ok: true,
    items: [
      { symbol: '600519', name: '贵州茅台', strategy: 'limit_up', stage: 'selected', missReason: '', ruleScore: 86, aiScore: 91, finalScore: 88 },
      { symbol: '300750', name: '宁德时代', strategy: 'limit_up', stage: 'ai_scored', missReason: 'below_threshold', ruleScore: 72, aiScore: 81, finalScore: 76, retT1: 0.061, hasT1: true },
      { symbol: '600036', name: '招商银行', strategy: 'limit_up', stage: 'ranked', missReason: 'not_in_ai_top', ruleScore: 66, aiScore: null, finalScore: null },
    ],
  }
  input.limits = { ok: true, rows: [{ symbol: '300750', consecutive: 1, status: 'sealed' }] }
  input.admission = { ok: true, ready: false, tradingDays: 12, requiredDays: 20, items: [{ label: '胜率达标', status: '未达标', detail: '52% < 55%' }] }
  return input
}

describe('detailKindFor · 六张牌的归属', () => {
  const input = fixture()
  it('账户/风控/扫描/模型按节点 id 命中', () => {
    expect(detailKindFor(input, 'book:center')).toBe('account')
    expect(detailKindFor(input, 'post:risk')).toBe('risk')
    expect(detailKindFor(input, 'post:scan')).toBe('scan')
    expect(detailKindFor(input, 'post:miss')).toBe('scan')
    expect(detailKindFor(input, 'model:large')).toBe('model')
    expect(detailKindFor(input, 'post:model')).toBe('model')
    expect(detailKindFor(input, 'post:desk')).toBe('signal')
  })
  it('有信号的 symbol 牌才是信号牌；数据源按 slug 匹配', () => {
    expect(detailKindFor(input, 'symbol:600519')).toBe('signal')
    expect(detailKindFor(input, 'symbol:999999')).toBeNull()
    expect(detailKindFor(input, 'source:tdx')).toBe('source')
    expect(detailKindFor(input, 'source:quality')).toBeNull()
    expect(detailKindFor(input, 'book:tape')).toBeNull()
  })
})

describe('§5.1 账户牌', () => {
  const panel = buildDetail(fixture(), 'book:center', NOW, { equityPoints: [100000, 101000, 99000, 100500] })
  const titles = panel?.sections.map((s) => s.title)
  it('六节齐：总览/日内曲线/持仓/委托/成交/绩效', () => {
    expect(titles).toEqual(['总览', '日内曲线', '持仓', '委托', '成交', '绩效'])
  })
  it('当日盈亏缺口点名 trade day_pnl，不写 0', () => {
    const row = panel?.sections[0].rows?.find((r) => r.key === 'daypnl')
    expect(row?.value).toContain('未返回')
    expect(row?.value).toContain('day_pnl')
  })
  it('已实现/浮动标明累计口径', () => {
    const realized = panel?.sections[0].rows?.find((r) => r.key === 'realized')
    expect(realized?.note).toContain('累计')
    expect(realized?.note).toContain('RollDay')
  })
  it('日内回撤来自前端采样：101000→99000 = 1.98%，基准写缺口', () => {
    const curve = panel?.sections[1]
    const dd = curve?.rows?.find((r) => r.key === 'dd')
    expect(dd?.value).toBe('1.98%')
    expect(curve?.foot).toContain('仅本次会话')
    expect(curve?.rows?.find((r) => r.key === 'bench')?.value).toContain('未接入')
  })
  it('持仓表带距止损/距止盈/持有天数，今日高低仍写未接入（不用现价冒充）', () => {
    const table = panel?.sections[2].table
    expect(table?.columns).toContain('距止损')
    const row = table?.rows[0]
    // (10.5-10.2)/10.5 = 2.857% → 2.9%
    expect(row?.[8]).toBe('2.9%')
    expect(row?.[9]).toBe('4.8%')
    expect(row?.[7]).toBe('3 天')
    expect(row?.[10]).toContain('未接入')
  })
  it('距止损 <2% 整行 warn、已触发 danger', () => {
    const input = fixture()
    input.signals!.items[0].stop = 10.45 // (10.5-10.45)/10.5 = 0.48% < 2%
    const warn = buildDetail(input, 'book:center', NOW)?.sections[2].table?.flags?.[0]
    expect(warn).toBe('warn')
    input.signals!.items[0].stop = 10.6 // 已跌破
    const danger = buildDetail(input, 'book:center', NOW)?.sections[2].table?.flags?.[0]
    expect(danger).toBe('danger')
  })
  it('滑点按 fills 精确配对：买入正为劣、卖出取反同口径', () => {
    const table = panel?.sections[3].table
    // o1: vwap=(10.42*60+10.44*40)/100=10.428；bp=(10.428-10.4)/10.4*10000≈26.9
    expect(table?.rows[0][5]).toBe('10.43')
    expect(table?.rows[0][6]).toBe('26.9 bp')
    // o2: vwap=10.98；bp=(11.0-10.98)/11.0*10000≈18.2
    expect(table?.rows[1][6]).toBe('18.2 bp')
  })
  it('绩效缺口点名 strategy/performance，不拿当日数据冒充', () => {
    const perf = panel?.sections[5].rows?.[0]
    expect(perf?.value).toContain('GET /v1/strategy/performance')
  })
})

describe('§5.2 信号牌', () => {
  const panel = buildDetail(fixture(), 'symbol:600519', NOW, { intel: { '123': { title: '宁王扩产', source: '巨潮', time: '09:50' } } })
  it('单只钻取：三价 + 依据、仓位依据、置信度', () => {
    const drill = panel?.sections.find((s) => s.key === 'drill')
    expect(drill?.rows?.find((r) => r.key === 'entry')?.note).toContain('入场带 10.30–10.50')
    expect(drill?.rows?.find((r) => r.key === 'stop')?.note).toContain('risk.stop_pct')
    expect(drill?.rows?.find((r) => r.key === 'pos')?.note).toContain('R07')
    expect(drill?.rows?.find((r) => r.key === 'conf')?.note).toContain('高价值')
  })
  it('四维雷达缺的维是 null 不是 0，并单独成行说明', () => {
    const radar = panel?.sections.find((s) => s.key === 'radar')
    expect(radar?.radar?.map((d) => d.value)).toEqual([78, 62, null, null])
    expect(radar?.rows?.map((r) => r.label)).toContain('资金面')
  })
  it('证据链：N:123 配到标题/来源/时间；取不到写未返回', () => {
    const table = panel?.sections.find((s) => s.key === 'evidence')?.table
    const news = table?.rows.find((r) => r[1] === 'N:123')
    expect(news?.[2]).toBe('宁王扩产')
    expect(news?.[3]).toContain('巨潮')
    const missing = buildDetail(fixture(), 'symbol:600519', NOW, { intel: { '123': null } })
    const row = missing?.sections.find((s) => s.key === 'evidence')?.table?.rows.find((r) => r[1] === 'N:123')
    expect(row?.[2]).toBe('标题未返回')
  })
  it('模型分 vs 规则分差 16 > 15 → 分歧 warn', () => {
    const cmp = panel?.sections.find((s) => s.key === 'compare')?.rows?.[0]
    expect(cmp?.value).toBe('88 vs 72')
    expect(cmp?.tone).toBe('warn')
    expect(cmp?.note).toContain('分歧')
  })
  it('推理摘录与同类历史点名接口；出口只跳路由', () => {
    expect(panel?.sections.find((s) => s.key === 'reason')?.rows?.[0].value).toContain('/v1/brain/reasoning?signal_id=')
    expect(panel?.sections.find((s) => s.key === 'history')?.rows?.[0].value).toContain('/v1/strategy/performance')
    expect(panel?.exit).toEqual({ label: '回到交易台确认', to: '/panel/trade' })
  })
  it('post:desk 只列待确认，有效期剩余按 now 算', () => {
    const desk = buildDetail(fixture(), 'post:desk', NOW)
    const table = desk?.sections[0].table
    expect(table?.rows).toHaveLength(1)
    expect(table?.rows[0][6]).toBe('剩 30 分')
    expect(desk?.sections.find((s) => s.key === 'drill')).toBeUndefined()
  })
})

describe('§5.3 风控牌', () => {
  const panel = buildDetail(fixture(), 'post:risk', NOW)
  it('限额环形：8/10 = 80% 转橙；单日亏损缺口不编 k/K', () => {
    const limits = panel?.sections.find((s) => s.key === 'limits')
    const buys = limits?.rows?.find((r) => r.key === 'buys')
    expect(buys?.value).toBe('8 / 10 笔')
    expect(buys?.ring?.tone).toBe('warn')
    // 单票取最大：000001 市值 10800 / 权益 100000 = 10.8% vs 5% → 216% 超限
    const single = limits?.rows?.find((r) => r.key === 'single')
    expect(single?.value).toBe('10.8% / 5.0%')
    expect(single?.ring?.tone).toBe('danger')
    const loss = limits?.rows?.find((r) => r.key === 'loss')
    expect(loss?.value).toContain('day_pnl')
    expect(limits?.rows?.find((r) => r.key === 'streak')?.value).toBe('后端无此参数')
  })
  it('ringTone 门限：>80 橙、=100 红', () => {
    expect(ringTone(79.9)).toBe('ok')
    expect(ringTone(80)).toBe('warn')
    expect(ringTone(100)).toBe('danger')
  })
  it('R01–R15 全表：命中次数来自日报，缺报写未返回，处置含缩量放行', () => {
    const table = panel?.sections.find((s) => s.key === 'rules')?.table
    expect(table?.rows).toHaveLength(15)
    expect(table?.rows[0][0]).toBe('R01')
    expect(table?.rows.find((r) => r[0] === 'R10')?.[2]).toBe('2')
    expect(table?.rows.find((r) => r[0] === 'R01')?.[2]).toBe('今日未命中')
    expect(table?.rows.find((r) => r[0] === 'R07')?.[3]).toBe('缩量放行')
    expect(RULES.map((r) => r.code)).toEqual(['R01', 'R02', 'R03', 'R04', 'R05', 'R06', 'R07', 'R08', 'R09', 'R10', 'R11', 'R12', 'R13', 'R14', 'R15'])
    const noReport = buildDetail({ ...fixture(), riskReport: { ok: false, summary: '' } }, 'post:risk', NOW)
    expect(noReport?.sections.find((s) => s.key === 'rules')?.table?.rows[0][2]).toBe('未返回')
  })
  it('急停历史：今日事件 + 懒加载近 7 日', () => {
    const withHistory = buildDetail(fixture(), 'post:risk', NOW, {
      killHistory: [{ date: '2026-10-12', ok: true, events: [{ kind: 'kill_switch', active: false, mode: 'SIM', source: 'manual', reason: '恢复', operator: 'admin', at: '2026-10-12 15:10' }] }],
    })
    const rows = withHistory?.sections.find((s) => s.key === 'kills')?.table?.rows
    expect(rows?.[0][0]).toBe('2026-10-13')
    expect(rows?.[0][2]).toBe('触发')
    expect(rows?.[1][0]).toBe('2026-10-12')
    expect(rows?.[1][2]).toBe('恢复')
  })
  it('准入：进度与还差几天；固定行本页只读', () => {
    const admit = panel?.sections.find((s) => s.key === 'admit')
    expect(admit?.rows?.find((r) => r.key === 'progress')?.value).toBe('12 / 20 个交易日')
    expect(admit?.rows?.find((r) => r.key === 'progress')?.note).toContain('还差 8')
    expect(admit?.foot).toContain('本页只读，急停在交易台')
  })
})

describe('§5.4 数据源牌', () => {
  const panel = buildDetail(fixture(), 'source:tdx', NOW)
  it('状态/吞吐按字段出，P95 点名缺的字段', () => {
    const state = panel?.sections.find((s) => s.key === 'state')
    expect(state?.rows?.find((r) => r.key === 'lastok')?.note).toContain('前')
    const tp = panel?.sections.find((s) => s.key === 'throughput')
    expect(tp?.rows?.find((r) => r.key === 'failrate')?.value).toBe('2.0%')
    expect(tp?.rows?.find((r) => r.key === 'avg')?.value).toBe('210 ms')
    expect(tp?.rows?.find((r) => r.key === 'p95')?.value).toBe('未接入')
  })
  it('quota_limit = 0 写「无限流」，不编剩余', () => {
    expect(panel?.sections.find((s) => s.key === 'quota')?.rows?.[0].value).toBe('无限流')
  })
  it('该域质量问题按检查项分桶，严重度 high 整行转橙', () => {
    const q = panel?.sections.find((s) => s.key === 'quality')
    expect(q?.rows?.find((r) => r.key === 'miss')?.value).toBe('12 条')
    expect(q?.rows?.find((r) => r.key === 'stale')?.value).toBe('3 条')
    expect(q?.table?.rows).toHaveLength(2)
    expect(q?.table?.flags?.[0]).toBe('warn')
    expect(q?.table?.flags?.[1]).toBe('')
  })
})

describe('§5.5 模型牌', () => {
  const panel = buildDetail(fixture(), 'post:model', NOW)
  it('分档全字段：失败率、P50/P95、在途/排队/KV、熔断状态', () => {
    const table = panel?.sections.find((s) => s.key === 'tiers')?.table
    expect(table?.rows[0][5]).toBe('2.4%')
    expect(table?.rows[0][7]).toBe('1840 ms')
    expect(table?.rows[0][10]).toBe('63%')
    expect(table?.rows[1][2]).toBe('熔断')
  })
  it('令牌/直方图/GPU 点名接口；固定路由文案', () => {
    expect(panel?.sections.find((s) => s.key === 'token')?.rows?.[0].value).toContain('/v1/brain/usage')
    expect(panel?.sections.find((s) => s.key === 'latency')?.rows?.[0].value).toContain('/v1/brain/latency-histogram')
    expect(panel?.sections.find((s) => s.key === 'throughput')?.rows?.find((r) => r.key === 'gpu')?.value).toContain('/v1/ops/gpu')
    expect(panel?.sections.find((s) => s.key === 'route')?.rows?.[0].value).toBe('技术/资金走小模型，消息/外围/综合走大模型')
  })
  it('评分分布十桶 + 散点/归因缺口', () => {
    expect(scoreHistogram([5, 15, 88, 100])).toEqual([
      { label: '0–10', count: 1 },
      { label: '10–20', count: 1 },
      { label: '20–30', count: 0 },
      { label: '30–40', count: 0 },
      { label: '40–50', count: 0 },
      { label: '50–60', count: 0 },
      { label: '60–70', count: 0 },
      { label: '70–80', count: 0 },
      { label: '80–90', count: 1 },
      { label: '90–100', count: 1 },
    ])
    const self = panel?.sections.find((s) => s.key === 'selfcheck')
    expect(self?.bars?.find((b) => b.label === '80–90')?.count).toBe(1)
    expect(self?.rows?.find((r) => r.key === 'scatter')?.value).toContain('/v1/strategy/score-vs-pnl')
  })
})

describe('§5.6 扫描牌', () => {
  const panel = buildDetail(fixture(), 'post:scan', NOW)
  it('六层 + 转化率；原因分布每层一个横条榜最多 6 项', () => {
    const layers = panel?.sections.find((s) => s.key === 'layers')
    expect(layers?.rows?.map((r) => r.label)).toEqual(['全市场', '初筛', 'AI≥70', '风控通过', '已下单', '成交'])
    expect(layers?.rows?.[0].value).toBe('未返回')
    expect(layers?.rows?.[1].value).toBe('3')
    const leaks = panel?.sections.filter((s) => s.key.startsWith('leak:')) ?? []
    expect(leaks.length).toBeGreaterThan(0)
    expect(leaks[0]?.bars!.length).toBeLessThanOrEqual(6)
    expect(leaks[0]?.bars?.[0]?.count ?? 0).toBeGreaterThanOrEqual(leaks[0]?.bars?.[1]?.count ?? 0)
  })
  it('漏选清单 ≤8 带中文原因；复盘只陈述事实', () => {
    const miss = panel?.sections.find((s) => s.key === 'miss')?.table
    expect(miss?.rows).toHaveLength(2)
    expect(miss?.rows[0][3]).toBe('综合分低于 75')
    const review = panel?.sections.find((s) => s.key === 'review')
    expect(review?.rows?.find((r) => r.key === 'limit')?.value).toBe('1 只')
    expect(review?.rows?.find((r) => r.key === 'best')?.value).toBe('300750 6.1%')
    expect(review?.foot).toContain('不做')
  })
})

describe('纯函数小件', () => {
  it('intelIdsOf 只认 N:数字，去重', () => {
    expect(intelIdsOf(['F:ma20=+2.3%', 'N:123', 'N:123', 'N:45', 'N:abc'])).toEqual(['123', '45'])
  })
  it('slippageBp 买入正为劣、卖出正也为劣', () => {
    expect(slippageBp('buy', 10, 10.01)).toBeCloseTo(10, 0)
    expect(slippageBp('buy', 10, 9.99)).toBeCloseTo(-10, 0)
    expect(slippageBp('sell', 10, 9.99)).toBeCloseTo(10, 0)
    expect(slippageBp('sell', 10, 10.01)).toBeCloseTo(-10, 0)
  })
  it('fillVwap 只配同一 clientOrderId', () => {
    const fills = [
      { id: 'o1', price: 10, qty: 50 },
      { id: 'o1', price: 11, qty: 50 },
      { id: 'o2', price: 99, qty: 100 },
    ]
    expect(fillVwap('o1', fills)).toBe(10.5)
    expect(fillVwap('o3', fills)).toBeNull()
  })
  it('equityStats 空采样与单点都是 null 而不是 0', () => {
    expect(equityStats([])).toEqual({ high: null, drawdownPct: null })
    expect(equityStats([100, null, 100])).toEqual({ high: 100, drawdownPct: 0 })
    expect(equityStats([100, 90, 95]).drawdownPct).toBeCloseTo(0.1, 5)
  })
})
