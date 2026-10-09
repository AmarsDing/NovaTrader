/** 运维总览（M12 自检 + 数据源状态）。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { opsStatus, type OpsStatus } from '@/api/ops'
import { quiet } from '@/utils/async'

export const useOpsStore = defineStore('ops', () => {
  const status = ref<OpsStatus | null>(null)
  const loading = ref(false)
  const at = ref('')

  async function refresh() {
    loading.value = true
    try {
      const data = await quiet(() => opsStatus())
      if (data) {
        status.value = data
        at.value = new Date().toISOString()
      }
    } finally {
      loading.value = false
    }
  }

  return { status, loading, at, refresh }
})
