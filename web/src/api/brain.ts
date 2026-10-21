import { request } from '@/api/http'
import { asList, num, pick, type RawRecord } from '@/utils/format'
import { useSecurityStore } from '@/stores/security'
import type {
  Analysis,
  AskReply,
  Briefing,
  BriefingItem,
  DimensionScore,
  EvidenceItem,
  Explanation,
  NewsLineRow,
  PositionViewRow,
  TierStats,
  WatchItemRow,
} from './types/brain'

export type {
  Analysis,
  AskReply,
  Briefing,
  BriefingItem,
  DimensionScore,
  EvidenceItem,
  Explanation,
  NewsLineRow,
  PositionViewRow,
  TierStats,
  WatchItemRow,
} from './types/brain'

/* ---------- 晨报（M05 §晨报） ---------- */

function normalizeBriefingItem(raw: RawRecord): BriefingItem {
  return {
    kind: pick<string>(raw, 'kind', 'type', 'category') || '',
    title: pick<string>(raw, 'title', 'subject') || '',
    body: pick<string>(raw, 'body', 'text', 'content') || '',
    symbol: pick<string>(raw, 'symbol', 'code') || '',
  }
}

function pickStrings(raw: unknown): string[] {
  return Array.isArray(raw) ? raw.map((v) => String(v ?? '')).filter(Boolean) : []
}

function normalizeBriefing(raw: RawRecord): Briefing {
  const headline = pick<string>(raw, 'headline') || ''
  const marketView = pick<string>(raw, 'marketView', 'market_view') || ''
  const risks = pickStrings(raw.risks)
  const watchlist: WatchItemRow[] = asList(raw, 'watchlist').map((w) => ({
    symbol: pick<string>(w, 'symbol') || '',
    reason: pick<string>(w, 'reason') || '',
    evidence: pickStrings(w.evidence),
  }))
  const positions: PositionViewRow[] = asList(raw, 'positions').map((p) => ({
    symbol: pick<string>(p, 'symbol') || '',
    view: pick<string>(p, 'view') || '',
    evidence: pickStrings(p.evidence),
  }))
  const news: NewsLineRow[] = asList(raw, 'news').map((n) => ({
    id: pick<string>(n, 'id') || '',
    title: pick<string>(n, 'title') || '',
    importance: num(pick(n, 'importance')),
    symbol: pick<string>(n, 'symbol') || '',
  }))
  const evidence: EvidenceItem[] = asList(raw, 'evidence').map((e) => ({
    field: pick<string>(e, 'field') || '',
    span: pick<string>(e, 'span') || '',
    verdict: pick<string>(e, 'verdict') || '',
    match: pick<string>(e, 'match') || '',
  }))
  // 兼容派生：监控/overview 的 summary/items 由真实字段折算。
  const items: BriefingItem[] = [
    ...(marketView ? [{ kind: '大盘', title: marketView, body: '', symbol: '' }] : []),
    ...risks.map((r) => ({ kind: '风险', title: r, body: '', symbol: '' })),
    ...watchlist.map((w) => ({ kind: '关注', title: w.reason, body: '', symbol: w.symbol })),
    ...positions.map((p) => ({ kind: '持仓', title: p.view, body: '', symbol: p.symbol })),
  ]
  return {
    date: pick<string>(raw, 'date', 'tradeDate', 'trade_date') || '',
    degraded: !!(raw.degraded || raw.Degraded),
    headline,
    marketView,
    risks,
    watchlist,
    positions,
    news,
    reason: pick<string>(raw, 'reason') || '',
    evidence,
    omittedCount: num(pick(raw, 'omittedCount', 'omitted_count')),
    createdAt: pick<string>(raw, 'createdAt', 'created_at') || '',
    summary: pick<string>(raw, 'summary', 'digest') || headline,
    items: items.length ? items : asList(raw, 'items', 'sections').map(normalizeBriefingItem),
    generatedAt: pick<string>(raw, 'generatedAt', 'generated_at', 'asOf', 'as_of') || pick<string>(raw, 'createdAt', 'created_at') || '',
    stale: !!raw.stale,
  }
}

/** 读晨报；date 为空串 = 今天（后端口径）。 */
export async function getBriefing(date: string): Promise<Briefing> {
  const data = await request(`/api/brain/briefing/${encodeURIComponent(date || 'latest')}`)
  return normalizeBriefing((data && typeof data === 'object' ? data : {}) as RawRecord)
}

/** 触发晨报生成（owner 写操作）。 */
export async function runBriefing(body: Record<string, unknown> = {}): Promise<Briefing> {
  const data = await request('/api/brain/briefing', { method: 'POST', json: body })
  return normalizeBriefing((data && typeof data === 'object' ? data : {}) as RawRecord)
}

/* ---------- 分析 / 追问 ---------- */

function normalizeDims(raw: unknown): DimensionScore[] {
  if (!Array.isArray(raw)) {
    // 部分后端把四维写成对象 {技术面: 88, 消息面: {...}}；这里兼容两种。
    if (raw && typeof raw === 'object') {
      return Object.entries(raw as RawRecord).map(([name, value]) => {
        const score = num(value)
        const comment =
          score === null && value && typeof value === 'object'
            ? pick<string>(value, 'comment', 'reason', 'text') || ''
            : ''
        return { name, score, comment }
      })
    }
    return []
  }
  return (raw as RawRecord[]).map((row) => ({
    name: pick<string>(row, 'name', 'dimension', 'dim') || '',
    score: num(pick(row, 'score')),
    comment: pick<string>(row, 'comment', 'reason', 'text') || '',
  }))
}

function normalizeAnalysis(raw: RawRecord): Analysis {
  const symbol = pick<string>(raw, 'symbol', 'code') || ''
  const name = pick<string>(raw, 'name', 'stockName', 'stock_name') || ''
  // 附录 B-5 回填：分析结果的 symbol/name 顺手进名称字典（put 判空码、null 名不覆盖旧名）。
  useSecurityStore().put([{ code: symbol, name: name || null }])
  return {
    symbol,
    name,
    score: num(pick(raw, 'score', 'totalScore', 'total_score')),
    verdict: pick<string>(raw, 'verdict', 'advice', 'action') || '',
    dimensions: normalizeDims(raw.dimensions ?? raw.dims),
    reasons: Array.isArray(raw.reasons) ? raw.reasons.map(String) : [],
    risks: Array.isArray(raw.risks) ? raw.risks.map(String) : [],
    generatedAt: pick<string>(raw, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
    model: pick<string>(raw, 'model', 'modelName', 'model_name') || '',
  }
}

export async function analyzeBrain(body: Record<string, unknown>): Promise<Analysis> {
  const data = await request('/api/brain/analyze', { method: 'POST', json: body })
  return normalizeAnalysis((data && typeof data === 'object' ? data : {}) as RawRecord)
}

export async function analyzeBatch(symbols: string[], body: Record<string, unknown> = {}): Promise<Analysis[]> {
  const data = await request('/api/brain/analyze/batch', { method: 'POST', json: { ...body, symbols } })
  return asList(data, 'items', 'results', 'analyses').map(normalizeAnalysis)
}

export async function askBrain(body: Record<string, unknown>): Promise<AskReply> {
  const data = await request('/api/brain/ask', { method: 'POST', json: body })
  const raw = (data && typeof data === 'object' ? data : {}) as RawRecord
  return {
    answer: pick<string>(raw, 'answer', 'text', 'reply') || '',
    citations: Array.isArray(raw.citations) ? raw.citations.map(String) : [],
    generatedAt: pick<string>(raw, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
    model: pick<string>(raw, 'model', 'modelName', 'model_name') || '',
  }
}

export async function explainBrain(body: Record<string, unknown>): Promise<Explanation> {
  const data = await request('/api/brain/explain', { method: 'POST', json: body })
  const raw = (data && typeof data === 'object' ? data : {}) as RawRecord
  return {
    symbol: pick<string>(raw, 'symbol', 'code') || '',
    text: pick<string>(raw, 'text', 'explanation', 'answer') || '',
    reasons: Array.isArray(raw.reasons) ? raw.reasons.map(String) : [],
    generatedAt: pick<string>(raw, 'generatedAt', 'generated_at', 'asOf', 'as_of') || '',
    model: pick<string>(raw, 'model', 'modelName', 'model_name') || '',
  }
}

/* ---------- 分层运行统计（brain-monitor） ---------- */

export async function brainStats(): Promise<TierStats[]> {
  const data = await request('/api/brain/stats')
  return asList(data, 'tiers', 'items', 'stats').map(normalizeTier)
}

function normalizeTier(raw: RawRecord): TierStats {
  return {
    tier: pick<string>(raw, 'tier') || '',
    model: pick<string>(raw, 'model') || '',
    configured: !!raw.configured,
    calls: num(pick(raw, 'calls')),
    failures: num(pick(raw, 'failures')),
    p50Ms: num(pick(raw, 'p50Ms', 'p50_ms')),
    p95Ms: num(pick(raw, 'p95Ms', 'p95_ms')),
    inFlight: num(pick(raw, 'inFlight', 'in_flight')),
    queued: num(pick(raw, 'queued')),
    circuitOpen: !!(raw.circuitOpen || raw.circuit_open),
    kvCacheUsage: num(pick(raw, 'kvCacheUsage', 'kv_cache_usage')),
    running: num(pick(raw, 'running')),
    waiting: num(pick(raw, 'waiting')),
  }
}

/* ---------- 监控驾驶舱 P1：令牌用量 / 推理摘录 / 延迟直方图 ---------- */

export interface BrainTierUsage {
  tier: string
  model: string
  promptTokens: number
  completionTokens: number
  totalTokens: number
  calls: number
  failures: number
}

export interface BrainUsage {
  window: string
  since: string
  tiers: BrainTierUsage[]
  total: BrainTierUsage
}

// 「没取到≠0」（P3-10c）哨兵钉链：五字段均为真计数（0=窗口内无调用/无 token/零失败的真实信息），
// ilm_call_log 列 NOT NULL Default(0)、proto 非 optional，字段恒在，?? 0 是冗余兜底，勿改 ?? null。
// 未调用的层级不出 tier 行——缺层级在列表层表达，不在字段层。
function normalizeTierUsage(raw: RawRecord): BrainTierUsage {
  return {
    tier: pick<string>(raw, 'tier') || '',
    model: pick<string>(raw, 'model') || '',
    promptTokens: num(pick(raw, 'promptTokens', 'prompt_tokens')) ?? 0,
    completionTokens: num(pick(raw, 'completionTokens', 'completion_tokens')) ?? 0,
    totalTokens: num(pick(raw, 'totalTokens', 'total_tokens')) ?? 0,
    calls: num(pick(raw, 'calls')) ?? 0,
    failures: num(pick(raw, 'failures')) ?? 0,
  }
}

export async function brainUsage(window: 'today' | '1h' | '24h' = 'today'): Promise<BrainUsage> {
  const data = (await request('/api/brain/usage', { query: { window } })) as RawRecord
  return {
    window: pick<string>(data, 'window') || window,
    since: pick<string>(data, 'since') || '',
    tiers: asList(data, 'tiers').map(normalizeTierUsage),
    total: normalizeTierUsage((data.total as RawRecord) ?? {}),
  }
}

export interface BrainReasoning {
  found: boolean
  decisionId: number
  traceId: string
  excerpt: string
  latencyMs: number
  tier: string
  model: string
  task: string
  createdAt: string
}

// 缺失口径由 found=false 表达（未找到时全字段零值）；decisionId=0 也可能表示无关联决策——
// 消费方必须先判 found，?? 0 是冗余哨兵（P3-10c 钉链）。
export async function brainReasoning(signalId: number): Promise<BrainReasoning> {
  const data = (await request('/api/brain/reasoning', { query: { signal_id: signalId } })) as RawRecord
  return {
    found: !!data.found,
    decisionId: num(pick(data, 'decisionId', 'decision_id')) ?? 0,
    traceId: pick<string>(data, 'traceId', 'trace_id') || '',
    excerpt: pick<string>(data, 'excerpt') || '',
    latencyMs: num(pick(data, 'latencyMs', 'latency_ms')) ?? 0,
    tier: pick<string>(data, 'tier') || '',
    model: pick<string>(data, 'model') || '',
    task: pick<string>(data, 'task') || '',
    createdAt: pick<string>(data, 'createdAt', 'created_at') || '',
  }
}

export interface BrainLatencyBucket {
  leMs: number
  count: number
}

export interface BrainLatencyHistogram {
  windowMinutes: number
  total: number
  /** 「没取到≠0」（P3-10c）：空窗（total=0）时分位无法计算为 null——0ms 不是合法分位值。 */
  p50Ms: number | null
  /** 同 p50Ms 口径。 */
  p95Ms: number | null
  buckets: BrainLatencyBucket[]
}

// total 为真计数（空窗=0 次成功调用的真信息）；p50/p95 空窗 → null，消费方显「样本不足」。
export async function brainLatencyHistogram(windowMinutes = 60): Promise<BrainLatencyHistogram> {
  const data = (await request('/api/brain/latency-histogram', { query: { window_minutes: windowMinutes } })) as RawRecord
  const total = num(pick(data, 'total')) ?? 0
  return {
    windowMinutes: num(pick(data, 'windowMinutes', 'window_minutes')) ?? windowMinutes,
    total,
    p50Ms: total > 0 ? num(pick(data, 'p50Ms', 'p50_ms')) : null,
    p95Ms: total > 0 ? num(pick(data, 'p95Ms', 'p95_ms')) : null,
    buckets: asList(data, 'buckets').map((b) => ({
      // 桶由后端固定构造（8 桶含上沿），leMs/count 恒有值——?? 0 冗余哨兵
      leMs: num(pick(b, 'leMs', 'le_ms')) ?? 0,
      count: num(pick(b, 'count')) ?? 0,
    })),
  }
}

/* ---------- 复盘体系（P0：收盘定稿、明日验证、程序版复盘；P1-7：分段式 AI 复盘） ---------- */

export interface ProgramReview {
  date: string
  content: string
  /** stats JSON 原文（含 missing 清单/AI 分段与核对留痕），由面板自行解析。 */
  statsJson: string
  /** AI 复盘：给明天的教训要点；程序版为空。 */
  lessons: string[]
  /** AI 复盘：参与模型（逗号拼接）；程序版为空。 */
  model: string
}

export type ReviewKind = 'program' | 'ai' | 'ai_week'

/** 每日/每周复盘。kind=program（默认）程序版八格；kind=ai 分段式 AI 复盘（P1-7，P2-8 起六段含筹码供给）；kind=ai_week AI 周报（P1-7b，日期自动对齐本周最后交易日）。当日没写成的抛 404。 */
export async function getProgramReview(date: string, kind: ReviewKind = 'program'): Promise<ProgramReview> {
  const data = (await request(`/api/brain/review/${encodeURIComponent(date)}?kind=${kind}`)) as RawRecord
  return {
    date: pick<string>(data, 'date') || date,
    content: pick<string>(data, 'content') || '',
    statsJson: pick<string>(data, 'statsJson', 'stats_json') || '',
    lessons: asList(data, 'lessons').map((x) => String(x ?? '')),
    model: pick<string>(data, 'model') || '',
  }
}

/** 手动生成/重新生成 AI 复盘：kind=ai 分段式日复盘（默认）；kind=ai_week 周报（行落在该周最后交易日）。同日同 kind 覆盖。 */
export async function generateAIReview(date: string, kind: 'ai' | 'ai_week' = 'ai'): Promise<ProgramReview> {
  const data = (await request('/api/brain/review/ai', {
    method: 'POST',
    body: JSON.stringify({ date, kind }),
  })) as RawRecord
  return {
    date: pick<string>(data, 'date') || date,
    content: pick<string>(data, 'content') || '',
    statsJson: pick<string>(data, 'statsJson', 'stats_json') || '',
    lessons: asList(data, 'lessons').map((x) => String(x ?? '')),
    model: pick<string>(data, 'model') || '',
  }
}

export interface FrozenEmotion {
  date: string
  upCount: number | null
  downCount: number | null
  brokenCount: number | null
  sealRate: number | null
  maxHeight: number | null
  promo12: number | null
  promo23: number | null
  promoHigh: number | null
  tierAvg: number | null
  tierMedian: number | null
  tierRedRate: number | null
  tierRelimit: number | null
  ladderJson: string
  firstBoardJson: string
  themeTreeJson: string
  prevTierJson: string
  sealQualityJson: string
  structureGaps: number[]
  partial: boolean
  missing: string[]
  source: string
  capturedAt: string
}

export async function getFrozenEmotion(date: string): Promise<FrozenEmotion> {
  const data = (await request(`/api/brain/emotion-frozen/${encodeURIComponent(date)}`)) as RawRecord
  return {
    date: pick<string>(data, 'date') || date,
    upCount: num(pick(data, 'upCount', 'up_count')),
    downCount: num(pick(data, 'downCount', 'down_count')),
    brokenCount: num(pick(data, 'brokenCount', 'broken_count')),
    sealRate: num(pick(data, 'sealRate', 'seal_rate')),
    maxHeight: num(pick(data, 'maxHeight', 'max_height')),
    promo12: num(pick(data, 'promo12')),
    promo23: num(pick(data, 'promo23')),
    promoHigh: num(pick(data, 'promoHigh', 'promo_high')),
    tierAvg: num(pick(data, 'tierAvg', 'tier_avg')),
    tierMedian: num(pick(data, 'tierMedian', 'tier_median')),
    tierRedRate: num(pick(data, 'tierRedRate', 'tier_red_rate')),
    tierRelimit: num(pick(data, 'tierRelimit', 'tier_relimit')),
    ladderJson: pick<string>(data, 'ladderJson', 'ladder_json') || '',
    firstBoardJson: pick<string>(data, 'firstBoardJson', 'first_board_json') || '',
    themeTreeJson: pick<string>(data, 'themeTreeJson', 'theme_tree_json') || '',
    prevTierJson: pick<string>(data, 'prevTierJson', 'prev_tier_json') || '',
    sealQualityJson: pick<string>(data, 'sealQualityJson', 'seal_quality_json') || '',
    // 板位 0 非法；脏数据 null 不得吞成「0板断层」——过滤而非 ?? 0（P3-10c）
    structureGaps: asList(data, 'structureGaps', 'structure_gaps')
      .map((v) => num(v))
      .filter((g): g is number => g != null && g > 0),
    partial: Boolean(pick<boolean>(data, 'partial')),
    missing: asList(data, 'missing').map((v) => String(v ?? '')).filter(Boolean),
    source: pick<string>(data, 'source') || '',
    capturedAt: pick<string>(data, 'capturedAt', 'captured_at') || '',
  }
}

export interface CheckResultRow {
  symbol: string
  name: string
  pct: number | null
  relimit: boolean
  hit: boolean
}

export interface CheckCandidateRow {
  symbol: string
  name: string
  reason: string
  text: string
}

export interface PredictionCheck {
  signalDate: string
  checkDate: string
  source: string
  candidateCount: number | null
  hitCount: number | null
  relimitCount: number | null
  hitRate: number | null
  avgPct: number | null
  maxPct: number | null
  partial: boolean
  missing: string[]
  candidates: CheckCandidateRow[]
  results: CheckResultRow[]
}

function normalizeCheck(data: RawRecord): PredictionCheck {
  return {
    signalDate: pick<string>(data, 'signalDate', 'signal_date') || '',
    checkDate: pick<string>(data, 'checkDate', 'check_date') || '',
    source: pick<string>(data, 'source') || '',
    candidateCount: num(pick(data, 'candidateCount', 'candidate_count')),
    hitCount: num(pick(data, 'hitCount', 'hit_count')),
    relimitCount: num(pick(data, 'relimitCount', 'relimit_count')),
    hitRate: num(pick(data, 'hitRate', 'hit_rate')),
    avgPct: num(pick(data, 'avgPct', 'avg_pct')),
    maxPct: num(pick(data, 'maxPct', 'max_pct')),
    partial: Boolean(pick<boolean>(data, 'partial')),
    missing: asList(data, 'missing').map((v) => String(v ?? '')).filter(Boolean),
    candidates: asList(data, 'candidates').map((c) => ({
      symbol: pick<string>(c, 'symbol') || '',
      name: pick<string>(c, 'name') || '',
      reason: pick<string>(c, 'reason') || '',
      text: pick<string>(c, 'text') || '',
    })),
    results: asList(data, 'results').map((r) => ({
      symbol: pick<string>(r, 'symbol') || '',
      name: pick<string>(r, 'name') || '',
      pct: num(pick(r, 'pct')),
      relimit: Boolean(pick<boolean>(r, 'relimit')),
      hit: Boolean(pick<boolean>(r, 'hit')),
    })),
  }
}

export async function listPredictionChecks(limit = 20): Promise<PredictionCheck[]> {
  const data = (await request('/api/brain/prediction-checks', { query: { limit } })) as RawRecord
  return asList(data, 'items').map(normalizeCheck)
}

export async function runPredictionCheck(date: string): Promise<PredictionCheck> {
  const data = (await request('/api/brain/prediction-checks/run', { method: 'POST', json: { date } })) as RawRecord
  return normalizeCheck(data)
}
