/** 委托与成交流水。SIM/LIVE 切换随 account.book。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { cancelOrder, confirmOrder, listFills, listOrders, placeOrder } from '@/api/trade'
import type { Fill, Order, OrderDraft } from '@/api/trade'
import { quiet } from '@/utils/async'

export const useTradeStore = defineStore('trade', () => {
  const orders = ref<Order[]>([])
  const fills = ref<Fill[]>([])
  const loading = ref(false)

  async function refresh(account: string) {
    loading.value = true
    try {
      const [orderList, fillList] = await Promise.all([
        quiet(() => listOrders(account)),
        quiet(() => listFills(account)),
      ])
      if (orderList) orders.value = orderList
      if (fillList) fills.value = fillList
    } finally {
      loading.value = false
    }
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

  return { orders, fills, loading, refresh, place, cancel, confirm }
})
