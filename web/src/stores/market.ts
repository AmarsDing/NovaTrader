/** 市场情绪与板块热力。原 cockpit 行情半边；K线序列仍在各组件本地。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getSentiment, listSectors, type SectorRow } from '@/api/market'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'

export const useMarketStore = defineStore('market', () => {
  const phase = ref('')
  const positionScale = ref<number | null>(null)
  const sentimentStale = ref(false)
  const sentimentAsOf = ref('')
  const up = ref<number | null>(null)
  const down = ref<number | null>(null)
  const brokenRate = ref<number | null>(null)
  const sectors = ref<SectorRow[]>([])
  const ready = ref(false)
  const checkedAt = ref(0)

  const refresh = createRefreshQueue(async () => {
    const [sentiment, sectorRows] = await Promise.all([quiet(() => getSentiment()), quiet(() => listSectors(8))])
    if (sentiment) {
      phase.value = sentiment.phase
      positionScale.value = sentiment.positionScale
      sentimentStale.value = sentiment.stale
      sentimentAsOf.value = sentiment.asOf
      up.value = sentiment.up
      down.value = sentiment.down
      brokenRate.value = sentiment.brokenRate
    }
    if (sectorRows) sectors.value = sectorRows
    ready.value = !!sentiment && !!sentiment.asOf && sectorRows != null
    if (!sentiment) sentimentStale.value = true
    checkedAt.value = Date.now()
    return ready.value
  })

  return {
    phase,
    positionScale,
    sentimentStale,
    sentimentAsOf,
    up,
    down,
    brokenRate,
    sectors,
    ready,
    checkedAt,
    refresh,
  }
})
