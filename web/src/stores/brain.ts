/** 智脑运行状态（M05 模型分层统计）；晨报/追问由各面板自拉。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { brainStats, type TierStats } from '@/api/brain'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'

export const useBrainStore = defineStore('brain', () => {
  const tiers = ref<TierStats[]>([])
  const loading = ref(false)
  const ready = ref(false)

  const refresh = createRefreshQueue(async () => {
    loading.value = true
    try {
      const stats = await quiet(() => brainStats())
      if (stats) tiers.value = stats
      ready.value = stats != null
      return ready.value
    } finally {
      loading.value = false
    }
  })

  return { tiers, loading, ready, refresh }
})
