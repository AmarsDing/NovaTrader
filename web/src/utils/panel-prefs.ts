/**
 * 面板筛选/周期偏好（W3-2）：每个面板一个 namespaced JSON 对象，仅持久化「浏览偏好」
 *（筛选/周期/页签这类不改变数据含义的选择；查询条件如代码、日期范围属业务输入，不入此档）。
 *
 * 纪律：
 * - 字段值白名单校验（align notify-read.ts 模式）：读档只放行 allow 白名单内的字段与值，
 *   非法键值逐条丢弃不拖垮整档；坏 JSON/不可用存储显式当空档（读路径容错，绝不反噬界面）。
 * - 动态选项字段：选项来自服务端数据、挂载时未知的，allow 值写 '*' 放行任意字符串——
 *   调用方必须在选项就绪后校验现值（不在选项内即回默认），动态字段的合法性由消费面兜底。
 * - 写失败静默（偏好不值得一行报错）。
 * - 键名一次定型：'nt.prefs.<面板域>.<用途>.v1'，升版只改尾号，旧档自然失效不迁移（偏好不值得迁移）。
 * - 安全闸：SIM/LIVE 账本等影响执行语义的选择不持久化到此档（会话级 Pinia 是上限），防重启后误以为是旧账本。
 *   已立项豁免：MarketView.account——行情图成交标记读取范围，纯浏览过滤不触执行/下单语义；
 *   豁免理由就地写在消费面板存档注释旁（见 view/MarketView.vue PREFS_ALLOW 处）。
 */
export type PrefAllow = Readonly<Record<string, readonly string[] | '*'>>

function pickStorage(): Pick<Storage, 'getItem' | 'setItem'> | undefined {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : undefined
  } catch {
    return undefined
  }
}

/** 读面板偏好：仅返回 allow 内字段且值合法的条目。 */
export function loadPanelPrefs(
  key: string,
  allow: PrefAllow,
  storage: Pick<Storage, 'getItem'> | undefined = pickStorage(),
): Record<string, string> {
  if (!storage) return {}
  try {
    const raw = storage.getItem(key)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const out: Record<string, string> = {}
    for (const [field, values] of Object.entries(allow)) {
      const v = (parsed as Record<string, unknown>)[field]
      if (typeof v !== 'string') continue
      if (values === '*' ? v.length > 0 : values.includes(v)) out[field] = v
    }
    return out
  } catch {
    return {}
  }
}

/** 写面板偏好：只写 allow 内字段，undefined 表示抹掉该字段（回到默认）。 */
export function savePanelPrefs(
  key: string,
  allow: PrefAllow,
  prefs: Readonly<Record<string, string | undefined>>,
  storage: Pick<Storage, 'setItem'> | undefined = pickStorage(),
): void {
  if (!storage) return
  try {
    const out: Record<string, string> = {}
    for (const field of Object.keys(allow)) {
      const v = prefs[field]
      if (typeof v !== 'string' || !v.length) continue
      const values = allow[field]
      if (values !== '*' && !values.includes(v)) continue
      out[field] = v
    }
    if (Object.keys(out).length === 0) storage.setItem(key, '{}')
    else storage.setItem(key, JSON.stringify(out))
  } catch {
    /* 存储不可用时仅本次会话内存生效 */
  }
}
