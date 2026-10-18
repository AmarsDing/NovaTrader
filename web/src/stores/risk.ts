/** 风控状态与动作（Kill Switch 常驻）。原 cockpit 风控半边。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getRiskState, resetKillSwitch, setMode, triggerKillSwitch, updateParam } from '@/api/risk'
import type { KillBody, ModeBody, ParamBody, RiskStateView } from '@/api/risk'
import { quiet } from '@/utils/async'
import { createRefreshQueue } from '@/utils/refresh-queue'

export const useRiskStore = defineStore('risk', () => {
  const mode = ref('')
  const killActive = ref(false)
  const killReason = ref('')
  const killSource = ref('')
  const killAt = ref('')
  const params = ref<Record<string, unknown>>({})
  const buysToday = ref<number | null>(null)
  const marketBreaker = ref('')
  const ready = ref(false)
  const checkedAt = ref(0)
  const snapshot = ref<RiskStateView | null>(null)

  const refresh = createRefreshQueue(async () => {
    const state = await quiet(() => getRiskState())
    ready.value = !!state && state.engineReady !== false && state.configReady !== false && ['L0', 'L1', 'L2', 'L3'].includes(state.mode)
    checkedAt.value = Date.now()
    if (ready.value && state) {
      snapshot.value = state
      mode.value = state.mode
      killActive.value = state.killActive
      killReason.value = state.killReason
      killSource.value = state.killSource
      killAt.value = state.killAt
      params.value = state.params
      buysToday.value = state.buysToday
      marketBreaker.value = state.marketBreaker
    }
    return ready.value
  })

  async function trigger(body: KillBody) {
    await triggerKillSwitch(body)
    await refresh()
  }

  async function reset(body: KillBody) {
    await resetKillSwitch(body)
    await refresh()
  }

  async function switchMode(body: ModeBody) {
    await setMode(body)
    await refresh()
  }

  async function saveParam(body: ParamBody) {
    await updateParam(body)
    await refresh()
  }

  return {
    mode,
    killActive,
    killReason,
    killSource,
    killAt,
    params,
    buysToday,
    marketBreaker,
    ready,
    checkedAt,
    snapshot,
    refresh,
    trigger,
    reset,
    switchMode,
    saveParam,
  }
})
