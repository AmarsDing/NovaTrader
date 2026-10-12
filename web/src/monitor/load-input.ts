import { listAudit } from '@/api/audit'
import { brainStats, getBriefing } from '@/api/brain'
import { getAdmission, getReport, getShadowStats, listTasks } from '@/api/backtest'
import { getQuality, listSources } from '@/api/datahub'
import { hotWords, timeline as fetchTimeline } from '@/api/intel'
import { getFactors, getSentimentDetail, listLimits, listSectors } from '@/api/market'
import { listMessages } from '@/api/notify'
import { opsStatus } from '@/api/ops'
import { getRiskReport, getRiskState } from '@/api/risk'
import { listCandidates } from '@/api/strategy'
import { listSignals } from '@/api/signals'
import { getAccount, listFills, listOrders, listPositions } from '@/api/trade'
import { useAccountStore } from '@/stores/account'
import { useRiskStore } from '@/stores/risk'
import { quiet } from '@/utils/async'
import type { MonitorInput, Pulse } from './types'

/** 各块独立失败。返回值永远可交给 buildScene，不向外抛。 */
export async function loadMonitorInput(focusSymbol: string, pulses: Pulse[]): Promise<MonitorInput> {
  const accountStore = useAccountStore()
  const book = accountStore.book || 'SIM'
  const focus = focusSymbol.trim()

  const [account, positions, orders, fills, risk, report, ops, sources, quality, sentiment, sectors, limits, signals, candidates, briefing, words, stats, tasks, admission, shadow, notices, audit, tape, factors] =
    await Promise.all([
      quiet(() => getAccount(book)),
      quiet(() => listPositions(book)),
      quiet(() => listOrders(book)),
      quiet(() => listFills(book)),
      quiet(() => getRiskState()),
      quiet(() => getRiskReport()),
      quiet(() => opsStatus()),
      quiet(() => listSources()),
      quiet(() => getQuality()),
      quiet(() => getSentimentDetail()),
      quiet(() => listSectors(8)),
      quiet(() => listLimits('UP')),
      quiet(() => listSignals()),
      quiet(() => listCandidates()),
      quiet(() => getBriefing('')),
      quiet(() => hotWords()),
      quiet(() => brainStats()),
      quiet(() => listTasks()),
      quiet(() => getAdmission()),
      quiet(() => getShadowStats()),
      quiet(() => listMessages({ limit: 8 })),
      quiet(() => listAudit({ limit: 8 })),
      focus ? quiet(() => fetchTimeline(focus)) : Promise.resolve(null),
      focus ? quiet(() => getFactors(focus, 'capital')) : Promise.resolve(null),
    ])

  void accountStore.refresh()
  void useRiskStore().refresh()

  return {
    now: Date.now(),
    focusSymbol: focus,
    pulses,
    account: account
      ? {
          ok: true,
          book,
          equity: account.equity,
          cash: account.cash,
          dayPnl: account.dayPnl,
          positions: positions ?? [],
          positionsOk: positions != null,
        }
      : null,
    risk: risk
      ? {
          ok: true,
          mode: risk.mode,
          killActive: risk.killActive,
          killReason: risk.killReason,
          buysToday: risk.buysToday,
          marketBreaker: risk.marketBreaker,
        }
      : null,
    ops: ops
      ? {
          ok: true,
          services: ops.services.map((item) => ({ name: item.name, status: item.status, detail: item.detail })),
          sources: ops.sources.map((item) => ({ name: item.name, status: item.status, detail: item.detail })),
          resources: ops.resources.map((item) => ({ name: item.name, status: item.status, detail: item.detail })),
        }
      : null,
    sources: sources
      ? {
          ok: true,
          items: sources.map((item) => ({
            id: item.id,
            name: item.name,
            status: item.status,
            detail: sourceDetail(item),
            lastAt: item.lastAt,
          })),
        }
      : null,
    sentiment: sentiment
      ? {
          ok: true,
          phase: sentiment.phase,
          up: sentiment.upCount,
          down: sentiment.downCount,
          asOf: sentiment.asOf,
          stale: sentiment.stale,
          brokenRate: sentiment.brokenRate,
          maxHeight: sentiment.maxHeight,
          amount: sentiment.amount,
        }
      : null,
    sectors: sectors
      ? {
          ok: true,
          rows: sectors.map((row) => ({
            name: row.name,
            avgPct: row.avgPct,
            overseas: row.overseas,
            leader: row.leader,
          })),
        }
      : null,
    limits: limits
      ? {
          ok: true,
          rows: limits.map((row) => ({ symbol: row.symbol, consecutive: row.consecutive, status: row.status })),
        }
      : null,
    signals: signals
      ? {
          ok: true,
          items: signals.map((item) => ({
            id: item.id,
            symbol: item.symbol,
            name: item.name,
            score: item.score,
            highValue: item.highValue,
            degraded: item.degraded,
            status: item.status,
            evidence: item.evidence,
            dims: item.dims,
            entry: item.entry,
            stop: item.stop,
            take: item.take,
            time: item.time,
          })),
        }
      : null,
    briefing: briefing
      ? {
          ok: true,
          generatedAt: briefing.generatedAt,
          stale: briefing.stale,
          items: briefing.items.map((item) => ({
            title: item.title,
            body: item.body,
            symbol: item.symbol,
            kind: item.kind,
          })),
        }
      : null,
    hotWords: words ? { ok: true, items: words.map((item) => ({ type: item.type, target: item.target, count: item.count })) } : null,
    timeline:
      focus && tape
        ? {
            ok: true,
            symbol: focus,
            items: tape.items.map((item) => ({ id: item.id, title: item.title, time: item.time, source: item.source })),
          }
        : null,
    brainStats: stats
      ? {
          ok: true,
          tiers: stats.map((tier) => ({
            tier: tier.tier,
            model: tier.model,
            configured: tier.configured,
            circuitOpen: tier.circuitOpen,
            calls: tier.calls,
            failures: tier.failures,
            p50Ms: tier.p50Ms,
            p95Ms: tier.p95Ms,
            inFlight: tier.inFlight,
            queued: tier.queued,
            kvCacheUsage: tier.kvCacheUsage,
          })),
        }
      : null,
    equityCurve: await equityCurve(tasks),
    orders: orders ? { ok: true, items: orders.map((item) => ({ id: item.id, symbol: item.symbol, side: item.side, status: item.status, price: item.price, volume: item.volume, reason: item.reason })) } : { ok: false, items: [] },
    fills: fills ? { ok: true, items: fills.map((item) => ({ id: item.id, symbol: item.symbol, side: item.side, price: item.price, qty: item.qty, time: item.time })) } : { ok: false, items: [] },
    candidates: candidates
      ? { ok: true, items: candidates.map((item) => ({ symbol: item.symbol, name: item.name, strategy: item.strategy, missReason: item.missReason, finalScore: item.finalScore })) }
      : { ok: false, items: [] },
    quality: quality
      ? { ok: true, items: quality.map((item) => ({ domain: item.domain, tradeDate: item.tradeDate, missing: item.missing, stale: item.stale, detail: item.detail })) }
      : { ok: false, items: [] },
    admission: admission
      ? { ok: true, ready: admission.ready, tradingDays: admission.tradingDays, requiredDays: admission.requiredDays, items: admission.items.map((item) => ({ label: item.label, status: item.status, detail: item.detail })) }
      : { ok: false, ready: false, tradingDays: null, requiredDays: null, items: [] },
    shadow: shadow ? { ok: true, items: shadow.map((item) => ({ name: item.name, live: item.live, shadow: item.shadow })) } : { ok: false, items: [] },
    tasks: tasks ? { ok: true, items: tasks.map((item) => ({ id: item.id, name: item.name, status: item.status, message: item.message })) } : { ok: false, items: [] },
    notices: notices ? { ok: true, items: notices.map((item) => ({ id: item.id, title: item.title, level: item.level, time: item.time })) } : { ok: false, items: [] },
    audit: audit ? { ok: true, items: audit.map((item) => ({ id: String(item.id), action: item.action, target: item.target, time: item.createdAt })) } : { ok: false, items: [] },
    factors:
      focus && factors
        ? { ok: true, symbol: focus, items: factors.flatMap((row) => row.values) }
        : focus
          ? { ok: false, symbol: focus, items: [] }
          : null,
    riskReport: report ? { ok: true, summary: report.summary } : { ok: false, summary: '' },
  }
}

function sourceDetail(item: { status: string; detail: string; successRate?: number | null; latencyMs?: number | null; quotaUsed?: number | null; quotaLimit?: number | null; active?: boolean }): string {
  const bits: string[] = []
  if (item.successRate != null) bits.push(`成功率 ${item.successRate <= 1 ? Math.round(item.successRate * 100) : Math.round(item.successRate)}%`)
  if (item.latencyMs != null) bits.push(`延迟 ${Math.round(item.latencyMs)}ms`)
  if (item.quotaUsed != null && item.quotaLimit != null) bits.push(`配额 ${item.quotaUsed}/${item.quotaLimit}`)
  if (item.active) bits.push('在用')
  if (item.detail && item.detail !== item.status) bits.push(item.detail)
  return bits.join(' · ') || item.detail || item.status
}

async function equityCurve(tasks: { id: string }[] | null): Promise<MonitorInput['equityCurve']> {
  if (!tasks) return { ok: false, points: [] }
  const first = tasks.find((task) => task.id)
  if (!first) return { ok: true, points: [] }
  const report = await quiet(() => getReport(first.id))
  if (!report) return { ok: false, points: [] }
  return { ok: true, points: report.equity.map((point) => ({ day: point.day, equity: point.equity })) }
}

export function emptyMonitorInput(): MonitorInput {
  return {
    now: Date.now(),
    focusSymbol: '',
    account: null,
    risk: null,
    ops: null,
    sources: null,
    sentiment: null,
    sectors: null,
    limits: null,
    signals: null,
    briefing: null,
    hotWords: null,
    timeline: null,
    brainStats: null,
    equityCurve: null,
    pulses: [],
  }
}
