/** 账户与持仓（SIM/LIVE 账本切换）。原 cockpit 账户半边。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getAccount, listPositions, type Position } from '@/api/trade'
import { quiet } from '@/utils/async'

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

  async function refresh() {
    const b = book.value || 'SIM'
    const [acc, pos] = await Promise.all([quiet(() => getAccount(b)), quiet(() => listPositions(b))])
    if (acc) {
      equity.value = acc.equity
      cash.value = acc.cash
      unrealized.value = acc.unrealized
      realized.value = acc.realized
      dayPnl.value = acc.dayPnl
      paperDays.value = acc.paperDays
      openHalted.value = acc.openHalted
      haltReason.value = acc.haltReason
    }
    if (pos) positions.value = pos
  }

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
    refresh,
  }
})
