/**
 * P2-9 重连缺口回填的纯逻辑（MarketView 的 onResync 路径调用）。
 * 后端窗口语义（proto GetBarsRequest + data/repo.go:539-567）：
 * start/end 含两端、limit 从 end 往前取、返回升序；
 * 因此回填若能覆盖现有末根（fetched 首行时间 ≤ 末根时间），合并只需要
 * 「同根替换 + 新根追加」两种动作——更深的乱序重叠不会出现，出现了也只跳过不插入。
 * 算/判/写分离：先在副本上合并，covered 且不 overflow 才一次性写回 existing，失败路径不留脏增量。
 * 粒度：ms（1m 等分钟线，按毫秒/字典序比）与 day（1d，按上海日历日比）两种；
 * day 粒度下同日不同格式的行（'2026-10-19' 与 '2026-10-19T14:59:00+08:00'）视为同根，
 * 防御今朝待用 bar 管线（M01 §7.4 已产未路由）的分钟帧把日线末根写成 RFC3339 后替换不进。
 */
import type { Bar } from '@/api/types/market'
import { chartTime } from '@/utils/time'

/** 回填上限的默认参考值（= MarketView LIVE_CAP）；调用方应显式传自己的 cap。 */
export const BACKFILL_LIMIT = 800

/** 合并粒度：分钟线按时刻、日线按上海日历日。 */
export type GapGranularity = 'ms' | 'day'

/** 与 applyLiveBar 同口径：双方可解析就按毫秒数值比，否则退字符串比较。 */
export function cmpTime(a: string, b: string): number {
  const ta = Date.parse(a)
  const tb = Date.parse(b)
  if (Number.isFinite(ta) && Number.isFinite(tb)) return ta < tb ? -1 : ta > tb ? 1 : 0
  return a === b ? 0 : a < b ? -1 : 1
}

/** 上海日历日（YYYY-MM-DD），统一 'YYYY-MM-DD' 与 RFC3339 两种来源格式；不可解析返回 null。 */
function dayKey(time: string): string | null {
  const key = chartTime(time, '1d')
  return typeof key === 'string' && key ? key : null
}

export interface GapMerge {
  /** false = 回填窗口够不到现有末根（缺口宽于回填上限/数据缺失），调用方应整载兜底。 */
  covered: boolean
  /** 合并后超过 cap：应整载对齐而不是继续堆积。（covered=false 时恒 false） */
  overflow: boolean
  /** 按时间升序、需要喂给 NtKline.updateLast 的行（同根替换在前、新根追加在后）。 */
  applied: Bar[]
  /** 新增到尾部的行数（day 粒度：晚于回填窗口首日的行数）。 */
  appended: number
  /** 被换下的 existing 尾部行数（ms：同刻替换；day：同日及更晚被整条重画的尾部行数）。 */
  replaced: number
}

/** 分钟粒度：同刻替换、更晚追加、更早跳过；返回合并后副本，覆盖不了返回 null。 */
function mergeMs(existing: Bar[], rows: Bar[], out: GapMerge): Bar[] | null {
  const tail = existing[existing.length - 1]
  // 末根时间不可解析（垃圾帧污染的尾部）：不做字典序猜测，交整载兜底清掉。
  if (!Number.isFinite(Date.parse(tail.time))) return null
  if (cmpTime(rows[0].time, tail.time) > 0) return null
  const draft = existing.slice()
  for (const bar of rows) {
    const tip = draft[draft.length - 1]
    const cmp = cmpTime(bar.time, tip.time)
    if (cmp < 0) continue
    if (cmp === 0) {
      draft[draft.length - 1] = bar
      out.replaced += 1
    } else {
      draft.push(bar)
      out.appended += 1
    }
    out.applied.push(bar)
  }
  return draft
}

/**
 * 日线粒度：fetched 是 REST 权威升序。先裁掉 existing 中日历日 ≥ 窗口首日的尾部
 * （同日不同格式的混合尾根一并换掉），再整段接上 fetched，结果天然升序。
 * 覆盖判定按日历日：窗口首日 > 末根日 = 够不到。任一侧不可解析时回退 ms 口径。
 */
function mergeDay(existing: Bar[], rows: Bar[], out: GapMerge): Bar[] | null {
  const firstDay = dayKey(rows[0].time)
  const tailDay = dayKey(existing[existing.length - 1].time)
  if (!firstDay || !tailDay) return mergeMs(existing, rows, out)
  if (firstDay > tailDay) return null
  let cut = existing.length
  while (cut > 0) {
    const key = dayKey(existing[cut - 1].time)
    if (key == null || key < firstDay) break
    cut -= 1
  }
  const draft = existing.slice(0, cut)
  out.replaced = existing.length - cut
  for (const bar of rows) {
    draft.push(bar)
    out.applied.push(bar)
    const key = dayKey(bar.time)
    if (key != null && key > firstDay) out.appended += 1
  }
  return draft
}

/**
 * 把缺口窗口的 REST 行合并进 existing（保持数组引用，绕开 NtKline 的全量套用）。
 * 只动尾部：同根替换、更晚追加；早于末根的行跳过。
 * 覆盖判定：fetched 为空视为无需合并（covered=true）；非空则首行必须不晚于现有末根（同粒度口径）。
 * 失败路径（covered=false / overflow=true）不改 existing；成功才一次性写回。
 */
export function mergeGapRows(
  existing: Bar[],
  fetched: Bar[],
  cap: number = BACKFILL_LIMIT,
  granularity: GapGranularity = 'ms',
): GapMerge {
  const out: GapMerge = { covered: true, overflow: false, applied: [], appended: 0, replaced: 0 }
  const rows = fetched.filter((bar) => bar && bar.time).sort((a, b) => cmpTime(a.time, b.time))
  if (!rows.length) return out
  if (!existing.length || !existing[existing.length - 1]?.time) {
    out.covered = false
    return out
  }
  const draft = (granularity === 'day' ? mergeDay : mergeMs)(existing, rows, out)
  if (!draft) {
    out.covered = false
    out.applied = []
    out.appended = 0
    out.replaced = 0
    return out
  }
  out.overflow = draft.length > cap
  if (!out.overflow) existing.splice(0, existing.length, ...draft)
  return out
}
