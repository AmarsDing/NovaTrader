/**
 * 汇聚与静默的服务端持久化（M12 §8.3 存储级后端化，1-e 裁定用户点选：
 * 配置走 strategy_config KV，不加表不加列、引擎与通知历史行为不变）。
 * - `app.alert.quiet.cooldown_ms` / `app.alert.quiet.windows` 由后端参数目录登记出验，
 *   经 /api/config 快照下发（Target=admin；unknown 键会被快照过滤，必须登记）；
 * - utils/alert-quiet 的 localStorage 层保留为即时读缓存与离线回退——toast 决策
 *   保持同步读，不引入网络等待；
 * - 同步口径：首次 prime 后服务端为准。服务端已有值就采纳，没有（旧版本机）就
 *   把本机值写回认领；写入失败（viewer 只读 / CAS 冲突 / 离线）本机配置仍生效，
 *   状态经 quietSyncState 给面板如实呈现，绝不报假成功。
 */
import { ref } from 'vue'
import { ApiError } from '@/api/http'
import { getConfigSnapshot, saveConfig } from '@/api/config'
import { loadQuiet, saveQuiet, type AlertQuietConfig } from './alert-quiet'

export const QUIET_COOLDOWN_KEY = 'app.alert.quiet.cooldown_ms'
export const QUIET_WINDOWS_KEY = 'app.alert.quiet.windows'

export type QuietSyncState = 'idle' | 'ok' | 'readonly' | 'error'
/** 面板呈现用：仅表达最近一次服务端往返的结果，不参与 toast 决策。 */
export const quietSyncState = ref<QuietSyncState>('idle')
/** 服务端值被采纳/写回认领时 +1；面板按它重拽编辑草稿，避免窗内陈旧。 */
export const quietSyncVersion = ref(0)

const PUSH_DEBOUNCE_MS = 800
let pushTimer: ReturnType<typeof setTimeout> | null = null
let primedOnce = false

/** 测试专用：vitest 单文件内模块态跨用例存续，用例间复位 prime 门闩与快照。 */
export function resetQuietSyncForTest(): void {
  primedOnce = false
  quietSyncState.value = 'idle'
  quietSyncVersion.value = 0
  if (pushTimer) {
    clearTimeout(pushTimer)
    pushTimer = null
  }
}

function isForbidden(e: unknown): boolean {
  return e instanceof ApiError && e.status === 403
}

function isConflict(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409
}

/**
 * 快照值折成配置：逐项独立采纳；键缺失 → 该项保持本机；
 * 值显式清空（''）语义=回落默认，与本机键清除同义。
 * 返回 null 表示两项均未设置（服务端无该配置）。
 */
export function parseServerQuiet(values: Record<string, string>): AlertQuietConfig | null {
  const local = loadQuiet()
  let touched = false
  const next: AlertQuietConfig = { cooldownMs: local.cooldownMs, windows: [...local.windows] }
  const rawCooldown = values[QUIET_COOLDOWN_KEY]
  if (rawCooldown !== undefined) {
    touched = true
    if (rawCooldown === '') {
      next.cooldownMs = 60_000
    } else {
      const n = Number(rawCooldown)
      // 服务端校验已挡非法值；读侧仍容错——坏值当未设置。
      if (Number.isInteger(n) && n >= 0 && n <= 86_400_000) next.cooldownMs = n
    }
  }
  const rawWindows = values[QUIET_WINDOWS_KEY]
  if (rawWindows !== undefined) {
    touched = true
    if (rawWindows === '') {
      next.windows = []
    } else {
      try {
        const parsed = JSON.parse(rawWindows) as AlertQuietConfig['windows']
        if (Array.isArray(parsed)) next.windows = parsed
      } catch {
        /* 坏 JSON 当未设置 */
      }
    }
  }
  return touched ? next : null
}

async function pushOnce(cfg: AlertQuietConfig): Promise<void> {
  const snap = await getConfigSnapshot()
  await saveConfig(
    {
      [QUIET_COOLDOWN_KEY]: String(cfg.cooldownMs),
      [QUIET_WINDOWS_KEY]: JSON.stringify(cfg.windows),
    },
    snap.revision,
  )
}

/**
 * 首次拉服务端值：有了就采纳（覆盖本机），没有就把非默认本机值写回认领。
 * 每上下文只生效一次（面板重复挂载不重拉）；失败留 false 供下次挂载重试。
 * 返回值只表达「服务端通道可用」；失败已是 quietSyncState，不向上抛。
 */
export async function primeQuietFromServer(): Promise<boolean> {
  if (primedOnce) return true
  try {
    const snap = await getConfigSnapshot()
    const adopt = parseServerQuiet(snap.values)
    if (adopt) {
      saveQuiet(adopt)
      quietSyncVersion.value += 1
    } else {
      const local = loadQuiet()
      if (local.cooldownMs !== 60_000 || local.windows.length > 0) await pushOnce(local)
    }
    primedOnce = true
    quietSyncState.value = 'ok'
    return true
  } catch (e) {
    // prime 阶段的 403：服务端没有这两键（未认领）且本机有值 → viewer 也写不进去。
    quietSyncState.value = isForbidden(e) ? 'readonly' : 'error'
    return false
  }
}

/**
 * 本机保存后调用（watch/增删窗路径统一走 saveQuiet → 本函数调度）。
 * 防抖合并连击改窗；读-改-写两跳起 CAS，撞 409 重读再试一次，
 * 仍撞/校验拒绝则采纳服务端值告知面板——多窗并发最后一致性交给服务端为准。
 */
export function scheduleQuietPush(): void {
  if (pushTimer) clearTimeout(pushTimer)
  pushTimer = setTimeout(() => {
    pushTimer = null
    void pushQuietToServer()
  }, PUSH_DEBOUNCE_MS)
}

export async function pushQuietToServer(): Promise<boolean> {
  const cfg = loadQuiet()
  try {
    await pushOnce(cfg)
  } catch (e) {
    if (isForbidden(e)) {
      quietSyncState.value = 'readonly'
      return false
    }
    if (!isConflict(e)) {
      quietSyncState.value = 'error'
      return false
    }
    // CAS 冲突一次：重读快照按当前本机值再写；仍撞则服务端为准回采本机。
    let lastErr: unknown
    try {
      await pushOnce(cfg)
      quietSyncState.value = 'ok'
      return true
    } catch (e2) {
      lastErr = e2
    }
    if (isConflict(lastErr)) {
      try {
        const snap = await getConfigSnapshot()
        const adopt = parseServerQuiet(snap.values)
        if (adopt) {
          saveQuiet(adopt)
          quietSyncVersion.value += 1
        }
        quietSyncState.value = 'ok'
        return true
      } catch {
        /* 回采失败：记 error，本机值仍是即时态 */
      }
    }
    quietSyncState.value = isForbidden(lastErr) ? 'readonly' : 'error'
    return false
  }
  quietSyncState.value = 'ok'
  return true
}
