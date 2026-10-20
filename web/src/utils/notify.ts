/**
 * 系统通知三 sink 分流（G3 告警活跃感知）：
 *   应用活跃（窗口聚焦且未 hidden）→ 仅应用内 toast，不打扰系统响铃；
 *   应用不活跃（最小化/失焦/锁屏）→ Tauri 原生通知；
 *   浏览器开发态（无桌面壳）→ 一律应用内 toast 兜底。
 * 活跃判定直读 document.visibilityState / hasFocus，与 settings store 的
 * pageVisible（visibilitychange 同步）同源，不再订阅第二份。
 *
 * P2 告警漏斗接线（方案 §8）：deskNotify 第 4 参带事件视图时走
 *   分级 → L4 旁路（AlertCenter 全屏通道负责）→ 静默落账 → 同因聚合（bump 计数）
 *   → 冷却抑 toast → 分流 toast + 落 feed → L3 抬页签角标 + 按声音开关单响；
 * event 缺省保持存量行为，trade-bar 等存量调用方零改动。
 */
import { useAlertsStore, bumpToast, type ToastItem } from '@/stores/alerts'
import { useNotifyStore } from '@/stores/notify'
import { useSettingsStore } from '@/stores/settings'
import {
  causeKey,
  cooldownCheck,
  createCauseAggregator,
  levelOf,
  renderAlertCopy,
  type AlertEventView,
} from '@/ui/alert-levels'
import { isQuietNow, loadQuiet } from './alert-quiet'
import { playOnce } from './sound'
import { inTauri } from './tauri'

export type NotifySink = 'inapp' | 'native'

/** 活跃 → inapp；不活跃且有壳 → native；不活跃且无壳 → inapp（页面内提示兜底）。 */
export function pickNotifySink(active: boolean, tauri: boolean): NotifySink {
  if (active) return 'inapp'
  return tauri ? 'native' : 'inapp'
}

/** 推送 severity/kind 文本 → toast 色调。红涨绿跌无关，纯告警级别语义。 */
export function toastKindOf(raw: string): ToastItem['kind'] {
  const key = raw.toLowerCase()
  if (key.includes('crit') || key.includes('fatal') || key.includes('error') || key.includes('danger')) return 'error'
  if (key.includes('warn') || key.includes('alert')) return 'warn'
  if (key.includes('success') || key.includes('up') || key.includes('ok')) return 'success'
  return 'info'
}

function isAppFocused(): boolean {
  if (typeof document === 'undefined') return false
  return document.visibilityState !== 'hidden' && document.hasFocus()
}

const TOAST_TTL: Record<ToastItem['kind'], number> = {
  info: 4200,
  success: 4200,
  warn: 6000,
  error: 8000,
}

async function nativeNotify(title: string, body: string): Promise<boolean> {
  try {
    const { isPermissionGranted, requestPermission, sendNotification } = await import('@tauri-apps/plugin-notification')
    let allowed = await isPermissionGranted()
    if (!allowed) {
      const perm = await requestPermission()
      allowed = perm === 'granted'
    }
    if (!allowed) return false
    sendNotification({ title, body })
    return true
  } catch {
    return false
  }
}

/** §8 漏斗模块级单例：跨帧记忆都在这（聚合窗 / 末次 toast 键 → id / 冷却时刻表）。 */
const causeAgg = createCauseAggregator(10_000)
const lastToastIdByKey = new Map<string, number>()
const lastFired = new Map<string, number>()

/** 事件 → 建议动作面板（§8.6）：规则看规则、带票看行情、数据源看数据，兜底通知中心。 */
function panelFor(e: AlertEventView): string {
  if (e.kind === 'alert-rule') return 'alert-rules'
  if (e.symbol) return 'quote-sidebar'
  if (e.category === 'datahub') return 'datasource-center'
  return 'notify-center'
}

/** 事件在场时的 §8 告警漏斗；返回是否有 toast 被弹出或命中 bump。 */
async function funnelNotify(text: string, textBody: string, kind: unknown, event: AlertEventView): Promise<boolean> {
  const level = levelOf(event)
  /* L4 旁路：AlertCenter 既有全屏通道负责，防双响，也不进 feed。 */
  if (level === 'L4') return false
  const key = causeKey(event)
  try {
    const notify = useNotifyStore()
    const feedBase = {
      level,
      kind: event.kind,
      title: text,
      body: textBody || undefined,
      symbol: event.symbol,
      causeKey: key,
      at: Date.now(),
    }
    /* 静默窗内：无 toast/无声/无角标，仅落账。 */
    if (isQuietNow(level)) {
      notify.ingestFeed({ ...feedBase, silenced: true })
      return false
    }
    /* 同因聚合：窗内再发不新增 toast，命中旧条目计数 +1（未命中则忽略 bump）。 */
    const { count } = causeAgg.seen(key)
    if (count > 1) {
      const prev = lastToastIdByKey.get(key)
      if (prev != null) bumpToast(prev)
      notify.ingestFeed({ ...feedBase, count })
      return true
    }
    /* 冷却窗内不重复 toast，但仍落账。 */
    if (cooldownCheck(lastFired, key, Date.now(), loadQuiet().cooldownMs)) {
      notify.ingestFeed(feedBase)
      return false
    }
    /* L1 维持既有 toastKindOf 色调路径；L2/L3 走 warn 色并落账（§8.4 重要即琥珀提醒）。 */
    const toastKind = level === 'L1' ? toastKindOf(kind ? String(kind) : '') : 'warn'
    const alerts = useAlertsStore()
    let toastId: number | undefined
    if (pickNotifySink(isAppFocused(), inTauri()) === 'native') await nativeNotify(text, textBody)
    else {
      const id = alerts.pushToast(toastKind, text, textBody)
      setTimeout(() => alerts.dismissToast(id), TOAST_TTL[toastKind])
      toastId = id
    }
    if (level !== 'L1') {
      notify.ingestFeed(feedBase)
      if (level === 'L3') {
        notify.pulseAttention(panelFor(event), level)
        /* 声音预算 §8.4：L3 单响不循环，开关门在 settings。 */
        if (useSettingsStore().sound) playOnce()
      }
    }
    /* 记录本条目 toast id 供同窗聚合 bump：pushToast 返 id 用之，否则取 alerts 最顶条。 */
    const idForKey = toastId ?? alerts.toasts.at(-1)?.id
    if (idForKey != null) lastToastIdByKey.set(key, idForKey)
    return true
  } catch {
    return false
  }
}

/**
 * 通知入口（hub handlePush 唯一直调应用侧 API）：按活跃感知分流到单一 sink。
 * kind 传后端 severity/kind 原文，仅影响 in-app toast 色调，不进原生正文；
 * event（§8 告警帧视图）缺省时维持存量行为。
 */
export async function deskNotify(title: unknown, body?: unknown, kind?: unknown, event?: AlertEventView): Promise<boolean> {
  const text = title ? String(title).trim() : ''
  if (!text) return false
  const textBody = body ? String(body) : ''
  if (event) return funnelNotify(text, textBody, kind, event)
  if (pickNotifySink(isAppFocused(), inTauri()) === 'native') return nativeNotify(text, textBody)
  try {
    const alerts = useAlertsStore()
    const toastKind = toastKindOf(kind ? String(kind) : '')
    const id = alerts.pushToast(toastKind, text, textBody)
    setTimeout(() => alerts.dismissToast(id), TOAST_TTL[toastKind])
    return true
  } catch {
    return false
  }
}

/**
 * §8.2 五段式告警直发：renderAlertCopy 组「对象 · 事件 · 条件 · 时点」正文（对象缺省「系统」），
 * 标题取 view.title，色调沿用 toastKindOf 现状推断，随后进统一漏斗。
 */
export async function raiseAlert(view: AlertEventView): Promise<boolean> {
  const copy = renderAlertCopy(view)
  const joined = [copy.object, copy.event, copy.condition, copy.time].filter((s) => s && s.trim()).join(' · ')
  return deskNotify(view.title || copy.event, joined, view.level || view.kind || '', view)
}
