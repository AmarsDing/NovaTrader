/**
 * 活动条的数据源（S2 §4.4.1）：每张牌底缘 60 格、每格 1 秒的会话内活动记录。
 *
 * 只记事实：这一秒里这张牌的数据动过没有。判据两条，都不猜——
 *   1. 牌面签名变了（face/sub/detail 对应的原始字段）；
 *   2. 数据自带的时间戳（lastAt / asOf / generatedAt）往前走了，把跨过的秒都算有活动。
 * 60 格只回答「这一分钟里有没有动静」；更长的静默（模型 10 分钟、扫�描盘后 30 分钟）
 * 由 lastActive 直接算，不受窗口限制。
 *
 * 页面暂停（document.hidden）期间没有观测，那段区间记 hole：格画成未知，
 * 静默时钟也从 hole 结束处重新起算，避免用户切回窗口就看见一片「没有动静」。
 *
 * 全部在内存里，不落库、不上传（§9）。页面刷新即失。
 */

export const ACTIVITY_CELLS = 60
/** 有活动的格 / 静默格的透明度（§4.4.1） */
export const ACTIVITY_ON_ALPHA = 0.55
export const ACTIVITY_OFF_ALPHA = 0.06

interface Track {
  signature: string
  /** 首次观测时刻（ms） */
  since: number
  /** 最近一次判定有活动的秒（epoch 秒）；从未有过为 -1 */
  lastActiveSec: number
  /** 数据自带时间戳的最近值（ms），没有时间戳的牌为 null */
  dataAt: number | null
  /** 有活动的秒。只保留窗口内的格子 */
  hits: Set<number>
}

export class ActivityLog {
  private tracks = new Map<string, Track>()
  /** 未观测区间（ms）。只保留窗口内的 */
  private holes: { from: number; to: number }[] = []

  /**
   * 记一次观测。signature 变化或 dataAt 前进都算有活动，返回这次是否算动了。
   * dataAt 可以不给（没有自带时间戳的接口）。
   */
  observe(key: string, signature: string, now: number, dataAt?: number | null): boolean {
    const nowSec = Math.floor(now / 1000)
    const next = Number.isFinite(dataAt as number) ? (dataAt as number) : null
    let track = this.tracks.get(key)
    if (!track) {
      track = { signature, since: now, lastActiveSec: -1, dataAt: next, hits: new Set() }
      this.tracks.set(key, track)
      return false
    }

    let active = false
    if (track.signature !== signature) {
      track.signature = signature
      active = true
      track.hits.add(nowSec)
    }
    if (next != null && track.dataAt != null && next > track.dataAt) {
      /* 真到了新 tick：把上一次到这一次之间跨过的秒都点亮，看得出是「连续在跑」还是「一顿一顿」 */
      const from = Math.max(Math.floor(track.dataAt / 1000) + 1, nowSec - (ACTIVITY_CELLS - 1))
      for (let sec = from; sec <= nowSec; sec += 1) track.hits.add(sec)
      active = true
    }
    if (next != null) track.dataAt = next
    if (active || track.lastActiveSec < 0) {
      if (active) track.lastActiveSec = nowSec
      else track.lastActiveSec = Math.floor(track.since / 1000)
    }
    this.prune(nowSec)
    return active
  }

  /** 60 格。true 有活动、false 静默、null 没观测到（页面刚打开或暂停过） */
  cells(key: string, now: number): (boolean | null)[] {
    const nowSec = Math.floor(now / 1000)
    const track = this.tracks.get(key)
    const start = nowSec - (ACTIVITY_CELLS - 1)
    const out: (boolean | null)[] = []
    for (let index = 0; index < ACTIVITY_CELLS; index += 1) {
      const sec = start + index
      if (this.inHole(sec)) {
        out.push(null)
        continue
      }
      if (!track || sec < Math.floor(track.since / 1000)) {
        out.push(null)
        continue
      }
      out.push(track.hits.has(sec))
    }
    return out
  }

  /** 连续没有活动的秒数。看了多久就最多算多久，不把没看的时间算成静默 */
  darkFor(key: string, now: number): number {
    const track = this.tracks.get(key)
    if (!track) return 0
    const from = Math.max(track.lastActiveSec, this.lastHoleEnd(), Math.floor(track.since / 1000))
    return Math.max(0, Math.floor(now / 1000) - from)
  }

  /** 已经被观测的秒数。不足阈值的时间不下静默结论 */
  observedFor(key: string, now: number): number {
    const track = this.tracks.get(key)
    if (!track) return 0
    return Math.max(0, Math.floor((now - track.since) / 1000))
  }

  /** 记一段没有观测的区间（页面隐藏）。静默时钟从这段结束处重新起算 */
  hole(from: number, to: number): void {
    if (!(to > from)) return
    this.holes.push({ from, to })
    this.prune(Math.floor(to / 1000))
  }

  /** 会话内清空（换账户/换交易日的极端场景用得上） */
  reset(): void {
    this.tracks.clear()
    this.holes = []
  }

  private inHole(sec: number): boolean {
    return this.holes.some((item) => sec >= Math.floor(item.from / 1000) && sec <= Math.floor(item.to / 1000))
  }

  private lastHoleEnd(): number {
    let end = -1
    for (const item of this.holes) end = Math.max(end, Math.floor(item.to / 1000))
    return end
  }

  private prune(nowSec: number): void {
    const floor = nowSec - ACTIVITY_CELLS
    for (const track of this.tracks.values()) {
      for (const sec of track.hits) {
        if (sec < floor) track.hits.delete(sec)
      }
    }
    this.holes = this.holes.filter((item) => Math.floor(item.to / 1000) >= floor)
  }
}

/** gallery 静态示例用的固定花型：前半段连续在跑，后半段只零星两下 */
export function demoActivityCells(pattern: 'busy' | 'quiet' = 'busy'): (boolean | null)[] {
  const cells: (boolean | null)[] = []
  for (let index = 0; index < ACTIVITY_CELLS; index += 1) {
    if (index < 6) {
      cells.push(null)
      continue
    }
    if (pattern === 'busy') {
      cells.push(index % 4 !== 3)
      continue
    }
    cells.push(index > 22 && (index === 30 || index === 47))
  }
  return cells
}
