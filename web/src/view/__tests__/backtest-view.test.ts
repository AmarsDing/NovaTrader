/**
 * BacktestView 结果页六步骨架（W8b；R-UI-20/23/44）合同测试：
 * - 六步固定顺序（结论→信任条→假设→曲线→交易明细→原始参数），data-step 键序锁定；
 * - 结论 6 KPI + 警告行归入结论段；假设固定 8 格、未声明槽灰显保位；原始参数 mono 展开；
 * - 详情读口独立降级：getTask 挂 → 信任条 12 槽全缺位 + 参数段缺位徽注，报告本体五步照常（缺步灰显）。
 */
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BacktestView from '../BacktestView.vue'
import NtTrustBar from '@/display/NtTrustBar.vue'
import type { BacktestReport, BacktestTask, BacktestTaskDetail } from '@/api/backtest'

const task: BacktestTask = {
  id: '1',
  name: '回归 A',
  kind: 'single',
  strategy: 'breakout_ref',
  freq: '1d',
  status: 'succeeded',
  progress: 100,
  message: '',
  start: '2026-01-01',
  end: '2026-06-01',
  totalReturn: 0.12,
  maxDrawdown: -0.03,
  winRate: 0.6,
  hasReport: true,
}

const report: BacktestReport = {
  metrics: { totalReturn: 0.12, annualReturn: 0.3, maxDrawdown: -0.03, sharpe: 1.5, winRate: 0.6, profitFactor: 1.8, rounds: 5 },
  equity: [{ day: '2026-01-02', equity: 1, benchmark: 1, drawdown: 0 }],
  warnings: ['样本内只有 5 笔成交'],
  assumptions: ['佣金按万 2.5 双边', '滑点按 10bps'],
}

const trustBar = {
  barsTotal: 120,
  tradingDays: 100,
  tradeCount: 5,
  windowStart: '2026-01-01',
  windowEnd: '2026-06-01',
  costModel: 'fee+slippage',
  feeBps: 2.5,
  slippageBps: 10,
  blindVerified: true,
  pboVerified: false,
  datasetManifestId: 'm-1',
  datasetHash: 'h-1',
}

const detail: BacktestTaskDetail = {
  ...task,
  trustBar,
  config: { strategy: 'breakout_ref', initial_cash: 100000 },
}

const api = vi.hoisted(() => ({
  listTasks: vi.fn(async () => [task]),
  listStrategies: vi.fn(async () => []),
  createTask: vi.fn(async () => ({})),
  getReport: vi.fn(async () => report),
  listTrades: vi.fn(async () => []),
  getTask: vi.fn(async () => detail),
}))

vi.mock('@/api/backtest', () => api)

const mountOptions = {
  global: {
    stubs: { NtEquityCurve: true, ResearchJobs: true },
  },
}

async function openReport() {
  const wrapper = mount(BacktestView, mountOptions)
  await flushPromises()
  const btn = wrapper.findAll('button').find((b) => b.text() === '报告')
  expect(btn).toBeTruthy()
  await btn!.trigger('click')
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  api.listTasks.mockResolvedValue([task])
  api.listStrategies.mockResolvedValue([])
  api.getReport.mockResolvedValue(report)
  api.listTrades.mockResolvedValue([])
  api.getTask.mockResolvedValue(detail)
})

describe('六步骨架（步骤序固定）', () => {
  it('data-step 键序锁定：conclusion→trust→assumptions→curve→trades→params', async () => {
    const wrapper = await openReport()
    expect(wrapper.findAll('[data-step]').map((el) => el.attributes('data-step'))).toEqual([
      'conclusion',
      'trust',
      'assumptions',
      'curve',
      'trades',
      'params',
    ])
    wrapper.unmount()
  })

  it('结论段 6 KPI 全出、警告行归入结论段；信任条判据位按详情渲染', async () => {
    const wrapper = await openReport()
    const conclusion = wrapper.find('[data-step="conclusion"]')
    expect(conclusion.findAll('.nt-stat')).toHaveLength(6)
    expect(conclusion.text()).toContain('样本内只有 5 笔成交')
    const trust = wrapper.findComponent(NtTrustBar)
    expect(trust.props('bar')).toEqual(trustBar)
    expect(wrapper.find('[data-step="trust"]').text()).toContain('已验证')
    expect(wrapper.find('[data-step="trust"]').text()).toContain('未验证') // pboVerified=false 灰显
    wrapper.unmount()
  })

  it('假设固定 8 格：声明填两格、六槽灰显「未声明」；原始参数 mono 原文展开', async () => {
    const wrapper = await openReport()
    const cells = wrapper.findAll('[data-step="assumptions"] [data-slot]')
    expect(cells).toHaveLength(8)
    expect(cells[0].text()).toBe('佣金按万 2.5 双边')
    expect(cells[1].text()).toBe('滑点按 10bps')
    expect(cells[2].text()).toBe('未声明')
    expect(cells.filter((c) => c.text() === '未声明')).toHaveLength(6)
    const pre = wrapper.find('[data-step="params"] pre')
    expect(pre.exists()).toBe(true)
    expect(pre.text()).toContain('initial_cash')
    wrapper.unmount()
  })
})

describe('详情读口独立降级（缺步灰显，不拖死报告）', () => {
  it('getTask 挂 → 信任条 12 槽全缺位+参数段缺位徽注，结论/曲线/明细照常', async () => {
    api.getTask.mockRejectedValue(new Error('详情读口断开'))
    const wrapper = await openReport()
    const trust = wrapper.find('[data-step="trust"]')
    expect(trust.findAll('.nt-missing-inline')).toHaveLength(12)
    expect(trust.findAll('.nt-trust__pass')).toHaveLength(0)
    expect(wrapper.find('[data-step="params"]').text()).toContain('未声明')
    // 报告本体五步照常：KPI 在、警告在、假设格在
    expect(wrapper.find('[data-step="conclusion"]').text()).toContain('样本内只有 5 笔成交')
    expect(wrapper.findAll('[data-step="assumptions"] [data-slot]')).toHaveLength(8)
    expect(wrapper.find('[data-step="trades"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
