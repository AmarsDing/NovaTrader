/**
 * 市场看板 composable（实施文档 D6/D7，2026-11-08-market-board-implementation-plan.md）：
 * 直拉 market API 统一取数、分档节奏自刷、WS 异动帧本地前置。
 *
 * 纪律：
 * - 不经 market store：store 拍平字段不含 advance/flat/amount/maxHeight/scoreCoef，
 *   sectors 仅 top8，且无自刷定时器（30s 是 OverviewView 私有节奏）——看板自建节奏；
 * - KPI/广度用 getSentimentDetail（/api/market/sentiment 全量视图；getSentiment 的
 *   SentimentView 只有 phase/positionScale/up/down/brokenRate 六字段，撑不起六格 KPI）；
 * - 保留上次有效值不清屏（TSP placeholderData 原则）：单点失败只置该源 failed 旗标（首载失败由
 *   对应卡显 missing 文案而非永久骨架），已就绪的旧数据 + NtFreshness 陈旧徽注继续呈现；
 *   仅 fast 组全灭时置 fastError 横幅（部分失败在卡位内表达，不占横幅）；
 * - 节奏：交易时段（inSession，serverNow 口径）fast 15s / mid 30s / slow 60s，
 *   非交易时段统一 120s；页面隐藏暂停，回前台立即补一轮；
 * - WS alert/move 帧本地前置异动流：kind 白名单 + symbol|kind|time 去重
 *   （逻辑照抄 intraday-alerts.panel.vue，去掉候选池/幅度筛选分支——看板恒全量）。
 */
import { computed, ref, watch } from 'vue'
import {
  getBreadth,
  getEmotionPath,
  getIntradayAlerts,
  getLadder,
  getSentimentDetail,
  listLimits,
  listSectors,
  type BreadthSummary,
  type EmotionPath,
  type IntradayAlert,
  type LadderSummary,
  type LimitBoard,
  type SectorRow,
  type Sentiment,
} from '@/api/market'
import { getIndexSnapshot, type IndexQuoteView } from '@/api/indexSnapshot'
import type { LadderMiniModel } from '@/display/NtLadderMini.vue'
import type { LeaderRow } from '@/display/NtLeaderBoard.vue'
import { useSecurityStore } from '@/stores/security'
import { messageOf } from '@/utils/format'
import { formatDate, inSession } from '@/utils/time'
import { serverNow } from '@/utils/server-clock'
import { createPageRefresh } from '@/view/page-refresh'
import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'

/* ---------- 刷新节奏（纯函数，可单测） ---------- */

export const BOARD_CADENCE = {
  /** 指数 / KPI / 梯队 / 异动。 */
  fast: { session: 15000, closed: 120000 },
  /** 情绪路径 / 板块热度。 */
  mid: { session: 30000, closed: 120000 },
  /** MA20 广度。 */
  slow: { session: 60000, closed: 120000 },
} as const

export type BoardCadenceKind = keyof typeof BOARD_CADENCE

export function cadenceMs(kind: BoardCadenceKind, session: boolean): number {
  const c = BOARD_CADENCE[kind]
  return session ? c.session : c.closed
}

/* ---------- 板块领涨/领跌拆分（纯函数） ---------- */

/** 领涨 = avgPct>0 降序前 top；领跌 = avgPct<0 升序前 top；null 与 0 不进榜（不编方向）。 */
export function splitSectorRanks(sectors: SectorRow[], top = 5): { gainers: SectorRow[]; losers: SectorRow[] } {
  const gainers = sectors
    .filter((s) => s.avgPct != null && s.avgPct > 0)
    .sort((a, b) => b.avgPct! - a.avgPct!)
    .slice(0, top)
  const losers = sectors
    .filter((s) => s.avgPct != null && s.avgPct < 0)
    .sort((a, b) => a.avgPct! - b.avgPct!)
    .slice(0, top)
  return { gainers, losers }
}

/* ---------- 梯队精简卡装配（纯函数；口径照抄 limit-ladder.panel.vue:78,100-137） ---------- */

export function aggregateLadderMini(boards: LimitBoard[], ladder: LadderSummary | null): LadderMiniModel {
  const sealed = boards.filter((row) => row.status === 'SEALED')
  const broken = boards.filter((row) => row.status === 'BROKEN')
  const total = sealed.length + broken.length
  const groups = new Map<number, number>()
  for (const row of sealed) {
    const height = row.consecutive && row.consecutive > 0 ? row.consecutive : 1
    groups.set(height, (groups.get(height) ?? 0) + 1)
  }
  const tiers = [...groups.entries()]
    .sort((a, b) => b[0] - a[0])
    .slice(0, 6)
    .map(([height, count]) => ({ height, count }))
  const steps = (ladder?.total ?? [])
    .filter((step) => step.sample > 0)
    .map((step) => ({
      label: `${step.fromHeight} 进 ${step.fromHeight >= 3 ? '4+' : step.fromHeight + 1}`,
      rate: step.rate,
      sample: step.sample,
    }))
  return {
    sealed: sealed.length,
    broken: broken.length,
    sealRate: total > 0 ? sealed.length / total : null,
    tiers,
    steps,
  }
}

/* ---------- 龙头榜装配（纯函数；名称解析交 SymbolName 字典，此处只 ensure） ---------- */

/** 有龙头的板块按 avgPct 降序取前 top（avgPct null 的排尾，不冒充 0）。 */
export function buildLeaderRows(sectors: SectorRow[], top = 8): LeaderRow[] {
  return sectors
    .filter((s) => s.leader)
    .sort((a, b) => (b.avgPct ?? Number.NEGATIVE_INFINITY) - (a.avgPct ?? Number.NEGATIVE_INFINITY))
    .slice(0, top)
    .map((s) => ({
      sectorCode: s.code,
      sectorName: s.name,
      avgPct: s.avgPct,
      leader: s.leader,
      leaderReason: s.leaderReason,
    }))
}

/* ---------- 异动去重键（与 intraday-alerts.panel.vue 同口径） ---------- */

export function alertKey(a: { symbol: string; kind: string; time: string }): string {
  return `${a.symbol}|${a.kind}|${a.time}`
}

const MAX_ALERTS = 100

/* ---------- composable 本体 ---------- */

export function useMarketBoard() {
  const security = useSecurityStore()

  /* ready = 首次成功取到（此后只进不退，旧值保留）；failed = 最近一次尝试失败且尚未就绪——
   * 面板据此把 :loading 与 :ready 分开传：加载中显骨架，失败显 missing（禁 ready 单旗造成永久骨架）。 */
  const sentiment = ref<Sentiment | null>(null)
  const sentimentReady = ref(false)
  const sentimentFailed = ref(false)
  const indices = ref<IndexQuoteView[]>([])
  const indicesReady = ref(false)
  const indicesFailed = ref(false)
  const boards = ref<LimitBoard[]>([])
  const limitsReady = ref(false)
  const limitsFailed = ref(false)
  const ladder = ref<LadderSummary | null>(null)
  const ladderReady = ref(false)
  const alerts = ref<IntradayAlert[]>([])
  const alertsReady = ref(false)
  const alertsFailed = ref(false)
  const liveCount = ref(0)
  const path = ref<EmotionPath | null>(null)
  const pathReady = ref(false)
  const pathFailed = ref(false)
  const sectors = ref<SectorRow[]>([])
  const sectorsReady = ref(false)
  const sectorsFailed = ref(false)
  const breadth = ref<BreadthSummary | null>(null)
  const breadthReady = ref(false)
  const breadthFailed = ref(false)

  const fastError = ref('')
  const loading = ref(false)

  const seen = new Set<string>()
  let disposed = false
  /** 在途定时器（触发即自清，长驻会话不累积哑 id）。 */
  const timers = new Set<ReturnType<typeof setTimeout>>()
  let offPush: (() => void) | undefined
  let unsubscribe: (() => void) | undefined

  function today(): string {
    return formatDate(serverNow())
  }

  /** fast 组：情绪明细 / 指数快照 / 涨停名单 / 晋级率 / 异动流水。 */
  async function loadFast(): Promise<boolean> {
    const [s, ix, lim, lad, al] = await Promise.allSettled([
      getSentimentDetail(),
      getIndexSnapshot(),
      listLimits('UP'),
      getLadder(5),
      getIntradayAlerts(today()),
    ])
    if (disposed) return false
    let ok = false
    if (s.status === 'fulfilled') {
      sentiment.value = s.value
      sentimentReady.value = true
      sentimentFailed.value = false
      ok = true
    } else {
      sentimentFailed.value = true
    }
    if (ix.status === 'fulfilled') {
      indices.value = ix.value.indices
      indicesReady.value = true
      indicesFailed.value = false
      ok = true
    } else {
      indicesFailed.value = true
    }
    if (lim.status === 'fulfilled') {
      boards.value = lim.value
      limitsReady.value = true
      limitsFailed.value = false
      ok = true
    } else {
      limitsFailed.value = true
    }
    if (lad.status === 'fulfilled') {
      ladder.value = lad.value
      ladderReady.value = true
      ok = true
    }
    if (al.status === 'fulfilled') {
      alertsFailed.value = false
      // REST 重拉重置去重集并整表替换（与 intraday-alerts 同口径：回放权威覆盖本地前置）。
      seen.clear()
      const merged: IntradayAlert[] = []
      for (const row of al.value) {
        const k = alertKey(row)
        if (seen.has(k)) continue
        seen.add(k)
        merged.push(row)
      }
      alerts.value = merged.slice(0, MAX_ALERTS)
      alertsReady.value = true
      ok = true
    } else {
      alertsFailed.value = true
    }
    // 全灭才横幅：部分失败已由各卡 missing 态/陈旧徽注在卡位内表达，不占整组横幅。
    const failed = [s, ix, lim, lad, al].find((r) => r.status === 'rejected')
    fastError.value = ok ? '' : failed?.status === 'rejected' ? messageOf(failed.reason) : ''
    return ok
  }

  /** mid 组：盘中情绪路径 / 板块热度（20 档）。 */
  async function loadMid(): Promise<boolean> {
    const [p, sec] = await Promise.allSettled([getEmotionPath(today()), listSectors(20)])
    if (disposed) return false
    let ok = false
    if (p.status === 'fulfilled') {
      path.value = p.value
      pathReady.value = true
      pathFailed.value = false
      ok = true
    } else {
      pathFailed.value = true
    }
    if (sec.status === 'fulfilled') {
      sectors.value = sec.value
      sectorsReady.value = true
      sectorsFailed.value = false
      ok = true
    } else {
      sectorsFailed.value = true
    }
    return ok
  }

  /** slow 组：MA20 广度（20 日窗口）。 */
  async function loadSlow(): Promise<boolean> {
    const b = await Promise.allSettled([getBreadth(20)])
    if (disposed) return false
    if (b[0]!.status === 'fulfilled') {
      breadth.value = b[0]!.value
      breadthReady.value = true
      breadthFailed.value = false
      return true
    }
    breadthFailed.value = true
    return false
  }

  /* createPageRefresh：同 key in-flight 合并 + trailing push（D6）；节奏由下方 loop 驱动。 */
  const fast = createPageRefresh({ key: () => 'fast', read: loadFast, pushIntervalMs: BOARD_CADENCE.fast.session })
  const mid = createPageRefresh({ key: () => 'mid', read: loadMid, pushIntervalMs: BOARD_CADENCE.mid.session })
  const slow = createPageRefresh({ key: () => 'slow', read: loadSlow, pushIntervalMs: BOARD_CADENCE.slow.session })

  function schedule(fn: () => void, ms: number) {
    const t = setTimeout(() => {
      timers.delete(t)
      fn()
    }, ms)
    timers.add(t)
  }

  /** 单组循环：完成后按当前时段重新计延迟；页面隐藏跳过本轮（回前台由 visibilitychange 补）。 */
  function loop(kind: BoardCadenceKind, run: () => Promise<boolean>) {
    const tick = async () => {
      if (disposed) return
      if (!document.hidden) await run()
      if (!disposed) schedule(tick, cadenceMs(kind, inSession(serverNow())))
    }
    schedule(tick, 0)
  }

  function onVisible() {
    if (document.hidden) return
    void fast.refresh()
    void mid.refresh()
    void slow.refresh()
  }

  function onPushMessage(msg: WsPushMessage) {
    if (msg.topic !== 'alert' || msg.event !== 'move') return
    const d = (msg.data ?? {}) as Record<string, unknown>
    const kind = String(d.kind ?? '')
    if (kind !== 'surge' && kind !== 'limit_up' && kind !== 'break') return
    const row: IntradayAlert = {
      symbol: String(d.symbol ?? ''),
      name: String(d.name ?? ''),
      kind,
      time: String(d.time ?? ''),
      pct: typeof d.pct === 'number' ? d.pct : null,
      price: typeof d.price === 'number' ? d.price : null,
    }
    if (!row.symbol) return
    const k = alertKey(row)
    if (seen.has(k)) return
    seen.add(k)
    // WS 帧顺手养名称字典（REST 回填走 api 层 backfill，这里补实时路）。
    if (row.name) security.put([{ code: row.symbol, name: row.name }])
    liveCount.value++
    alerts.value = [row, ...alerts.value].slice(0, MAX_ALERTS)
  }

  function start() {
    loop('fast', () => fast.refresh())
    loop('mid', () => mid.refresh())
    loop('slow', () => slow.refresh())
    unsubscribe = subscribe('alert')
    offPush = onPush(onPushMessage)
    document.addEventListener('visibilitychange', onVisible)
  }

  function stop() {
    disposed = true
    for (const t of timers) clearTimeout(t)
    timers.clear()
    fast.dispose()
    mid.dispose()
    slow.dispose()
    offPush?.()
    unsubscribe?.()
    document.removeEventListener('visibilitychange', onVisible)
  }

  async function refreshAll() {
    loading.value = true
    try {
      await Promise.all([fast.refresh(), mid.refresh(), slow.refresh()])
    } finally {
      loading.value = false
    }
  }

  /* ---------- 派生视图模型 ---------- */

  const gainers = computed(() => splitSectorRanks(sectors.value).gainers)
  const losers = computed(() => splitSectorRanks(sectors.value).losers)
  const ladderMini = computed(() => (limitsReady.value ? aggregateLadderMini(boards.value, ladder.value) : null))
  const leaderRows = computed(() => buildLeaderRows(sectors.value))
  const emotionPoints = computed(() => path.value?.points ?? [])
  const emotionPartial = computed(() => path.value?.partial ?? false)
  /** 头部新鲜度锚点：情绪快照 asOf（ISO 串解析失败按缺失，NtFreshness 自述「时点未知」）。 */
  const sentimentAsOfMs = computed(() => {
    const t = sentiment.value?.asOf ? Date.parse(sentiment.value.asOf) : NaN
    return Number.isFinite(t) ? t : null
  })

  // 板块龙头代码批量 ensure 名称字典（四重去重 + 批量窗在 store 内）。
  watch(sectors, (list) => {
    const codes = list.map((s) => s.leader).filter(Boolean)
    if (codes.length) security.ensure(codes)
  })

  return {
    sentiment,
    sentimentReady,
    sentimentFailed,
    indices,
    indicesReady,
    indicesFailed,
    boards,
    limitsReady,
    limitsFailed,
    ladder,
    ladderReady,
    alerts,
    alertsReady,
    alertsFailed,
    liveCount,
    path,
    pathReady,
    pathFailed,
    sectors,
    sectorsReady,
    sectorsFailed,
    breadth,
    breadthReady,
    breadthFailed,
    fastError,
    loading,
    gainers,
    losers,
    ladderMini,
    leaderRows,
    emotionPoints,
    emotionPartial,
    sentimentAsOfMs,
    start,
    stop,
    refreshAll,
  }
}
