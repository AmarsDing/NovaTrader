import { listAudit } from '@/api/audit'
import { brainLatencyHistogram, brainStats, brainUsage, getBriefing } from '@/api/brain'
import { getAdmission, getReport, getShadowStats, listTasks } from '@/api/backtest'
import { getQuality, listSources } from '@/api/datahub'
import { hotWords, timeline as fetchTimeline } from '@/api/intel'
import { getAuction, getBreadth, getFactors, getKlineGaps, getLadder, getLimitPremium, getSentimentDetail, listLimits, listSectors } from '@/api/market'
import { listMessages } from '@/api/notify'
import { getOpsBrokerFiles, getOpsDb, getOpsGpu, getOpsNats, opsStatus } from '@/api/ops'
import { getRiskLimits, getRiskReport, getRiskState } from '@/api/risk'
import { listCandidates, getScoreVsPnl, getStrategyPerformance } from '@/api/strategy'
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

  const [account, positions, orders, fills, risk, report, limits, ops, sources, quality, sentiment, sectors, limitsBoard, signals, candidates, briefing, words, stats, usage, latency, tasks, admission, shadow, notices, audit, tape, factors, perf, scatter, auction, ladder, gpu, brokerFiles, premium, breadth, klineGaps, db, nats] =
    await Promise.all([
      quiet(() => getAccount(book)),
      quiet(() => listPositions(book)),
      quiet(() => listOrders(book)),
      quiet(() => listFills(book)),
      quiet(() => getRiskState()),
      quiet(() => getRiskReport()),
      quiet(() => getRiskLimits()),
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
      quiet(() => brainUsage('today')),
      quiet(() => brainLatencyHistogram(60)),
      quiet(() => listTasks()),
      quiet(() => getAdmission()),
      quiet(() => getShadowStats()),
      quiet(() => listMessages({ limit: 8 })),
      quiet(() => listAudit({ limit: 8 })),
      focus ? quiet(() => fetchTimeline(focus)) : Promise.resolve(null),
      focus ? quiet(() => getFactors(focus, 'capital')) : Promise.resolve(null),
      quiet(() => getStrategyPerformance(20)),
      quiet(() => getScoreVsPnl(50)),
      quiet(() => getAuction(10)),
      quiet(() => getLadder(5)),
      quiet(() => getOpsGpu()),
      quiet(() => getOpsBrokerFiles()),
      /* P2：K 线缺口 / 昨涨停今溢价 / 20 日均线占比 / PG / NATS（§7） */
      quiet(() => getLimitPremium(5, 50)),
      quiet(() => getBreadth(10)),
      quiet(() => getKlineGaps(5)),
      quiet(() => getOpsDb()),
      quiet(() => getOpsNats()),
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
          realized: account.realized,
          unrealized: account.unrealized,
          paperDays: account.paperDays,
          openHalted: account.openHalted,
          haltReason: account.haltReason,
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
          killSource: risk.killSource,
          killAt: risk.killAt,
          params: risk.params,
          breakers: risk.breakers,
          accountAsOf: risk.accountAsOf,
          lastQuoteAt: risk.lastQuoteAt,
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
            kind: item.kind,
            enabled: item.enabled,
            active: item.active,
            successRate: item.successRate,
            latencyMs: item.latencyMs,
            quotaUsed: item.quotaUsed,
            quotaLimit: item.quotaLimit,
            priority: item.priority,
            official: item.official,
            consecutiveFailures: item.consecutiveFailures,
            successCount: item.successCount,
            failureCount: item.failureCount,
            avgLatencyMs: item.avgLatencyMs,
            lastFailureAt: item.lastFailureAt,
            lastError: item.lastError,
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
    limits: limitsBoard
      ? {
          ok: true,
          rows: limitsBoard.map((row) => ({ symbol: row.symbol, consecutive: row.consecutive, status: row.status })),
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
            /* S5 详情窗（§5.2）要的字段：三价依据、规则分对比、同类历史、有效期 */
            side: item.side,
            strategy: item.strategy,
            ruleScore: item.ruleScore,
            aiScore: item.aiScore,
            reason: item.reason,
            entryLow: item.entryLow,
            entryHigh: item.entryHigh,
            atr: item.atr,
            positionPct: item.positionPct,
            validUntil: item.validUntil,
            holdDaysMax: item.holdDaysMax,
            exitKind: item.exitKind,
            exitPrice: item.exitPrice,
            pnlPct: item.pnlPct,
            hasPnl: item.hasPnl,
            holdingDays: item.holdingDays,
            traceId: item.traceId,
            tradeDate: item.tradeDate,
            closedAt: item.closedAt,
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
    orders: orders
      ? {
          ok: true,
          items: orders.map((item) => ({
            id: item.id,
            symbol: item.symbol,
            side: item.side,
            status: item.status,
            price: item.price,
            volume: item.volume,
            filled: item.filled,
            reason: item.reason,
          })),
        }
      : { ok: false, items: [] },
    fills: fills ? { ok: true, items: fills.map((item) => ({ id: item.id, symbol: item.symbol, side: item.side, price: item.price, qty: item.qty, time: item.time })) } : { ok: false, items: [] },
    candidates: candidates
      ? {
          ok: true,
          items: candidates.map((item) => ({
            symbol: item.symbol,
            name: item.name,
            strategy: item.strategy,
            stage: item.stage,
            missReason: item.missReason,
            ruleScore: item.ruleScore,
            aiScore: item.aiScore,
            finalScore: item.finalScore,
            /* S5 §5.6 盘后漏选复盘：这几个是该股此后的实际涨跌，只作事实陈述，不当策略收益 */
            pool: item.pool,
            refPrice: item.refPrice,
            retT1: item.retT1,
            retT3: item.retT3,
            retT5: item.retT5,
            hasT1: item.hasT1,
            hasT3: item.hasT3,
            hasT5: item.hasT5,
          })),
        }
      : { ok: false, items: [] },
    quality: quality
      ? {
          ok: true,
          items: quality.map((item) => ({
            id: item.id,
            domain: item.domain,
            checkName: item.checkName,
            symbol: item.symbol,
            tradeDate: item.tradeDate,
            severity: item.severity,
            source: item.source,
            otherSource: item.otherSource,
            message: item.message,
            hits: item.hits,
            lastSeen: item.lastSeen,
          })),
        }
      : { ok: false, items: [] },
    admission: admission
      ? { ok: true, ready: admission.ready, tradingDays: admission.tradingDays, requiredDays: admission.requiredDays, items: admission.items.map((item) => ({ label: item.label, status: item.status, detail: item.detail })) }
      : { ok: false, ready: false, tradingDays: null, requiredDays: null, items: [] },
    // 影子统计接口只给持有窗口维度的「样本数 / 平均收益」（没有实盘 vs 影子的对照列），
    // 所以这里按 T+N 出卡：两列分别是样本数和该窗口的影子平均收益，口径写在 sub 上。
    shadow: shadow
      ? {
          ok: true,
          items: (shadow.horizons ?? []).map((item) => ({
            name: `T+${item.horizon}`,
            live: item.count,
            shadow: item.avgReturn,
          })),
        }
      : { ok: false, items: [] },
    tasks: tasks ? { ok: true, items: tasks.map((item) => ({ id: item.id, name: item.name, status: item.status, message: item.message })) } : { ok: false, items: [] },
    notices: notices ? { ok: true, items: notices.map((item) => ({ id: item.id, title: item.title, level: item.level, time: item.time })) } : { ok: false, items: [] },
    audit: audit ? { ok: true, items: audit.map((item) => ({ id: String(item.id), action: item.action, target: item.target, time: item.createdAt })) } : { ok: false, items: [] },
    factors:
      focus && factors
        ? { ok: true, symbol: focus, items: factors.flatMap((row) => row.values) }
        : focus
          ? { ok: false, symbol: focus, items: [] }
          : null,
    riskReport: report
      ? {
          ok: true,
          date: report.date,
          summary: report.summary,
          checks: report.checks,
          approved: report.approved,
          rejected: report.rejected,
          adjusted: report.adjusted,
          p99Ms: report.p99Ms,
          rejects: report.rejects,
          events: report.events,
        }
      : { ok: false, summary: '' },
    riskLimits: limits ? { ok: true, items: limits } : null,
    /* S7：脑服务只读聚合；两个接口任一通了就算 ok，界面按字段出，缺的仍写「未接入」 */
    brainP1:
      usage || latency
        ? {
            ok: true,
            usage: usage
              ? {
                  window: usage.window,
                  since: usage.since,
                  total: usage.total,
                  tiers: usage.tiers,
                }
              : null,
            latency: latency
              ? {
                  windowMinutes: latency.windowMinutes,
                  total: latency.total,
                  p50Ms: latency.p50Ms,
                  p95Ms: latency.p95Ms,
                  buckets: latency.buckets,
                }
              : null,
          }
        : { ok: false },
    /* S7：策略绩效与评分-收益散点；两个接口任一通了就算 ok，界面按字段出 */
    strategyP1:
      perf || scatter
        ? {
            ok: true,
            performance: perf ?? null,
            scatter: scatter ?? null,
          }
        : { ok: false },
    /* S7：竞价截面与连板梯队；任一通了就算 ok，缺的字段界面写「未接入」 */
    marketP1:
      auction || ladder
        ? {
            ok: true,
            auction: auction ?? null,
            ladder: ladder ?? null,
          }
        : { ok: false },
    /* S7：GPU 与文件单目录；任一通了就算 ok */
    opsP1:
      gpu || brokerFiles
        ? {
            ok: true,
            gpu: gpu ?? null,
            brokerFiles: brokerFiles ?? null,
          }
        : { ok: false },
    /* P2：昨涨停今溢价 / 20 日均线占比 / K 线缺口 / PG / NATS；任一通了就算 ok */
    marketP2:
      premium || breadth || klineGaps
        ? {
            ok: true,
            premium: premium ?? null,
            breadth: breadth ?? null,
            klineGaps: klineGaps ?? null,
          }
        : { ok: false },
    opsP2:
      db || nats
        ? {
            ok: true,
            db: db ?? null,
            nats: nats ?? null,
          }
        : { ok: false },
  }
}

/** 牌面一行摘要（详情窗按字段出，这行只给远看）。缺的字段直接不写，不填 0。 */
function sourceDetail(item: {
  status: string
  detail: string
  successRate?: number | null
  latencyMs?: number | null
  avgLatencyMs?: number | null
  successCount?: number | null
  failureCount?: number | null
  quotaUsed?: number | null
  quotaLimit?: number | null
  active?: boolean
}): string {
  const bits: string[] = []
  if (item.successRate != null) bits.push(`成功率 ${item.successRate <= 1 ? Math.round(item.successRate * 100) : Math.round(item.successRate)}%`)
  if (item.successCount != null || item.failureCount != null) bits.push(`成 ${item.successCount ?? '—'} 败 ${item.failureCount ?? '—'}`)
  const latency = item.latencyMs ?? item.avgLatencyMs
  if (latency != null) bits.push(`延迟 ${Math.round(latency)}ms`)
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
