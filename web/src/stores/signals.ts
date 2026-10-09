/** 策略信号流。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { listSignals, type SignalItem } from '@/api/signals'
import { quiet } from '@/utils/async'

export const useSignalsStore = defineStore('signals', () => {
  const items = ref<SignalItem[]>([])
  const loading = ref(false)

  async function refresh() {
    loading.value = true
    try {
      const list = await quiet(() => listSignals())
      if (list) items.value = list
    } finally {
      loading.value = false
    }
  }

  return { items, loading, refresh }
})
