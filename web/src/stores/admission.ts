/**
 * 实盘准入信封单源（FE-T4）：总闸 liveEnabled 与通道 brokerConnected 只认 GetAdmission 回执信封。
 * 此前 TradeView/trade-bar 前端写死 liveAllowed=false，与信封构成双真相（方案 §2.6-T4）——
 * 本店收编后 LIVE 可选性只走信封旗标；取数失败 fail-closed（旗标翻回 false，不留过期「已开」门板）。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getAdmission, type AdmissionEnvelope, type AdmissionReport } from '@/api/admission'
import { messageOf } from '@/utils/format'
import { serverNow } from '@/utils/server-clock'

export const useAdmissionStore = defineStore('admission', () => {
  const report = ref<AdmissionReport | null>(null)
  const liveEnabled = ref(false)
  const brokerConnected = ref(false)
  /** 最近一次取数成功落笔（服务端钟，P2-10）；0=从未取到。 */
  const checkedAt = ref(0)
  const error = ref('')
  const busy = ref(false)
  let inflight: Promise<boolean> | null = null
  let timer: ReturnType<typeof setInterval> | null = null

  function applyEnvelope(env: AdmissionEnvelope): void {
    report.value = env.report
    liveEnabled.value = env.liveEnabled
    brokerConnected.value = env.brokerConnected
    checkedAt.value = serverNow()
    error.value = ''
  }

  /** 取数失败 fail-closed：证据与双闸旗标一并翻回，不沿用上一次的值虚报。 */
  function refresh(): Promise<boolean> {
    if (inflight) return inflight
    busy.value = true
    inflight = (async () => {
      try {
        applyEnvelope(await getAdmission())
        return true
      } catch (err) {
        report.value = null
        liveEnabled.value = false
        brokerConnected.value = false
        error.value = messageOf(err)
        return false
      } finally {
        busy.value = false
        inflight = null
      }
    })()
    return inflight
  }

  /** 超过 maxAge 未刷新才真取（TradeView 轮询与下单条轮询共用去重；在途请求不重发）。 */
  function ensureFresh(maxAgeMs = 30_000): void {
    if (!checkedAt.value || serverNow() - checkedAt.value > maxAgeMs) void refresh()
  }

  /** 30s 静候档轮询（与 readiness/数据源谱同档）：幂等启动，document.hidden 跳拍。 */
  function startPolling(intervalMs = 30_000): void {
    if (timer) return
    void refresh()
    timer = setInterval(() => {
      if (!document.hidden) void refresh()
    }, intervalMs)
  }

  return { report, liveEnabled, brokerConnected, checkedAt, error, busy, applyEnvelope, refresh, ensureFresh, startPolling }
})
