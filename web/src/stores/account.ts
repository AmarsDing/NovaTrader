/** 账户与持仓（SIM/LIVE 账本切换）。原 cockpit 账户半边。 */
import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { getAccount, listPositions, type AccountSnapshot, type Position } from '@/api/trade'
import { parseAccountPush } from '@/api/account-push'
import type { WsEventMetadata } from '@/ws/topics'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'

export const useAccountStore = defineStore('account', () => {
  const book = ref('SIM')
  const equity = ref<number | null>(null)
  const cash = ref<number | null>(null)
  const unrealized = ref<number | null>(null)
  const realized = ref<number | null>(null)
  const dayPnl = ref<number | null>(null)
  const paperDays = ref<number | null>(null)
  const openHalted = ref(false)
  const haltReason = ref('')
  const positions = ref<Position[]>([])
  /** 持仓接口有没有读到。false 时界面不能把账面写成空仓。 */
  const positionsOk = ref(false)
  const accountOk = ref(false)
  const checkedAt = ref(0)
  const snapshot = ref<AccountSnapshot | null>(null)
  let refreshSequence = 0
  /** 当前 book 已随推送应用的实体版本（accountstate generation）。 */
  let appliedGeneration = 0
  /** 推送增量应用次数：慢 REST 响应的栅栏（飞过期间不许覆盖推送字段）。 */
  let applications = 0

  const queuedRefresh = createRefreshQueue(async (b) => {
    if (b !== (book.value || 'SIM')) return false
    const sequence = ++refreshSequence
    const applicationsAtStart = applications
    const [acc, pos] = await Promise.all([quiet(() => getAccount(b)), quiet(() => listPositions(b))])
    if (sequence !== refreshSequence || b !== book.value) return false
    // 飞行期间落了更新的推送版本：REST 数据按实体版本已陈旧，
    // equity/cash/positions/unrealized 保留推送口径，REST 只补它独有的字段。
    const pushWon = applications !== applicationsAtStart
    if (pushWon) {
      if (acc && acc.account.toUpperCase() === b.toUpperCase()) {
        snapshot.value = acc
        realized.value = acc.realized
        paperDays.value = acc.paperDays
        openHalted.value = acc.openHalted
        haltReason.value = acc.haltReason
      }
      return accountOk.value && positionsOk.value
    }
    accountOk.value = !!acc && acc.account.toUpperCase() === b.toUpperCase() && acc.equity != null && acc.cash != null
    checkedAt.value = Date.now()
    if (accountOk.value && acc) {
      snapshot.value = acc
      equity.value = acc.equity
      cash.value = acc.cash
      unrealized.value = acc.unrealized
      realized.value = acc.realized
      // REST 无当日盈亏口径时常为 null：不清掉推送推导出的口径。
      if (acc.dayPnl != null) dayPnl.value = acc.dayPnl
      paperDays.value = acc.paperDays
      openHalted.value = acc.openHalted
      haltReason.value = acc.haltReason
    }
    positionsOk.value = pos != null
    if (pos) positions.value = pos
    return accountOk.value && positionsOk.value
  })
  function refresh() { return queuedRefresh(book.value || 'SIM') }

  /**
   * position 主题账户快照推送增量（N1-02）：载荷是完整账户态时按实体版本直接应用，
   * 免掉 getAccount/listPositions 两个 REST。返回 true 表示这条推送已被账户域消化
   * （含已覆盖过的重放）；false = 提示帧/别的账本/残缺载荷，调用方须整域回退刷新。
   * 推送不携带 realized/paperDays/openHalted/haltReason：仍由 REST 轮询补齐。
   */
  function applyPush(payload: unknown, metadata?: WsEventMetadata): boolean {
    const parsed = parseAccountPush(payload, metadata)
    if (!parsed || parsed.book !== (book.value || 'SIM').toUpperCase()) return false
    // 乱序/重放的旧版本不影响既有状态，但也不需要 REST 回退。
    if (parsed.generation <= appliedGeneration) return true
    appliedGeneration = parsed.generation
    applications++
    equity.value = parsed.equity
    cash.value = parsed.cash
    unrealized.value = parsed.unrealized
    if (parsed.dayPnl != null) dayPnl.value = parsed.dayPnl
    positions.value = parsed.positions
    accountOk.value = true
    positionsOk.value = true
    checkedAt.value = Date.now()
    return true
  }

  watch(book, () => {
    refreshSequence++
    appliedGeneration = 0
    accountOk.value = false
    positionsOk.value = false
    checkedAt.value = 0
    snapshot.value = null
    equity.value = cash.value = unrealized.value = realized.value = dayPnl.value = paperDays.value = null
    positions.value = []
    openHalted.value = false
    haltReason.value = ''
  }, { flush: 'sync' })

  return {
    book,
    equity,
    cash,
    unrealized,
    realized,
    dayPnl,
    paperDays,
    openHalted,
    haltReason,
    positions,
    positionsOk,
    accountOk,
    checkedAt,
    snapshot,
    refresh,
    applyPush,
  }
})
