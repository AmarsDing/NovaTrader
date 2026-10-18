/** 委托与成交流水。SIM/LIVE 切换随 account.book。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { cancelOrder, confirmOrder, listFills, listOrders, placeOrder } from '@/api/trade'
import type { Fill, Order, OrderDraft } from '@/api/trade'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'

export const useTradeStore = defineStore('trade', () => {
  const orders = ref<Order[]>([])
  const fills = ref<Fill[]>([])
  const loading = ref(false)
  const ready = ref(false)
  const book = ref('')
  let sequence = 0

  const queuedRefresh = createRefreshQueue(async (account) => {
    if (book.value !== account) return false
    const requestSequence = ++sequence
    loading.value = true
    try {
      const [orderList, fillList] = await Promise.all([
        quiet(() => listOrders(account)),
        quiet(() => listFills(account)),
      ])
      if (requestSequence !== sequence || book.value !== account) return false
      ready.value = orderList != null && fillList != null
      if (orderList) orders.value = orderList
      if (fillList) fills.value = fillList
      return ready.value
    } finally {
      if (requestSequence === sequence) loading.value = false
    }
  })
  function refresh(account: string) {
    if (book.value !== account) { ready.value = false; orders.value = []; fills.value = [] }
    book.value = account
    return queuedRefresh(account)
  }

  async function place(draft: OrderDraft) {
    await placeOrder(draft)
    await refresh(draft.account || 'SIM')
  }

  async function cancel(account: string, clientOrderId: string) {
    await cancelOrder(clientOrderId)
    await refresh(account)
  }

  async function confirm(account: string, clientOrderId: string) {
    await confirmOrder(clientOrderId)
    await refresh(account)
  }

  return { orders, fills, loading, ready, book, refresh, place, cancel, confirm }
})
