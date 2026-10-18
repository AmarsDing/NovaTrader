/** 账户与持仓（SIM/LIVE 账本切换）。原 cockpit 账户半边。 */
import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { getAccount, listPositions, type AccountSnapshot, type Position } from '@/api/trade'
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

  const queuedRefresh = createRefreshQueue(async (b) => {
    if (b !== (book.value || 'SIM')) return false
    const sequence = ++refreshSequence
    const [acc, pos] = await Promise.all([quiet(() => getAccount(b)), quiet(() => listPositions(b))])
    if (sequence !== refreshSequence || b !== book.value) return false
    accountOk.value = !!acc && acc.account.toUpperCase() === b.toUpperCase() && acc.equity != null && acc.cash != null
    checkedAt.value = Date.now()
    if (accountOk.value && acc) {
      snapshot.value = acc
      equity.value = acc.equity
      cash.value = acc.cash
      unrealized.value = acc.unrealized
      realized.value = acc.realized
      dayPnl.value = acc.dayPnl
      paperDays.value = acc.paperDays
      openHalted.value = acc.openHalted
      haltReason.value = acc.haltReason
    }
    positionsOk.value = pos != null
    if (pos) positions.value = pos
    return accountOk.value && positionsOk.value
  })
  function refresh() { return queuedRefresh(book.value || 'SIM') }
  watch(book, () => {
    refreshSequence++
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
  }
})
