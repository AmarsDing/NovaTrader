/** 策略信号流。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { listSignals, type SignalItem } from '@/api/signals'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'

export const useSignalsStore = defineStore('signals', () => {
  const items = ref<SignalItem[]>([])
  const loading = ref(false)
  const ready = ref(false)

  const refresh = createRefreshQueue(async () => {
    loading.value = true
    try {
      const list = await quiet(() => listSignals())
      if (list) items.value = list
      ready.value = list != null
      return ready.value
    } finally {
      loading.value = false
    }
  })

  return { items, loading, ready, refresh }
})
