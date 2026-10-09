/** 风控状态与动作（Kill Switch 常驻）。原 cockpit 风控半边。 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getRiskState, resetKillSwitch, setMode, triggerKillSwitch, updateParam } from '@/api/risk'
import type { KillBody, ModeBody, ParamBody } from '@/api/risk'
import { quiet } from '@/utils/async'

export const useRiskStore = defineStore('risk', () => {
  const mode = ref('')
  const killActive = ref(false)
  const killReason = ref('')
  const killSource = ref('')
  const killAt = ref('')
  const params = ref<Record<string, unknown>>({})
  const buysToday = ref<number | null>(null)
  const marketBreaker = ref('')

  async function refresh() {
    const state = await quiet(() => getRiskState())
    if (state) {
      mode.value = state.mode
      killActive.value = state.killActive
      killReason.value = state.killReason
      killSource.value = state.killSource
      killAt.value = state.killAt
      params.value = state.params
      buysToday.value = state.buysToday
      marketBreaker.value = state.marketBreaker
    }
  }

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
    refresh,
    trigger,
    reset,
    switchMode,
    saveParam,
  }
})
