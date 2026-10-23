import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getAuction, getIntradayAlerts, getLimitPremium, getQuote } from '@/api/market'
import { brainLatencyHistogram, getFrozenEmotion } from '@/api/brain'
import { normalizeSignal } from '@/api/signals'
import { request } from '@/api/http'

vi.mock('@/api/http', () => ({ request: vi.fn() }))
const requestMock = vi.mocked(request)

// api-fill 批次：getQuote/getIntradayAlerts/normalizeSignal 回填名称字典，函数体内 useSecurityStore，需激活 pinia
beforeEach(() => setActivePinia(createPinia()))

/**
 * 「没取到≠0」（P3-10c）fix 字段贯通钉死：
 * 后端折叠破口修复后，proto optional 缺键必须在归一化层落到 null（显「—」/「样本不足」），
 * 不得回退成 0 伪装读数；哨兵字段（键恒在/契约 0=未知）保持 ?? 0，不得误改 ?? null。
 */
describe('P3-10c fix 字段：缺键贯通 null，不落 0', () => {
  beforeEach(() => {
    requestMock.mockReset()
    // api-fill 批次：getQuote/getIntradayAlerts/normalizeSignal 回填名称字典，函数体内 useSecurityStore，需激活 pinia
    setActivePinia(createPinia())
  })

  it('getAuction：四推导字段缺键 → null；auctionAmount 键恒在缺键按哨兵 0', async () => {
    requestMock.mockResolvedValue({
      found: true,
      totalAmount: 1_000_000,
      limitUpCount: 1,
      limitUps: [
        // 新股：无 auction_pct / vol_ratio / turnover / unmatched 键（proto optional 省略发送）
        { symbol: '000001.SZ', auction_amount: 500_000, auction_limit_up: true },
        // 平开真 0 必须原样贯通，不得被吞成 null
        { symbol: '600519.SH', auction_pct: 0, auction_amount: 300_000, auction_vol_ratio: 0, auction_turnover: 0, auction_unmatched: 0, auction_limit_up: false },
      ],
      topRush: [],
      asOf: '09:25',
    })
    const s = await getAuction()
    expect(s.limitUps[0]).toMatchObject({
      symbol: '000001.SZ',
      auctionPct: null,
      auctionVolRatio: null,
      auctionTurnover: null,
      auctionUnmatched: null,
      auctionAmount: 500_000,
    })
    expect(s.limitUps[1]).toMatchObject({ auctionPct: 0, auctionVolRatio: 0, auctionTurnover: 0, auctionUnmatched: 0 })
  })

  it('getLimitPremium：openAvg/highAvg 缺键 → null；sample/limitAgain/closeAvg 恒有哨兵', async () => {
    requestMock.mockResolvedValue({
      found: true,
      summary: {
        tradeDate: '2026-10-19',
        baseDate: '2026-10-18',
        sample: 12,
        limitAgain: 0,
        // 开盘口径整批缺 → open_avg 省略；快照收盘补齐前 → high_avg 省略
        closeAvg: 0.012,
        closeWin: 0.5,
        hasClose: true,
      },
      days: [],
      rows: [],
    })
    const p = await getLimitPremium()
    expect(p.summary).toMatchObject({ openAvg: null, highAvg: null, closeAvg: 0.012, closeWin: 0.5, sample: 12, limitAgain: 0 })
  })

  it('brainLatencyHistogram：空窗 total=0 → p50/p95 为 null（0ms 不是合法分位值）', async () => {
    requestMock.mockResolvedValue({ windowMinutes: 60, total: 0, p50Ms: 0, p95Ms: 0, buckets: [] })
    const h = await brainLatencyHistogram()
    expect(h.total).toBe(0)
    expect(h.p50Ms).toBeNull()
    expect(h.p95Ms).toBeNull()

    requestMock.mockResolvedValue({ windowMinutes: 60, total: 42, p50Ms: 320, p95_ms: 1500, buckets: [{ leMs: 500, count: 30 }] })
    const h2 = await brainLatencyHistogram()
    expect(h2.p50Ms).toBe(320)
    expect(h2.p95Ms).toBe(1500)
    expect(h2.buckets).toEqual([{ leMs: 500, count: 30 }])
  })

  it('getFrozenEmotion：structureGaps 脏值（null/0）过滤，不得吞成「0板断层」', async () => {
    requestMock.mockResolvedValue({ date: '2026-10-19', structureGaps: [null, 0, 2, 3, 'x'] })
    const e = await getFrozenEmotion('2026-10-19')
    expect(e.structureGaps).toEqual([2, 3])
  })

  it('normalizeSignal：high_value 后端唯一权威，降级高分不回退复活（原 >=85 回退已删）', async () => {
    const degraded = normalizeSignal({ symbol: '000001.SZ', finalScore: 86.7, highValue: false, aiDegraded: true })
    expect(degraded.highValue).toBe(false)
    // 老接口缺 high_value 键时也不得有前端 >=85 回退
    const legacy = normalizeSignal({ symbol: '000001.SZ', finalScore: 90 })
    expect(legacy.highValue).toBe(false)
  })

  it('getIntradayAlerts：pct/price 缺键 → null（首个真实 missing 路径 P3-10 §6），真 0 涨幅原样贯通', async () => {
    requestMock.mockResolvedValue({
      found: true,
      alerts: [
        // 老版本/将来真缺失写入方：proto optional 省略 pct/price 键
        { symbol: '000001.SZ', name: '测试股', kind: 'limit_up', time: '2026-10-19T09:40:00+08:00' },
        // 平开贴昨收触发时真 0 涨幅必须保留，不得被吞成 null
        { symbol: '600519.SH', name: '贵州茅台', kind: 'break', time: '2026-10-19T10:05:00+08:00', pct: 0, price: 1700.5 },
      ],
    })
    const rows = await getIntradayAlerts('2026-10-19')
    expect(rows[0]).toMatchObject({ symbol: '000001.SZ', pct: null, price: null })
    expect(rows[1]).toMatchObject({ symbol: '600519.SH', pct: 0, price: 1700.5 })
  })

  it('getQuote 哨兵：契约 0=未知保持 0（勿改 null）；prevClose=0 是派生不可算总闸门', async () => {
    requestMock.mockResolvedValue({ symbol: '301999.SZ', last: 15.2, prevClose: 0, chg: 0, pctChg: 0, found: true, source: 'snap' })
    const q = await getQuote('301999.SZ')
    expect(q.last).toBe(15.2)
    expect(q.prevClose).toBe(0)
    expect(q.pctChg).toBe(0)
  })
})
