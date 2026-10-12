/**
 * HUD 十格的牌面解析：时段（timeband.ts）给格子定义，这里把 MonitorInput
 * 解析成人读的主词/副行。纯函数，缺口格写明缺哪个接口（监控业务监控方案 §7 纪律）。
 */
import { amount, pctText, phaseName, ratioPct, signed } from '@/utils/format'
import { bandCountdown, type HudCellDef, type SparkKey } from './timeband'
import type { MonitorInput } from './types'

export interface HudCellView {
  key: string
  label: string
  face: string
  sub: string
  tone: 'up' | 'down' | 'flat'
  select: string
  spark: SparkKey
  hero?: boolean
}

const GAP_PERF = '未接入：需要 GET /v1/strategy/performance'
const GAP_AUCTION = '未接入：需要 GET /v1/market/auction'

/** 显存只有一个文本 detail（`已用 X MiB / 共 Y MiB`），HUD 的 GPU 格与终端带「模型 / GPU」共用这一处解析。
 * 解析不出来就返回 null——GPU 利用率要等 GET /v1/ops/gpu（S7），不猜。 */
export function gpuMemory(input: MonitorInput): { used: number; total: number; pct: number; text: string } | null {
  const node = input.ops?.ok ? input.ops.resources?.find((item) => item.name.startsWith('显存')) : null
  if (!node) return null
  const parsed = /已用 (\d+(?:\.\d+)?) MiB \/ 共 (\d+(?:\.\d+)?) MiB/.exec(node.detail)
  if (!parsed) return null
  const used = Number(parsed[1])
  const total = Number(parsed[2])
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0) return null
  return { used, total, pct: Math.round((used / total) * 100), text: `${used} / ${total} MiB` }
}

export function resolveHud(input: MonitorInput, cells: HudCellDef[], now: number): HudCellView[] {
  const date = new Date(now)
  return cells.map((cell) => {
    const resolved = resolve(cell.key, input, date)
    return { key: cell.key, label: cell.label, select: cell.select, spark: cell.spark, hero: cell.hero, ...resolved }
  })
}

function resolve(key: string, input: MonitorInput, date: Date): Pick<HudCellView, 'face' | 'sub' | 'tone'> {
  switch (key) {
    case 'phase': {
      const sentiment = input.sentiment
      if (!sentiment?.ok) return { face: '未返回', sub: '情绪接口无返回', tone: 'flat' }
      const up = sentiment.up == null ? '—' : String(sentiment.up)
      const down = sentiment.down == null ? '—' : String(sentiment.down)
      /* 后端给的是枚举码（HOT/WARM/…），牌面统一走 format.phaseName 的中文表 */
      const name = sentiment.phase ? phaseName(sentiment.phase) : '未返回'
      return { face: sentiment.stale ? '过期' : name, sub: `涨 ${up} 跌 ${down}`, tone: 'flat' }
    }
    case 'limit': {
      const count = input.limits?.ok ? input.limits.rows.length : null
      const height = input.sentiment?.ok ? input.sentiment.maxHeight : null
      return {
        face: count == null ? '未返回' : String(count),
        sub: height == null ? '' : `最高 ${height} 板`,
        tone: 'flat',
      }
    }
    case 'broken': {
      const rate = input.sentiment?.ok ? input.sentiment.brokenRate : null
      /* brokenRate 是 0–1 比率（后端 BrokenCount/(UpCount+BrokenCount)），走 ratioPct */
      return { face: rate == null ? '未返回' : ratioPct(rate, 0), sub: '', tone: 'flat' }
    }
    case 'mainline': {
      const top = input.sectors?.ok ? input.sectors.rows[0] : null
      if (!top) return { face: '未返回', sub: '板块接口无返回', tone: 'flat' }
      return { face: top.name || '未命名', sub: top.avgPct == null ? '' : `板块均 ${pctText(top.avgPct)}`, tone: 'flat' }
    }
    case 'signal': {
      if (!input.signals?.ok) return { face: '信号未返回', sub: '', tone: 'flat' }
      const pending = input.signals.items.filter((item) => item.status === 'approved' || item.status === 'risk_checking').length
      const best = Math.max(-1, ...input.signals.items.map((item) => item.score ?? -1))
      return {
        face: pending > 0 ? `${pending} 笔` : '空闲',
        sub: best < 0 ? '' : `最高 ${best} 分`,
        tone: 'flat',
      }
    }
    case 'miss': {
      if (!input.candidates?.ok) return { face: '候选未返回', sub: '', tone: 'flat' }
      const top = topReason(input.candidates.items.map((item) => item.missReason))
      return { face: String(input.candidates.items.length), sub: top, tone: 'flat' }
    }
    case 'risk': {
      const risk = input.risk
      if (!risk?.ok) return { face: '风控未返回', sub: '', tone: 'flat' }
      if (risk.killActive) return { face: '急停', sub: risk.killReason || '已触发', tone: 'flat' }
      if (risk.marketBreaker) return { face: '熔断', sub: risk.marketBreaker, tone: 'flat' }
      return { face: '守着', sub: risk.buysToday == null ? '' : `今日买单 ${risk.buysToday}`, tone: 'flat' }
    }
    case 'model': {
      const stats = input.brainStats
      if (!stats?.ok) return { face: '统计未返回', sub: '令牌未接入', tone: 'flat' }
      const open = stats.tiers.find((tier) => tier.circuitOpen)
      if (open) return { face: open.tier || open.model || '熔断', sub: '熔断', tone: 'flat' }
      const calls = stats.tiers.reduce((sum, tier) => sum + (tier.calls ?? 0), 0)
      const p95 = Math.max(0, ...stats.tiers.map((tier) => tier.p95Ms ?? 0))
      return {
        face: stats.tiers.length === 0 ? '统计未返回' : `${calls} 次`,
        sub: `P95 ${Math.round(p95)}ms · 令牌未接入`,
        tone: 'flat',
      }
    }
    case 'gpu': {
      const node = input.ops?.ok ? input.ops.resources?.find((item) => item.name.startsWith('显存')) : null
      if (!node) return { face: '未接入', sub: 'ops.resources 无显存节点', tone: 'flat' }
      const memory = gpuMemory(input)
      if (!memory) return { face: node.status === 'ok' ? '在' : '断', sub: '利用率未接入：需要 GET /v1/ops/gpu', tone: 'flat' }
      return { face: `${memory.pct}%`, sub: '显存占比 · 利用率未接入', tone: 'flat' }
    }
    case 'pnl': {
      const account = input.account
      if (!account?.ok) return { face: '账户未返回', sub: '', tone: 'flat' }
      const pnl = account.dayPnl
      return {
        face: signed(pnl),
        sub: `权益 ${amount(account.equity)} · 持仓 ${account.positions.length}`,
        tone: pnl == null || pnl === 0 ? 'flat' : pnl > 0 ? 'up' : 'down',
      }
    }
    /* ---- 时段特化格 ---- */
    case 'gap:perf': {
      const perf = input.strategyP1?.ok ? input.strategyP1.performance : null
      if (!perf) return { face: '未接入', sub: GAP_PERF, tone: 'flat' }
      if (perf.sample === 0) return { face: '样本不足', sub: '还没有已出后验的信号', tone: 'flat' }
      return { face: pctText(perf.winRate * 100, 0), sub: `近 ${perf.sample} 笔胜率 · 盈亏比 ${perf.plRatio > 0 ? perf.plRatio.toFixed(1) : '不足'}`, tone: perf.winRate >= 0.5 ? 'up' : 'down' }
    }
    case 'gap:misslimit':
      return { face: '未接入', sub: '需要昨日涨停跨日快照表（P2）', tone: 'flat' }
    case 'overnight:overseas': {
      const hit = input.sectors?.ok ? input.sectors.rows.find((row) => row.overseas != null) : null
      if (!hit) return { face: '未调用', sub: '', tone: 'flat' }
      return { face: hit.name, sub: `外围 ${pctText(hit.overseas)}`, tone: 'flat' }
    }
    case 'gap:auction-amount':
    case 'gap:auction-limit':
    case 'gap:auction-rush':
      return { face: '未接入', sub: GAP_AUCTION, tone: 'flat' }
    case 'midday:pnl': {
      const pnl = input.account?.ok ? input.account.dayPnl : null
      return { face: pnl == null ? '账户未返回' : signed(pnl), sub: '半场盈亏', tone: pnl == null || pnl === 0 ? 'flat' : pnl > 0 ? 'up' : 'down' }
    }
    case 'midday:limit': {
      const count = input.limits?.ok ? input.limits.rows.length : null
      return { face: count == null ? '未返回' : String(count), sub: '半场涨停', tone: 'flat' }
    }
    case 'midday:focus': {
      const brief = input.briefing?.ok ? input.briefing.items[0] : null
      if (brief) return { face: brief.title || '晨报', sub: '晨报头条', tone: 'flat' }
      const word = input.hotWords?.ok ? input.hotWords.items[0] : null
      if (word) return { face: word.target || '热词', sub: '热词第一', tone: 'flat' }
      return { face: '晨报未生成', sub: '', tone: 'flat' }
    }
    case 'close:float': {
      const positions = input.account?.ok ? input.account.positions : null
      if (!positions) return { face: '持仓未返回', sub: '', tone: 'flat' }
      if (positions.length === 0) return { face: '无持仓', sub: '', tone: 'flat' }
      let float = 0
      let known = false
      for (const row of positions) {
        if (row.lastPrice == null || row.avgCost == null || row.quantity == null) continue
        float += (row.lastPrice - row.avgCost) * row.quantity
        known = true
      }
      if (!known) return { face: '未返回', sub: '现价或成本缺失', tone: 'flat' }
      return { face: signed(float), sub: '持仓浮动盈亏', tone: float === 0 ? 'flat' : float > 0 ? 'up' : 'down' }
    }
    case 'close:stop': {
      const positions = input.account?.ok ? input.account.positions : null
      if (!positions) return { face: '持仓未返回', sub: '', tone: 'flat' }
      if (positions.length === 0) return { face: '无持仓', sub: '', tone: 'flat' }
      const signals = input.signals?.ok ? input.signals.items : []
      let nearest: { symbol: string; pct: number } | null = null
      for (const row of positions) {
        const stop = signals.find((item) => item.symbol === row.symbol && item.stop != null)?.stop
        if (stop == null || row.lastPrice == null || row.lastPrice <= 0) continue
        const pct = ((row.lastPrice - stop) / row.lastPrice) * 100
        if (!nearest || pct < nearest.pct) nearest = { symbol: row.symbol, pct }
      }
      if (!nearest) return { face: '止损价未接入', sub: '信号 stop 未对齐持仓', tone: 'flat' }
      return { face: `${nearest.pct.toFixed(1)}%`, sub: `距止损最近 ${nearest.symbol}`, tone: 'flat' }
    }
    case 'close:countdown': {
      const text = bandCountdown(date, 'closing') || bandCountdown(date, 'afternoon')
      const minutes = /(\d+) 分/.exec(text)
      return { face: minutes ? `${minutes[1]} 分` : '—', sub: '15:00 收盘', tone: 'flat' }
    }
    case 'gap:winrate': {
      const perf = input.strategyP1?.ok ? input.strategyP1.performance : null
      if (!perf) return { face: '未接入', sub: GAP_PERF, tone: 'flat' }
      if (perf.sample === 0) return { face: '样本不足', sub: '还没有已出后验的信号', tone: 'flat' }
      return { face: pctText(perf.winRate * 100, 0), sub: `近 ${perf.sample} 笔胜率`, tone: perf.winRate >= 0.5 ? 'up' : 'down' }
    }
    case 'gap:attribution': {
      const perf = input.strategyP1?.ok ? input.strategyP1.performance : null
      if (!perf) return { face: '未接入', sub: '需要 GET /v1/strategy/performance 分维归因', tone: 'flat' }
      if (perf.attributions.length === 0) return { face: '无记录', sub: 'M07 归因写入后才有', tone: 'flat' }
      const top = perf.attributions.reduce((a, b) => (b.count > a.count ? b : a))
      return { face: `${top.attribution} ×${top.count}`, sub: `近 ${perf.sample} 笔第一归因`, tone: 'flat' }
    }
    case 'gap:correlation': {
      const scatter = input.strategyP1?.ok ? input.strategyP1.scatter : null
      if (!scatter) return { face: '未接入', sub: '需要 GET /v1/strategy/score-vs-pnl', tone: 'flat' }
      if (scatter.sample < 2) return { face: '样本不足', sub: '已后验笔数少于 2', tone: 'flat' }
      return { face: scatter.pearson.toFixed(2), sub: `评分-收益相关（${scatter.sample} 笔）`, tone: Math.abs(scatter.pearson) >= 0.5 ? 'up' : 'flat' }
    }
    case 'off:tasks': {
      const tasks = input.tasks?.ok ? input.tasks.items : null
      if (!tasks || tasks.length === 0) return { face: '任务未返回', sub: '', tone: 'flat' }
      const running = tasks.filter((item) => item.status === 'running').length
      return { face: String(tasks.length), sub: running > 0 ? `${running} 个在跑` : '没有回测在跑', tone: 'flat' }
    }
    case 'off:backfill': {
      const items = input.quality?.ok ? input.quality.items : null
      if (!items) return { face: '质量未返回', sub: '', tone: 'flat' }
      if (items.length === 0) return { face: '0', sub: '没有质量问题记录', tone: 'flat' }
      const hits = items.reduce((sum, item) => sum + (item.hits ?? 0), 0)
      return { face: String(hits), sub: `质量问题 ${items.length} 条命中`, tone: 'flat' }
    }
    case 'off:db':
      return { face: '未接入', sub: '需要 PG 库体积只读口（P2）', tone: 'flat' }
    default:
      return { face: '未返回', sub: '', tone: 'flat' }
  }
}

/** 漏选原因众数；并列取先出现的 */
function topReason(reasons: string[]): string {
  const counts = new Map<string, number>()
  for (const reason of reasons) {
    const text = reason.trim()
    if (!text) continue
    counts.set(text, (counts.get(text) ?? 0) + 1)
  }
  let best = ''
  let bestCount = 0
  for (const [reason, count] of counts) {
    if (count > bestCount) {
      best = reason
      bestCount = count
    }
  }
  return best
}
