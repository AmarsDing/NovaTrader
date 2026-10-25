/** 智脑运行状态（M05 模型分层统计）；晨报/追问由各面板自拉。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { brainStats, type TierStats } from '@/api/brain'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'
import { serverNow } from '@/utils/server-clock'

export const useBrainStore = defineStore('brain', () => {
  const tiers = ref<TierStats[]>([])
  const loading = ref(false)
  const ready = ref(false)
  /** 最近一次成功刷新时刻（serverNow 口径）——NtFreshness 锚点；无成功帧=缺失显未知，不敢冒充。 */
  const lastOkAt = ref<number | null>(null)

  const refresh = createRefreshQueue(async () => {
    loading.value = true
    try {
      const stats = await quiet(() => brainStats())
      if (stats) {
        tiers.value = stats
        lastOkAt.value = serverNow()
      }
      ready.value = stats != null
      return ready.value
    } finally {
      loading.value = false
    }
  })

  return { tiers, loading, ready, lastOkAt, refresh }
})
