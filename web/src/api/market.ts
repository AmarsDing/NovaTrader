import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import { useSecurityStore, type SecurityBasicPatch } from '@/stores/security'
import type { Bar, LimitBoard, MarketIndicator, SectorRow, Sentiment, SentimentView } from './types/market'

export type { Bar, LimitBoard, MarketIndicator, SectorRow, Sentiment, SentimentView }

/* ---------- 名称字典（方案 §6.2 填充路 2/3 + 附录 B-5） ---------- */

// fetcher 注册惰性一次：禁模块顶层 useSecurityStore()（pinia 初始化时序），首次字典相关调用时挂上。
let basicsFetcherBound = false

function bindBasicsFetcher() {
  if (basicsFetcherBound) return
  basicsFetcherBound = true
  useSecurityStore().registerBasicsFetcher(fetchSecurityBasics)
}

/** 附录 B-5 回填：各响应里现成的 {code,name,...} 归一化后顺手进字典；
 * put 内部判空码、null 名不覆盖旧名、updatedAt 只增。 */
function backfillSecurityBasics(list: SecurityBasicPatch[]): void {
  if (!list.length) return
  bindBasicsFetcher()
  useSecurityStore().put(list)
}

/** 名称字典批量取数（§6.2 填充路 2）：GET /api/market/security-basics（admin 聚合透传 market 服务）。
 * codes 省略/为空 = 全量字典（上限 6000 行）；repeated query 逐参重复（?codes=A&codes=B）。
 * 归一为字典补丁：name 空串→null（缺失非空串）；peTtm/pb/circMv 后端 0=缺失→null（不伪造，§6.2 口径）；
 * totalMv 为 optional 字段（未采集整条缺省），缺省/0 同样归一为 null。 */
export async function fetchSecurityBasics(codes?: string[]): Promise<SecurityBasicPatch[]> {
  bindBasicsFetcher()
  const suffix = codes?.length ? `?${codes.map((c) => `codes=${encodeURIComponent(c)}`).join('&')}` : ''
  const data = await request(`/api/market/security-basics${suffix}`)
  const rows = asList<RawRecord>((data ?? {}) as RawRecord, 'list')
  return rows
    .map((row): SecurityBasicPatch => {
      const updatedAt = Date.parse(pick<string>(row, 'updateTime', 'update_time') || '')
      const peTtm = num(pick(row, 'peTtm', 'pe_ttm'))
      const pb = num(pick(row, 'pb'))
      const circMv = num(pick(row, 'circMv', 'circ_mv'))
      const totalMv = num(pick(row, 'totalMv', 'total_mv'))
      return {
        code: pick<string>(row, 'code') || '',
        name: pick<string>(row, 'name') || null,
        market: pick<string>(row, 'market') || undefined,
        industry: pick<string>(row, 'industry') || undefined,
        st: !!(row.stFlag ?? row.st_flag),
        suspended: !!(row.suspendFlag ?? row.suspend_flag),
        peTtm: peTtm === 0 ? null : peTtm,
        pb: pb === 0 ? null : pb,
        circMv: circMv === 0 ? null : circMv,
        totalMv: totalMv == null || totalMv === 0 ? null : totalMv,
        updatedAt: Number.isFinite(updatedAt) ? updatedAt : undefined,
      }
    })
    .filter((row) => row.code !== '')
}

/** K 线读取窗口（P2-9 缺口回填）。省略时维持默认行数；后端语义：start/end 含两端，limit 从 end 往前取。 */
export interface BarsWindow {
  /** RFC3339 或 YYYY-MM-DD，含该端。前端现有 Bar.time 串可直接回传。 */
  start?: string
  /** RFC3339 或 YYYY-MM-DD（纯日期后端自动扩到当日最后一刻）；省略 = 不限上界。 */
  end?: string
  /** 条数上限（默认 1m 240 / 1d 180；后端默认 1000、上限 10000）。 */
  limit?: number
}

export async function getBars(symbol: string, freq: string, window?: BarsWindow): Promise<Bar[]> {
  const data = await request('/api/market/bars', {
    query: {
      symbol,
      freq,
      limit: window?.limit ?? (freq === '1m' ? 240 : 180),
      adjust: 'NONE',
      start: window?.start || undefined,
      end: window?.end || undefined,
    },
  })
  return asList(data, 'bars').map((raw) => ({
    time: pick<string>(raw, 'time', 'barTime', 'bar_time') || '',
    open: num(pick(raw, 'open')),
    high: num(pick(raw, 'high')),
    low: num(pick(raw, 'low')),
    close: num(pick(raw, 'close')),
    volume: num(pick(raw, 'volume')),
    amount: num(pick(raw, 'amount')),
  }))
}

export async function getIndicators(symbol: string): Promise<MarketIndicator[]> {
  const data = await request('/api/market/indicators', {
    query: { symbol, adjust: 'NONE' },
  })
  return asList(data, 'points').map((raw) => {
    const values = raw.values && typeof raw.values === 'object' ? raw.values : raw
    return {
      time: pick<string>(raw, 'time') || '',
      ma5: num(pick(values, 'ma5')),
      ma10: num(pick(values, 'ma10')),
      ma20: num(pick(values, 'ma20')),
      dif: num(pick(values, 'dif')),
      dea: num(pick(values, 'dea')),
      macd: num(pick(values, 'macd')),
      rsi6: num(pick(values, 'rsi6')),
    }
  })
}

export interface FactorPoint {
  symbol: string
  kind: string
  stale: boolean
  values: { name: string; value: number | null }[]
}

/** 因子截面。kind=capital 时只取资金因子。 */
export async function getFactors(symbol: string, kind = 'capital'): Promise<FactorPoint[]> {
  const data = await request('/api/market/factors', { query: { symbols: symbol, kind } })
  return asList(data, 'rows').map((raw) => {
    const values = raw.values && typeof raw.values === 'object' ? (raw.values as Record<string, unknown>) : {}
    return {
      symbol: pick<string>(raw, 'symbol') || symbol,
      kind: pick<string>(raw, 'kind') || kind,
      stale: !!raw.stale,
      values: Object.entries(values).map(([name, value]) => ({ name, value: num(value) })),
    }
  })
}

export async function getSentiment(): Promise<SentimentView> {
  const raw = await request('/api/market/sentiment')
  return {
    phase: pick<string>(raw, 'phase') || '',
    positionScale: num(pick(raw, 'positionScale', 'position_scale')),
    up: num(pick(raw, 'upCount', 'up_count')),
    down: num(pick(raw, 'downCount', 'down_count')),
    brokenRate: num(pick(raw, 'brokenRate', 'broken_rate')),
    stale: !!(raw && typeof raw === 'object' && (raw as Record<string, unknown>).stale),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

export async function listSectors(top = 8): Promise<SectorRow[]> {
  const data = await request('/api/market/sectors', { query: { top } })
  return asList(data, 'sectors').map((raw) => ({
    code: pick<string>(raw, 'sectorCode', 'sector_code') || '',
    name: pick<string>(raw, 'name') || pick<string>(raw, 'sectorCode', 'sector_code') || '',
    rank: num(pick(raw, 'rank')),
    heat: num(pick(raw, 'heat')),
    avgPct: num(pick(raw, 'avgPct', 'avg_pct')),
    overseas: num(pick(raw, 'overseasImpulse', 'overseas_impulse')),
    leader: pick<string>(raw, 'leaderSymbol', 'leader_symbol') || '',
    /* 批次 A9：proto SectorHeat 全量接线（曾丢弃）——涨停数/上涨占比/成交额/成员数/龙头理由。 */
    upLimitCount: num(pick(raw, 'upLimitCount', 'up_limit_count')),
    advanceRatio: num(pick(raw, 'advanceRatio', 'advance_ratio')),
    amount: num(pick(raw, 'amount')),
    memberCount: num(pick(raw, 'memberCount', 'member_count')),
    leaderReason: pick<string>(raw, 'leaderReason', 'leader_reason') || '',
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }))
}

/** 情绪快照全量（M02 §6）：五阶段 + 计数 + 系数 + 风险等级。 */
export async function getSentimentDetail(): Promise<Sentiment> {
  const data = await request('/api/market/sentiment')
  const raw = (data && typeof data === 'object' ? data : {}) as Record<string, unknown>
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
    upCount: num(pick(raw, 'upCount', 'up_count')),
    downCount: num(pick(raw, 'downCount', 'down_count')),
    brokenCount: num(pick(raw, 'brokenCount', 'broken_count')),
    brokenRate: num(pick(raw, 'brokenRate', 'broken_rate')),
    maxHeight: num(pick(raw, 'maxHeight', 'max_height')),
    profitEffect: num(pick(raw, 'profitEffect', 'profit_effect')),
    advance: num(pick(raw, 'advance')),
    decline: num(pick(raw, 'decline')),
    flat: num(pick(raw, 'flat')),
    amount: num(pick(raw, 'amount')),
    phase: pick<string>(raw, 'phase') || '',
    riskLevel: num(pick(raw, 'riskLevel', 'risk_level')),
    scoreCoef: num(pick(raw, 'scoreCoef', 'score_coef')),
    positionScale: num(pick(raw, 'positionScale', 'position_scale')),
    stale: !!raw.stale,
  }
}

/** 涨跌停列表（M02 §5）：direction = UP / DOWN。 */
export async function listLimits(direction: 'UP' | 'DOWN' = 'UP'): Promise<LimitBoard[]> {
  const data = await request('/api/market/limits', { query: { direction } })
  return asList(data, 'boards', 'limits', 'items').map((raw) => ({
    symbol: pick<string>(raw, 'symbol') || '',
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    direction: pick<string>(raw, 'direction') || direction,
    status: pick<string>(raw, 'status') || '',
    limitPrice: num(pick(raw, 'limitPrice', 'limit_price')),
    firstSealAt: pick<string>(raw, 'firstSealAt', 'first_seal_at') || '',
    lastSealAt: pick<string>(raw, 'lastSealAt', 'last_seal_at') || '',
    lastBreakAt: pick<string>(raw, 'lastBreakAt', 'last_break_at') || undefined,
    openCount: num(pick(raw, 'openCount', 'open_count')),
    sealAmount: num(pick(raw, 'sealAmount', 'seal_amount')),
    consecutive: num(pick(raw, 'consecutive')),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }))
}

/* ---------- S7 P1：集合竞价截面与连板梯队（只读） ---------- */

export interface AuctionRow {
  symbol: string
  /** 竞价涨幅 %。「没取到≠0」：源缺键（新股无昨收等）为 null——0 是平开真值，必须可区分。 */
  auctionPct: number | null
  /** 键恒在（源无条件落键），0=竞价无成交真值；整日无截面看 summary.found。 */
  auctionAmount: number
  /** 无 5 日均量不可算为 null（新股前五日高发），禁止显 0.0 伪装读数。 */
  auctionVolRatio: number | null
  /** 无股本不可算为 null；0 换手虽真实存在但此时行已难入 topRush。 */
  auctionTurnover: number | null
  auctionLimitUp: boolean
  /** 无 09:20-09:25 快照为 null；当前前端无展示位，保留贯通。 */
  auctionUnmatched: number | null
}

export interface AuctionSummary {
  tradeDate: string
  found: boolean
  totalAmount: number
  limitUpCount: number
  limitUps: AuctionRow[]
  topRush: AuctionRow[]
  asOf: string
}

// 「没取到≠0」（P3-10c）：后端 proto optional 省略发送的字段归一为 null；
// auctionAmount 键恒在，?? 0 是冗余哨兵（0=无成交真值，整日缺失看 summary.found）。
function normalizeAuctionRow(raw: RawRecord): AuctionRow {
  return {
    symbol: pick<string>(raw, 'symbol') || '',
    auctionPct: num(pick(raw, 'auctionPct', 'auction_pct')) ?? null,
    auctionAmount: num(pick(raw, 'auctionAmount', 'auction_amount')) ?? 0,
    auctionVolRatio: num(pick(raw, 'auctionVolRatio', 'auction_vol_ratio')) ?? null,
    auctionTurnover: num(pick(raw, 'auctionTurnover', 'auction_turnover')) ?? null,
    auctionLimitUp: !!(raw.auctionLimitUp ?? raw.auction_limit_up),
    auctionUnmatched: num(pick(raw, 'auctionUnmatched', 'auction_unmatched')) ?? null,
  }
}

/** 集合竞价截面（M02，09:25 落库）。found=false 表示当日还没有竞价因子。
 * 哨兵钉死（P3-10c）：totalAmount/limitUpCount 的 ?? 0 冗余——整日缺失由 found=false 表达且三处消费全先判 found；
 * found=true 时 totalAmount 仅正额行入和（0=真 0），limitUpCount 是真计数（0 家=真实市况）。 */
export async function getAuction(top = 10): Promise<AuctionSummary> {
  const data = await request('/api/market/auction', { query: { top } })
  const raw = (data ?? {}) as RawRecord
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    totalAmount: num(pick(raw, 'totalAmount', 'total_amount')) ?? 0,
    limitUpCount: num(pick(raw, 'limitUpCount', 'limit_up_count')) ?? 0,
    limitUps: asList<RawRecord>(raw, 'limitUps', 'limit_ups').map(normalizeAuctionRow),
    topRush: asList<RawRecord>(raw, 'topRush', 'top_rush').map(normalizeAuctionRow),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

export interface LadderStep {
  fromHeight: number
  sample: number
  succeeded: number
  rate: number
}

export interface LadderDay {
  tradeDate: string
  steps: LadderStep[]
}

export interface LadderSummary {
  days: LadderDay[]
  total: LadderStep[]
}

// 哨兵钉死（P3-10c）：fromHeight 后端固定 steps map 恒为 1/2/3；sample/succeeded 真计数（0=基准日无 N 板/次日无一封住）；
// rate 的 0 哨兵仅存于 sample=0，消费方（detail.ts）恒先判 sample===0 写「无样本」——四字段 ?? 0 冗余，勿改 ?? null。
function normalizeLadderStep(raw: RawRecord): LadderStep {
  return {
    fromHeight: num(pick(raw, 'fromHeight', 'from_height')) ?? 0,
    sample: num(pick(raw, 'sample')) ?? 0,
    succeeded: num(pick(raw, 'succeeded')) ?? 0,
    rate: num(pick(raw, 'rate')) ?? 0,
  }
}

/** 连板梯队晋级率（M02 跨日对比）。sample=0 的档由界面写「无样本」。 */
export async function getLadder(days = 5): Promise<LadderSummary> {
  const data = await request('/api/market/ladder', { query: { days } })
  const raw = (data ?? {}) as RawRecord
  return {
    days: asList<RawRecord>(raw, 'days').map((d) => ({
      tradeDate: pick<string>(d, 'tradeDate', 'trade_date') || '',
      steps: asList<RawRecord>(d, 'steps').map(normalizeLadderStep),
    })),
    total: asList<RawRecord>(raw, 'total').map(normalizeLadderStep),
  }
}

/* ---------- P2：K 线缺口 / 昨涨停今溢价 / 20 日均线占比（只读） ---------- */

export interface PremiumRow {
  symbol: string
  name: string
  tradeDate: string
  baseDate: string
  consecutive: number
  baseClose: number
  open: number
  close: number
  openPremium: number
  closePremium: number
  highPremium: number
  limitAgain: boolean
  hasClose: boolean
  source: string
}

export interface PremiumDay {
  tradeDate: string
  baseDate: string
  /** 真计数：0=昨日涨停 0 只的真实市况。 */
  sample: number
  /** 真计数：0=今日无一只再板。 */
  limitAgain: number
  /** 开盘溢价均值（分数）。「没取到≠0」：开盘口径样本空（竞价整批缺）为 null——0 是真均值无法冒充。 */
  openAvg: number | null
  /** 缺失口径由 hasClose=false 表达，消费方先判 hasClose；0=全体收平真值。 */
  closeAvg: number
  /** 同 closeAvg 的 hasClose 口径。 */
  closeWin: number
  /** 最高溢价均值（分数）。快照行收盘补齐前 High 全缺为 null。 */
  highAvg: number | null
  hasClose: boolean
}

export interface PremiumSummary {
  tradeDate: string
  found: boolean
  live: boolean
  summary: PremiumDay | null
  days: PremiumDay[]
  rows: PremiumRow[]
  asOf: string
}

// sample/limitAgain 真计数（0 是真实市况）；openAvg/highAvg 后端 optional 省略→null 贯通；
// closeAvg/closeWin 缺失口径由 hasClose 表达（P3-10c 哨兵钉链）。
function normalizePremiumDay(raw: RawRecord): PremiumDay {
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    baseDate: pick<string>(raw, 'baseDate', 'base_date') || '',
    sample: num(pick(raw, 'sample')) ?? 0,
    limitAgain: num(pick(raw, 'limitAgain', 'limit_again')) ?? 0,
    openAvg: num(pick(raw, 'openAvg', 'open_avg')) ?? null,
    closeAvg: num(pick(raw, 'closeAvg', 'close_avg')) ?? 0,
    closeWin: num(pick(raw, 'closeWin', 'close_win')) ?? 0,
    highAvg: num(pick(raw, 'highAvg', 'high_avg')) ?? null,
    hasClose: !!raw.hasClose,
  }
}

/** 昨日涨停今日溢价（P2 快照表；live=true 表示快照未落、实时兜底）。溢价率为分数（0.0323=3.23%）。 */
export async function getLimitPremium(days = 5, top = 50): Promise<PremiumSummary> {
  const data = await request('/api/market/limit-premium', { query: { days, top } })
  const raw = (data ?? {}) as RawRecord
  const rows = asList<RawRecord>(raw, 'rows').map((r) => ({
    symbol: pick<string>(r, 'symbol') || '',
    name: pick<string>(r, 'name') || '',
    tradeDate: pick<string>(r, 'tradeDate', 'trade_date') || '',
    baseDate: pick<string>(r, 'baseDate', 'base_date') || '',
    consecutive: num(pick(r, 'consecutive')) ?? 0,
    baseClose: num(pick(r, 'baseClose', 'base_close')) ?? 0,
    open: num(pick(r, 'open')) ?? 0,
    close: num(pick(r, 'close')) ?? 0,
    openPremium: num(pick(r, 'openPremium', 'open_premium')) ?? 0,
    closePremium: num(pick(r, 'closePremium', 'close_premium')) ?? 0,
    highPremium: num(pick(r, 'highPremium', 'high_premium')) ?? 0,
    limitAgain: !!r.limitAgain,
    hasClose: !!r.hasClose,
    source: pick<string>(r, 'source') || '',
  }))
  // 附录 B-5 回填：行内 symbol/name 顺手进名称字典。
  backfillSecurityBasics(rows.map((r) => ({ code: r.symbol, name: r.name || null })))
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    live: !!raw.live,
    summary: raw.summary ? normalizePremiumDay(raw.summary as RawRecord) : null,
    days: asList<RawRecord>(raw, 'days').map(normalizePremiumDay),
    rows,
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

export interface BreadthDay {
  tradeDate: string
  window: number
  above: number
  below: number
  flat: number
  total: number
  ratio: number
  live: boolean
  asOf: string
}

export interface BreadthSummary {
  tradeDate: string
  found: boolean
  latest: BreadthDay | null
  days: BreadthDay[]
}

/** 20 日均线上占比（市场广度）。ratio 为分数。
 * 哨兵钉死（P3-10c）：window 恒 20（后端口径常量，?? 20 是约定兜底）；above/below/flat/total 真计数
 * （后端保证出现的行 total>0）；ratio=0 是真实市况（全市场在 MA20 下方），空态由 found=false 表达，勿改 ?? null。 */
export async function getBreadth(days = 10): Promise<BreadthSummary> {
  const data = await request('/api/market/breadth', { query: { days } })
  const raw = (data ?? {}) as RawRecord
  const norm = (r: RawRecord): BreadthDay => ({
    tradeDate: pick<string>(r, 'tradeDate', 'trade_date') || '',
    window: num(pick(r, 'window')) ?? 20,
    above: num(pick(r, 'above')) ?? 0,
    below: num(pick(r, 'below')) ?? 0,
    flat: num(pick(r, 'flat')) ?? 0,
    total: num(pick(r, 'total')) ?? 0,
    ratio: num(pick(r, 'ratio')) ?? 0,
    live: !!r.live,
    asOf: pick<string>(r, 'asOf', 'as_of') || '',
  })
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    latest: raw.latest ? norm(raw.latest as RawRecord) : null,
    days: asList<RawRecord>(raw, 'days').map(norm),
  }
}

export interface KlineGapDay {
  tradeDate: string
  universe: number
  missing: number
  present: number
  sample: string[]
  asOf: string
}

export interface KlineGapsSummary {
  tradeDate: string
  found: boolean
  universe: number
  days: KlineGapDay[]
  missingTotal: number
  asOf: string
}

/** K 线连续性检查（交易日历开市但无 1d 收盘行的缺口日）。
 * 哨兵钉死（P3-10c）：universe/missing/present/missingTotal 全部真计数（0=无缺口真值），
 * 缺口有无由 found 先行表达（hud found=false 显「K 线无缺口」），?? 0 冗余勿改 ?? null。 */
export async function getKlineGaps(days = 5): Promise<KlineGapsSummary> {
  const data = await request('/api/market/kline-gaps', { query: { days } })
  const raw = (data ?? {}) as RawRecord
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    universe: num(pick(raw, 'universe')) ?? 0,
    days: asList<RawRecord>(raw, 'days').map((d) => ({
      tradeDate: pick<string>(d, 'tradeDate', 'trade_date') || '',
      universe: num(pick(d, 'universe')) ?? 0,
      missing: num(pick(d, 'missing')) ?? 0,
      present: num(pick(d, 'present')) ?? 0,
      sample: asList<string>(d, 'sample'),
      asOf: pick<string>(d, 'asOf', 'as_of') || '',
    })),
    missingTotal: num(pick(raw, 'missingTotal', 'missing_total')) ?? 0,
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

/* ---------- 复盘体系 P0-3：盘中情绪路径 ---------- */

export interface EmotionPathPoint {
  time: string
  auction: boolean
  up: number
  down: number
  broken: number
  brokenRate: number
  maxHeight: number
  profitEffect: number
  advance: number
  decline: number
  amount: number
  phase: string
  scoreCoef: number
  stale: boolean
}

export interface EmotionPath {
  date: string
  auctionFound: boolean
  auctionTotalAmount: number
  auctionLimitUpCount: number
  /** 昨涨停竞价平均涨幅 %。没取到为 null（不落 0）。 */
  prevTierAuctionPct: number | null
  prevTierAuctionSample: number
  points: EmotionPathPoint[]
  partial: boolean
  missing: string[]
}

export async function getEmotionPath(date: string): Promise<EmotionPath> {
  // 哨兵钉死（P3-10c）：auction 两字段缺失由 auctionFound 先行表达且面板先判；
  // points 只含 09:25 竞价占位与已落库截面（没存的时点根本不进 points，缺失由 missing 列表表达）——
  // 点位 ?? 0 是冗余兜底（截面真值，0=该时点真实读数），不会把「没存」伪装成全 0 截面。
  const data = await request('/api/market/emotion-path', { query: { date } })
  const raw = (data ?? {}) as RawRecord
  return {
    date: pick<string>(raw, 'date') || '',
    auctionFound: !!(raw.auctionFound ?? raw.auction_found),
    auctionTotalAmount: num(pick(raw, 'auctionTotalAmount', 'auction_total_amount')) ?? 0,
    auctionLimitUpCount: num(pick(raw, 'auctionLimitUpCount', 'auction_limit_up_count')) ?? 0,
    prevTierAuctionPct: num(pick(raw, 'prevTierAuctionPct', 'prev_tier_auction_pct')) ?? null,
    prevTierAuctionSample: num(pick(raw, 'prevTierAuctionSample', 'prev_tier_auction_sample')) ?? 0,
    points: asList(raw, 'points').map((p) => ({
      time: pick<string>(p, 'time') || '',
      auction: !!p.auction,
      up: num(pick(p, 'up')) ?? 0,
      down: num(pick(p, 'down')) ?? 0,
      broken: num(pick(p, 'broken')) ?? 0,
      brokenRate: num(pick(p, 'brokenRate', 'broken_rate')) ?? 0,
      maxHeight: num(pick(p, 'maxHeight', 'max_height')) ?? 0,
      profitEffect: num(pick(p, 'profitEffect', 'profit_effect')) ?? 0,
      advance: num(pick(p, 'advance')) ?? 0,
      decline: num(pick(p, 'decline')) ?? 0,
      amount: num(pick(p, 'amount')) ?? 0,
      phase: pick<string>(p, 'phase') || '',
      scoreCoef: num(pick(p, 'scoreCoef', 'score_coef')) ?? 0,
      stale: !!p.stale,
    })),
    partial: !!raw.partial,
    missing: asList(raw, 'missing').map((m) => String(m)),
  }
}

/* ---------- 视频落地 P0：单券快照（GetQuote，只读） ---------- */

export interface QuoteLevel {
  price: number
  volume: number
}

/** 单券快照：数值 0 与空串一律按「未知」处理（proto 零值不猜），source/stale/found 是口径依据。 */
export interface QuoteView {
  symbol: string
  name: string
  market: string
  tradeStatus: string
  last: number
  chg: number
  pctChg: number
  open: number
  high: number
  low: number
  prevClose: number
  volume: number
  amount: number
  turnoverRate: number
  amplitude: number
  bid: QuoteLevel[]
  ask: QuoteLevel[]
  peTtm: number
  pb: number
  circMv: number
  totalMv: number
  limitUp: number
  limitDown: number
  noLimit: boolean
  /** 附录 B-2（optional）：52 周高/低前复权口径窗口值；服务端窗口不足不返回 → null 显「—」（数据纪律，禁虚构）。 */
  high52w: number | null
  low52w: number | null
  /** snap=盘中快照；1d=最近日线回退；空=无数据。 */
  source: string
  asOf: string
  stale: boolean
  found: boolean
}

function quoteLevels(list: QuoteLevel[]): QuoteLevel[] {
  return list.filter((l) => l.price > 0)
}

/** 候选池租约行（GetWatchlist 读口，W3-B1）：owner + 排序标的 + 到期时刻；到期/无租约=缺席（缺席即缺失）。 */
export interface WatchLeaseView {
  owner: string
  symbols: string[]
  expiresAt: string
}

/** 读取候选池租约（只读）：owner 过滤，空=全部 owner 租约——登录即可读，单机私有部署下各桌面互见彼此与 trade.* 进程的池（WS 不广播租约，此为本读口独有的有意暴露面）。 */
export async function getWatchlist(owner?: string): Promise<WatchLeaseView[]> {
  const data = await request('/api/market/watchlist', owner ? { query: { owner } } : {})
  return asList(data, 'leases')
    .map((row) => ({
      owner: pick<string>(row, 'owner') || '',
      symbols: asList(row.symbols).map((s) => String(s)),
      expiresAt: pick<string>(row, 'expiresAt') || pick<string>(row, 'expires_at') || '',
    }))
    .filter((row) => row.owner)
}

/** 登记候选池（同 owner 整体替换并续租），返回到期时刻。 */
export async function setWatchlist(owner: string, symbols: string[]): Promise<string> {
  const data = (await request('/api/market/watchlist', {
    method: 'POST',
    json: { owner, symbols },
  })) as RawRecord
  return pick<string>(data, 'expiresAt') || pick<string>(data, 'expires_at') || ''
}

export async function getQuote(symbol: string): Promise<QuoteView> {
  // 哨兵钉死（P3-10c）：全字段遵守接口契约「0=未知」（proto 零值不猜，消费面板逐处 truthy/比较守卫显「未知」）；
  // prevClose=0 还是 chg/pctChg/amplitude/limit 派生的不可算总闸门。?? 0 即契约实现，勿改 ?? null（会把真 0 吞成「—」）。
  const data = await request('/api/market/quote', { query: { symbol } })
  const raw = (data ?? {}) as RawRecord
  const quoteSymbol = pick<string>(raw, 'symbol') || symbol
  const quoteName = pick<string>(raw, 'name') || ''
  const quoteMarket = pick<string>(raw, 'market') || ''
  // 附录 B-5 回填：单券快照的 name/market 顺手进名称字典。
  backfillSecurityBasics([{ code: quoteSymbol, name: quoteName || null, market: quoteMarket || undefined }])
  return {
    symbol: quoteSymbol,
    name: quoteName,
    market: quoteMarket,
    tradeStatus: pick<string>(raw, 'tradeStatus', 'trade_status') || '',
    last: num(pick(raw, 'last')) ?? 0,
    chg: num(pick(raw, 'chg')) ?? 0,
    pctChg: num(pick(raw, 'pctChg', 'pct_chg')) ?? 0,
    open: num(pick(raw, 'open')) ?? 0,
    high: num(pick(raw, 'high')) ?? 0,
    low: num(pick(raw, 'low')) ?? 0,
    prevClose: num(pick(raw, 'prevClose', 'prev_close')) ?? 0,
    volume: num(pick(raw, 'volume')) ?? 0,
    amount: num(pick(raw, 'amount')) ?? 0,
    turnoverRate: num(pick(raw, 'turnoverRate', 'turnover_rate')) ?? 0,
    amplitude: num(pick(raw, 'amplitude')) ?? 0,
    bid: quoteLevels(asList<RawRecord>(raw, 'bid').map((l) => ({
      price: num(pick(l, 'price')) ?? 0,
      volume: num(pick(l, 'volume')) ?? 0,
    }))),
    ask: quoteLevels(asList<RawRecord>(raw, 'ask').map((l) => ({
      price: num(pick(l, 'price')) ?? 0,
      volume: num(pick(l, 'volume')) ?? 0,
    }))),
    peTtm: num(pick(raw, 'peTtm', 'pe_ttm')) ?? 0,
    pb: num(pick(raw, 'pb')) ?? 0,
    circMv: num(pick(raw, 'circMv', 'circ_mv')) ?? 0,
    totalMv: num(pick(raw, 'totalMv', 'total_mv')) ?? 0,
    limitUp: num(pick(raw, 'limitUpPrice', 'limit_up_price')) ?? 0,
    limitDown: num(pick(raw, 'limitDownPrice', 'limit_down_price')) ?? 0,
    noLimit: !!(raw.noLimit ?? raw.no_limit),
    // B-2 optional 缺窗不序列化：num() 返 null 直落「—」，不走 ?? 0（0=「未知」口径会把 structural 缺窗冒充成行情零值）。
    high52w: num(pick(raw, 'high52w', 'high_52w')),
    low52w: num(pick(raw, 'low52w', 'low_52w')),
    source: pick<string>(raw, 'source') || '',
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
    stale: !!raw.stale,
    found: !!raw.found,
  }
}

/* ---------- 复盘体系 P0-4：盘中异动流水 ---------- */

export type IntradayAlertKind = 'surge' | 'limit_up' | 'break'

export interface IntradayAlert {
  symbol: string
  name: string
  kind: IntradayAlertKind
  time: string
  /** 触发幅度 %（surge 为窗口涨幅，封/开板为当日涨幅）。没取到为 null。 */
  pct: number | null
  price: number | null
}

export async function getIntradayAlerts(date: string, minPct = 0, watchOnly = false): Promise<IntradayAlert[]> {
  const data = await request('/api/market/intraday-alerts', {
    query: { date, min_pct: minPct || undefined, watch_only: watchOnly || undefined },
  })
  const alerts = asList(data, 'alerts').map((raw) => ({
    symbol: pick<string>(raw, 'symbol') || '',
    name: pick<string>(raw, 'name') || '',
    kind: (pick<string>(raw, 'kind') || 'surge') as IntradayAlertKind,
    time: pick<string>(raw, 'time') || '',
    pct: num(pick(raw, 'pct')) ?? null,
    price: num(pick(raw, 'price')) ?? null,
  }))
  // 附录 B-5 回填：异动行的 symbol/name 顺手进名称字典。
  backfillSecurityBasics(alerts.map((a) => ({ code: a.symbol, name: a.name || null })))
  return alerts
}

/* ---------- 目录册增补 B2（只读）：席位胜率时序（GetSeatStats） ----------
 * 全市场口径：按基准日取滚动窗口（60 交易日）席位胜率，无 symbol 维度——
 * 侧栏席位卡明确标注全市场，不冒充个股关联。 */

export interface SeatStatRow {
  seat: string
  /** 统计基准日（窗口右端，yyyy-mm-dd）。 */
  asOf: string
  /** 窗口交易日数（60）。 */
  lookback: number
  /** 窗口内上榜次数（含 T+5 未发生）。 */
  sample: number
  /** 其中 T+5 已发生的子样本数。 */
  sampleT5: number
  /** T+1/T+5 胜率与收益：proto optional——有效样本为零/T+5 未发生时省略发送，
   *  null 显「—」（「没取到≠0」，0 无歧义但缺席≠0）。 */
  winRateT1: number | null
  avgRetT1: number | null
  medRetT1: number | null
  winRateT5: number | null
  avgRetT5: number | null
  medRetT5: number | null
  source: string
}

export interface SeatStatsReply {
  tradeDate: string
  /** false=该基准日 seat_stats 无行（当日龙虎榜空或任务未跑）——显「无行」不告警。 */
  found: boolean
  rows: SeatStatRow[]
  asOf: string
}

export async function getSeatStats(tradeDate = '', top = 50): Promise<SeatStatsReply> {
  const data = await request('/api/market/seat-stats', {
    query: { trade_date: tradeDate || undefined, top: top || undefined },
  })
  const raw = (data ?? {}) as RawRecord
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    found: !!raw.found,
    rows: asList<RawRecord>(raw, 'rows').map((row) => ({
      seat: pick<string>(row, 'seat') || '',
      asOf: pick<string>(row, 'asOf', 'as_of') || '',
      lookback: num(pick(row, 'lookback')) ?? 0,
      sample: num(pick(row, 'sample')) ?? 0,
      sampleT5: num(pick(row, 'sampleT5', 'sample_t5')) ?? 0,
      winRateT1: num(pick(row, 'winRateT1', 'win_rate_t1')),
      avgRetT1: num(pick(row, 'avgRetT1', 'avg_ret_t1')),
      medRetT1: num(pick(row, 'medRetT1', 'med_ret_t1')),
      winRateT5: num(pick(row, 'winRateT5', 'win_rate_t5')),
      avgRetT5: num(pick(row, 'avgRetT5', 'avg_ret_t5')),
      medRetT5: num(pick(row, 'medRetT5', 'med_ret_t5')),
      source: pick<string>(row, 'source') || '',
    })),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
  }
}

/* ---------- 目录册增补 B3（只读）：筹码分布 CYQ（GetChipDistribution） ----------
 * 换手衰减模型纯计算（前复权日K×换手率），不落库。 */

export interface ChipDistribution {
  symbol: string
  /** 四态：ok / missing（库中无该券或窗口无日线）/ not_meaningful（数据不足不可算）/ error（请求非法）。 */
  status: string
  /** status≠ok 时的显式原因文案（后端下发，原样透显，不自封）。 */
  reason: string
  /** 价格与衍生列 proto optional——status≠ok 全部缺席（null），不产假数（「没取到≠0」）。 */
  profit: number | null
  avgCost: number | null
  cost90Lo: number | null
  cost90Hi: number | null
  cost70Lo: number | null
  cost70Hi: number | null
  concentration90: number | null
  concentration70: number | null
  peakPrice: number | null
  price: number | null
  /** 事实列照常回波（非 ok 时为窗口实际入算行数/累计换手%）。 */
  daysUsed: number
  cumTurnoverPct: number
  windowStart: string
  windowEnd: string
}

export async function getChipDistribution(symbol: string): Promise<ChipDistribution> {
  const data = await request('/api/market/chip-distribution', { query: { symbol } })
  const raw = (data ?? {}) as RawRecord
  return {
    symbol: pick<string>(raw, 'symbol') || symbol,
    status: pick<string>(raw, 'status') || '',
    reason: pick<string>(raw, 'reason') || '',
    profit: num(pick(raw, 'profit')),
    avgCost: num(pick(raw, 'avgCost', 'avg_cost')),
    cost90Lo: num(pick(raw, 'cost90Lo', 'cost90_lo')),
    cost90Hi: num(pick(raw, 'cost90Hi', 'cost90_hi')),
    cost70Lo: num(pick(raw, 'cost70Lo', 'cost70_lo')),
    cost70Hi: num(pick(raw, 'cost70Hi', 'cost70_hi')),
    concentration90: num(pick(raw, 'concentration90')),
    concentration70: num(pick(raw, 'concentration70')),
    peakPrice: num(pick(raw, 'peakPrice', 'peak_price')),
    price: num(pick(raw, 'price')),
    daysUsed: num(pick(raw, 'daysUsed', 'days_used')) ?? 0,
    cumTurnoverPct: num(pick(raw, 'cumTurnoverPct', 'cum_turnover_pct')) ?? 0,
    windowStart: pick<string>(raw, 'windowStart', 'window_start') || '',
    windowEnd: pick<string>(raw, 'windowEnd', 'window_end') || '',
  }
}
