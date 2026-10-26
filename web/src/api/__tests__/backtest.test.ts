/**
 * backtest API 适配层（W8b；R-UI-20/23 数据纪律）合同测试：
 * - normalizeTrustBar：camel/snake 双口径归一；契约零值折缺（0=未统计/未声明 → null），
 *   tradeCount 保留 0（0 笔=样本不足结论位，是有信息的事实）；缺包/错形 → 全缺位对象；
 * - getTask：详情读口带信任条与原始参数；trust_bar 缺包合成全缺位（前端不整段另设分支）；
 * - getReport：assumptions/warnings 清单归一（非数组 → 空清单，不伪造）。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getReport, getTask, normalizeTrustBar } from '../backtest'

vi.mock('@/api/http', () => ({ request: vi.fn() }))

async function mockedRequest() {
  const mod = await import('@/api/http')
  return mod.request as unknown as ReturnType<typeof vi.fn>
}

beforeEach(async () => {
  ;(await mockedRequest()).mockReset()
})

describe('normalizeTrustBar（契约零值折缺）', () => {
  it('snake_case 全字段行归一为 camel 类型', () => {
    const bar = normalizeTrustBar({
      bars_total: 120,
      trading_days: 100,
      trade_count: 42,
      window_start: '2026-01-01',
      window_end: '2026-06-01',
      cost_model: 'fee+slippage',
      fee_bps: 2.5,
      slippage_bps: 10,
      blind_verified: true,
      pbo_verified: false,
      dataset_manifest_id: 'm-1',
      dataset_hash: 'h-1',
    })
    expect(bar).toEqual({
      barsTotal: 120,
      tradingDays: 100,
      tradeCount: 42,
      windowStart: '2026-01-01',
      windowEnd: '2026-06-01',
      costModel: 'fee+slippage',
      feeBps: 2.5,
      slippageBps: 10,
      blindVerified: true,
      pboVerified: false,
      datasetManifestId: 'm-1',
      datasetHash: 'h-1',
    })
  })

  it('0=未统计/未声明折 null；tradeCount 的 0 保留（样本不足结论位）', () => {
    const bar = normalizeTrustBar({
      barsTotal: 0,
      tradingDays: 0,
      tradeCount: 0,
      feeBps: 0,
      slippageBps: 0,
      windowStart: '',
      costModel: '',
      blindVerified: false,
      pboVerified: false,
      datasetManifestId: '',
      datasetHash: '',
    })
    expect(bar.barsTotal).toBeNull()
    expect(bar.tradingDays).toBeNull()
    expect(bar.tradeCount).toBe(0)
    expect(bar.feeBps).toBeNull()
    expect(bar.slippageBps).toBeNull()
    expect(bar.windowStart).toBe('')
    expect(bar.blindVerified).toBe(false)
  })

  it('整条缺包/错形 → 全缺位对象（零值=未验证，绝不渲染已验证）', () => {
    const bar = normalizeTrustBar(undefined)
    expect(bar.blindVerified).toBe(false)
    expect(bar.pboVerified).toBe(false)
    expect(bar.barsTotal).toBeNull()
    expect(bar.feeBps).toBeNull()
    expect(bar.costModel).toBe('')
    expect(bar.datasetManifestId).toBe('')
  })
})

describe('getTask 详情读口', () => {
  it('返回任务基座 + trustBar + config 原始参数', async () => {
    const req = await mockedRequest()
    req.mockResolvedValue({
      id: 7,
      name: '回归 A',
      strategy: 'breakout_ref',
      freq: '1d',
      status: 'succeeded',
      start_date: '2026-01-01',
      end_date: '2026-06-01',
      trust_bar: { bars_total: 60, trade_count: 12, blind_verified: true, dataset_manifest_id: 'm-1' },
      config: { strategy: 'breakout_ref', initial_cash: 100000 },
      summary: { total_return: 0.12, has_report: true },
    })
    const detail = await getTask('7')
    expect(req).toHaveBeenCalledWith('/api/backtest/tasks/7')
    expect(detail.name).toBe('回归 A')
    expect(detail.totalReturn).toBe(0.12)
    expect(detail.trustBar.barsTotal).toBe(60)
    expect(detail.trustBar.blindVerified).toBe(true)
    expect(detail.config).toEqual({ strategy: 'breakout_ref', initial_cash: 100000 })
  })

  it('trust_bar 缺包合成全缺位；config 缺省为 null（缺步灰显不崩）', async () => {
    const req = await mockedRequest()
    req.mockResolvedValue({ id: 8, name: '无判据任务', status: 'failed' })
    const detail = await getTask('8')
    expect(detail.trustBar.blindVerified).toBe(false)
    expect(detail.trustBar.barsTotal).toBeNull()
    expect(detail.config).toBeNull()
  })
})

describe('getReport 假设/警告清单', () => {
  it('assumptions/warnings 原样归一为字符串数组', async () => {
    const req = await mockedRequest()
    req.mockResolvedValue({
      report: {
        metrics: { total_return: 0.05 },
        equity: [],
        warnings: ['样本内只有 3 笔成交'],
        assumptions: ['佣金按万 2.5 双边', '滑点按 10bps'],
      },
    })
    const rep = await getReport('9')
    expect(rep.assumptions).toEqual(['佣金按万 2.5 双边', '滑点按 10bps'])
    expect(rep.warnings).toEqual(['样本内只有 3 笔成交'])
  })

  it('assumptions 缺省/非数组 → 空清单（前端 8 格灰显，不伪造条目）', async () => {
    const req = await mockedRequest()
    req.mockResolvedValue({ report: { metrics: {}, warnings: null } })
    const rep = await getReport('9')
    expect(rep.assumptions).toEqual([])
    expect(rep.warnings).toEqual([])
  })
})
