/**
 * P2 告警漏斗接线（方案 §8）合同测试：
 *   L4 旁路 → 静默落账 → 同因聚合 bump → 冷却抑 toast → L3 角标+单响 → panelFor 分支 → raiseAlert 五段文案。
 * 速记：
 * - 假定时器定在 2026-10-21 12:00（工作日盘中），聚合窗 10s / 默认冷却 60s 用 setSystemTime 推进；
 * - 模块级聚合器/冷却表跨用例共享，故每条用例用独立 dedupKey 隔离；
 * - sound 走 vi.mock（jsdom 无 AudioContext）；bumpToast 用 importOriginal 套 spy，行为与调用次数都能断言。
 */
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mock = vi.hoisted(() => ({ playOnce: vi.fn(() => true), bumpToast: vi.fn() }))
vi.mock('@/utils/sound', () => ({ playOnce: mock.playOnce }))
vi.mock('@/stores/alerts', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/stores/alerts')>()
  mock.bumpToast.mockImplementation((id: number | string) => mod.bumpToast(id))
  return { ...mod, bumpToast: mock.bumpToast }
})

import { useAlertsStore } from '../alerts'
import { useNotifyStore } from '../notify'
import { useSettingsStore } from '../settings'
import { deskNotify, raiseAlert } from '../../utils/notify'
import { saveQuiet } from '../../utils/alert-quiet'
import type { AlertEventView } from '../../ui/alert-levels'

const T0 = new Date(2026, 9, 21, 12, 0, 0).getTime()

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
  vi.setSystemTime(T0)
  localStorage.clear()
  mock.playOnce.mockClear()
  mock.bumpToast.mockClear()
})

afterEach(() => vi.useRealTimers())

describe('deskNotify 告警漏斗', () => {
  it('L4 旁路：无 toast、无 feed、无声、无角标（AlertCenter 全屏通道负责）', async () => {
    const shown = await deskNotify('强平触发', '账户 LIVE 触发强平线', 'critical', {
      kind: 'killswitch',
      level: 'critical',
      title: '强平触发',
      dedupKey: 'l4:1',
    })
    expect(shown).toBe(false)
    expect(useAlertsStore().toasts).toHaveLength(0)
    expect(useNotifyStore().feed).toHaveLength(0)
    expect(useNotifyStore().attention).toEqual({})
    expect(mock.playOnce).not.toHaveBeenCalled()
  })

  it('静默窗内：仅落账 silenced=true，无 toast/无声/无角标', async () => {
    saveQuiet({ cooldownMs: 60_000, windows: [{ start: '00:00', end: '23:59', levels: ['L1', 'L2', 'L3'] }] })
    const shown = await deskNotify('扫描零命中', '本轮无标的', 'warn', {
      kind: 'scan_zero_hit',
      level: 'warn',
      title: '扫描零命中',
      dedupKey: 'quiet:1',
    })
    expect(shown).toBe(false)
    const notify = useNotifyStore()
    expect(useAlertsStore().toasts).toHaveLength(0)
    expect(mock.playOnce).not.toHaveBeenCalled()
    expect(notify.attention).toEqual({})
    expect(notify.feed).toHaveLength(1)
    expect(notify.feed[0]).toMatchObject({ level: 'L2', silenced: true, causeKey: 'quiet:1' })
  })

  it('10s 内同因：toast 仍 1 条且 bumpToast 命中一次，feed 计数对齐为 2', async () => {
    const ev: AlertEventView = { kind: 'scan_zero_hit', level: 'warn', title: '扫描零命中', dedupKey: 'agg:1' }
    expect(await deskNotify('扫描零命中', '本轮无标的', 'warn', ev)).toBe(true)
    vi.setSystemTime(T0 + 2_000)
    expect(await deskNotify('扫描零命中', '本轮又无标的', 'warn', ev)).toBe(true)
    const alerts = useAlertsStore()
    const notify = useNotifyStore()
    expect(alerts.toasts).toHaveLength(1)
    expect(mock.bumpToast).toHaveBeenCalledTimes(1)
    expect(alerts.toasts[0].count).toBe(2)
    expect(notify.feed).toHaveLength(1)
    expect(notify.feed[0].count).toBe(2)
  })

  it('同因跨聚合窗但仍在冷却窗内：不重复 toast，feed 仍落一条新账', async () => {
    const ev: AlertEventView = { kind: 'scan_zero_hit', level: 'warn', title: '扫描零命中', dedupKey: 'cool:1' }
    expect(await deskNotify('扫描零命中', '第一轮', 'warn', ev)).toBe(true)
    vi.setSystemTime(T0 + 12_000)
    expect(await deskNotify('扫描零命中', '第二轮', 'warn', ev)).toBe(false)
    const alerts = useAlertsStore()
    const notify = useNotifyStore()
    expect(alerts.toasts).toHaveLength(1)
    expect(mock.bumpToast).not.toHaveBeenCalled()
    expect(notify.feed).toHaveLength(2)
    expect(notify.feed[0]).toMatchObject({ causeKey: 'cool:1', body: '第二轮' })
  })

  it('L3 且 settings.sound 开 → playOnce 单响一次 + quote-sidebar 抬 L3 角标', async () => {
    useSettingsStore().setSound(true)
    await deskNotify('急拉预警', '600519 涨幅>5%', 'surge', {
      kind: 'surge',
      symbol: '600519',
      title: '急拉预警',
      dedupKey: 'snd:on',
    })
    expect(mock.playOnce).toHaveBeenCalledTimes(1)
    expect(useNotifyStore().attention['quote-sidebar']).toMatchObject({ level: 'L3', count: 1 })
  })

  it('L3 但 settings.sound 关 → 不响，角标仍抬', async () => {
    useSettingsStore().setSound(false)
    await deskNotify('触板预警', '000001 触及涨停', 'limit_up', {
      kind: 'limit_up',
      symbol: '000001',
      title: '触板预警',
      dedupKey: 'snd:off',
    })
    expect(mock.playOnce).not.toHaveBeenCalled()
    expect(useNotifyStore().attention['quote-sidebar']).toMatchObject({ level: 'L3', count: 1 })
  })

  it.each([
    [{ kind: 'alert-rule', symbol: '600000', title: '规则命中', dedupKey: 'pf:1' }, 'alert-rules'],
    [{ kind: 'surge', symbol: '300750', title: '急拉', dedupKey: 'pf:2' }, 'quote-sidebar'],
    [{ kind: 'surge', category: 'datahub', title: '行情断流', dedupKey: 'pf:3' }, 'datasource-center'],
    [{ kind: 'surge', title: '心跳异常', dedupKey: 'pf:4' }, 'notify-center'],
  ] as const)('panelFor 分支 %j → %s 角标', async (ev, panelId) => {
    await deskNotify(ev.title, '', ev.kind, ev)
    const notify = useNotifyStore()
    expect(notify.attention[panelId]).toMatchObject({ level: 'L3', count: 1 })
    expect(Object.keys(notify.attention)).toEqual([panelId])
  })
})

describe('raiseAlert 五段式文案', () => {
  it('无 symbol → 对象段写「系统」，正文为「对象 · 事件 · 条件 · 时点」', async () => {
    const at = new Date(2026, 9, 21, 14, 30, 5).getTime()
    const shown = await raiseAlert({ kind: 'alert-rule', title: '规则触发', body: '涨幅>5%', at, dedupKey: 'ra:1' })
    expect(shown).toBe(true)
    const toast = useAlertsStore().toasts.at(-1)
    expect(toast?.title).toBe('规则触发')
    expect(toast?.kind).toBe('warn')
    expect(toast?.body).toContain('系统')
    expect(toast?.body).toBe('系统 · 规则触发 · 涨幅>5% · 14:30:05')
  })
})
