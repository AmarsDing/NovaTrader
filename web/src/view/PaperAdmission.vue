<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getAdmission, grantLive, revokeLive, type AdmissionReport } from '@/api/admission'
import { getOpsReadiness, type ReadinessGate } from '@/api/ops'
import { useAuthStore } from '@/stores/auth'
import { clock, messageOf } from '@/utils/format'
import { openDeeplink } from '@/utils/deeplink'
import { blockingGaps, gateItems, LIVE_GATE_ADVISORY } from '@/utils/preflight'
import { createPageRefresh } from './page-refresh'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtModal from '@/ui/NtModal.vue'
import NtPreflightChecklist, { type PreflightItem } from '@/display/NtPreflightChecklist.vue'
const auth = useAuthStore()
const report = ref<AdmissionReport | null>(null),
  liveEnabled = ref(false),
  brokerConnected = ref(false),
  error = ref(''),
  busy = ref(false)
let disposed = false,
  timer: ReturnType<typeof setInterval> | undefined
const refresh = createPageRefresh({
  key: () => 'SIM:default',
  pushIntervalMs: 0,
  read: async () => {
    busy.value = true
    try {
      const r = await getAdmission()
      if (disposed) return false
      report.value = r.report
      liveEnabled.value = r.liveEnabled
      brokerConnected.value = r.brokerConnected
      error.value = ''
      return true
    } catch (e) {
      if (!disposed) {
        report.value = null
        // 总闸/通道是信封旗标：取数失败后旧牌翻回关闭态（fail-closed 显示，不留过期「已开」假绿）。
        liveEnabled.value = false
        brokerConnected.value = false
        error.value = messageOf(e)
      }
      return false
    } finally {
      if (!disposed) busy.value = false
    }
  },
})
/* ---------- W3c 启用模态：实盘授权申请 + 收回（同件双态） ----------
 * 申请=清单式前置检查（R-UI-30/36）：必堵项只有准入证据（服务端同口径 fail-closed），
 * 链路/通道摆提示行不拦；收回=审计开关确认，提示 fail-closed 即时生效的口径。 */
const grantOpen = ref(false),
  granting = ref(false),
  grantError = ref('')
const gates = ref<ReadinessGate[]>([])
let grantFetchSeq = 0
async function openGrant() {
  /* 双保险：按钮禁用状态外，句内再截一次（viewer/无操作人不开模态，不留提交通路）。 */
  if (auth.isViewer() || !auth.operatorName) return
  grantError.value = ''
  grantOpen.value = true
  /* 不带上轮快照进场：取到前提示行全按「未证实」呈现（没取到≠0），就绪口不可达同口径不锁申请。 */
  gates.value = []
  const seq = ++grantFetchSeq
  try {
    const r = await getOpsReadiness()
    /* 代际+卸载双守门：重开/卸载后晚到的旧响应不得盖新（R-UI-35 慢响应顾序纪律）。 */
    if (seq === grantFetchSeq && !disposed) gates.value = r.gates
  } catch {
    /* 就绪口不可达：提示行保持未证实，不锁申请 */
  }
}
const grantItems = computed<PreflightItem[]>(() => {
  const r = report.value
  const items: PreflightItem[] = [
    {
      key: 'run_evidence',
      label: '连续模拟运行证据',
      state: r ? (r.run_evidence_ready ? 'ok' : 'warn') : 'unknown',
      why: r ? (r.run_evidence_ready ? undefined : '连续合格交易日未满，服务端会拒绝授权') : '证据未读到——按未证实处理',
      progress: r ? { done: r.consecutive_days, total: r.required_days } : undefined,
    },
  ]
  /* other_required_evidence 是长期待办（授权不拦），逐条摆提示行——不静默吞掉。 */
  for (const t of r?.other_required_evidence ?? []) {
    items.push({ key: `ore:${t}`, label: t, state: 'warn', blocking: false, why: '不构成授权前置，但长期缺失应保持人工审慎' })
  }
  items.push(...gateItems(gates.value, LIVE_GATE_ADVISORY))
  /* 通道行唯一出处=admission 信封旗标（同页真相）；就绪口 broker_gw 不并行摆第二行（双源只会互相矛盾）。 */
  items.push({
    key: 'broker_channel',
    label: '文件单通道',
    state: brokerConnected.value ? 'ok' : 'warn',
    blocking: false,
    why: brokerConnected.value ? undefined : '通道离线时新实盘委托 fail-closed；不在线也可先授权',
  })
  return items
})
const grantBlockText = computed(() => {
  if (auth.isViewer()) return '当前是查看角色，不能改实盘授权'
  if (!auth.operatorName) return '操作人身份缺失，不能留痕授权'
  const g = blockingGaps(grantItems.value)
  if (g.length) return g.map((i) => `${i.label}${i.why ? `：${i.why}` : ''}`).join('；')
  return ''
})
async function submitGrant(revokeMode: boolean) {
  /* 提交点复核与开场同源：会话中途降级/登出后不得发出空 operator 的审计请求。 */
  if (auth.isViewer() || !auth.operatorName) return
  granting.value = true
  grantError.value = ''
  try {
    /* 回执即翻闸后真信（双闸旗标+重算报告）：直接回填，不再补 GET。 */
    const env = revokeMode ? await revokeLive(auth.operatorName) : await grantLive(auth.operatorName)
    if (!disposed) {
      report.value = env.report
      liveEnabled.value = env.liveEnabled
      brokerConnected.value = env.brokerConnected
      error.value = ''
      grantOpen.value = false
    }
  } catch (e) {
    grantError.value = messageOf(e)
  } finally {
    granting.value = false
  }
}
/** 清单「去处理」：先收模态再跳修复位——全屏 scrim 不关会遮住刚打开的目标面板（跳转即白点）。 */
function onGuide(link: string) {
  grantOpen.value = false
  openDeeplink(link)
}
const reasons: Record<string, string> = {
  missing_session_slots: '盘中采样缺失',
  closing_proof_missing: '闭市对账证据缺失',
  trading_day_not_final: '当日尚未结算',
  account_unknown_or_stale: '账户快照缺失或陈旧',
  account_day_start_unknown: '日初基线未确认',
  ledger_or_reconciliation_unmatched: '账本对账未匹配',
  risk_unknown_or_stale: '风险状态未就绪或陈旧',
  killswitch_unknown_or_active: '急停状态未知或已触发',
  configuration_not_loaded: '配置加载版本未对齐',
  sim_mode_not_active: '未运行于 SIM 授权模式',
  unresolved_execution_identity: '存在原单待核对',
  outbox_unavailable_or_backlogged: '事件投递异常或积压',
  market_universe_unknown: '全市场覆盖基数未知',
  market_coverage_insufficient: '全市场覆盖不足',
  market_source_time_stale: '行情源时间陈旧',
  closing_market_evidence_missing: '收盘行情证据不足',
  market_source_unknown: '行情来源未确认',
  collection_dependency_error: '采样依赖读取失败',
  invalid_evidence_identity: '采样身份或哈希无效',
  collection_too_slow: '证据采集超时',
  future_evidence: '发现未来时刻证据',
}
onMounted(() => {
  void refresh.refresh()
  timer = setInterval(() => {
    if (!document.hidden) void refresh.refresh()
  }, 30_000)
})
onBeforeUnmount(() => {
  disposed = true
  refresh.dispose()
  if (timer) clearInterval(timer)
})
</script>
<template>
  <section class="admission" aria-label="连续模拟运行证据">
    <header>
      <div>
        <span class="eyebrow">PAPER OPERATION EVIDENCE</span>
        <h3>连续模拟运行 · 准入证据</h3>
      </div>
      <div class="head-actions">
        <NtButton size="sm" :loading="busy" @click="refresh.refresh()">核验</NtButton>
        <NtButton
          v-if="report"
          size="sm"
          variant="primary"
          :disabled="auth.isViewer() || !auth.operatorName"
          :disabled-reason="auth.isViewer() ? '当前是查看角色，不能改实盘授权' : !auth.operatorName ? '操作人身份缺失，不能留痕授权' : ''"
          @click="void openGrant()"
          >{{ liveEnabled ? '实盘授权（已开）' : '实盘授权' }}</NtButton
        >
      </div>
    </header>
    <NtErrorGuide :error="error" />
    <p v-if="!report" class="note">真实交易日证据尚未核验；连续合格天数未知。</p>
    <template v-else>
      <div class="headline">
        <strong
          >{{ report.consecutive_days }}<small> / {{ report.required_days }} 个连续合格交易日</small></strong
        ><span :class="{ ok: report.run_evidence_ready }">{{ report.run_evidence_ready ? '运行证据已达门槛' : '运行证据尚未达标' }}</span>
      </div>
      <p class="note">
        截至 {{ clock(report.evaluated_at) }} · 最近已闭市交易日 {{ report.latest_closed_trading_day }}。每 30
        秒留证，盘中采样完整、行情覆盖与闭市对账均合格才计入一天。
      </p>
      <details>
        <summary>查看每日覆盖与阻断原因</summary>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>交易日</th>
                <th>合格采样</th>
                <th>缺失 / 异常</th>
                <th>闭市对账</th>
                <th>结论 / 原因</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="d in report.days" :key="d.trading_day">
                <td>{{ d.trading_day }}</td>
                <td>{{ d.healthy_slots }} / {{ d.required_slots }}</td>
                <td>{{ d.missing_slots }} / {{ d.unhealthy_slots }}</td>
                <td>{{ d.closing_proof ? '已核验' : '未核验' }}</td>
                <td>
                  <!-- 「运行中」是在途不是警告：琥珀只留「证据不足」；合格走 accent。 -->
                  <b :class="{ good: d.status === 'qualified', bad: d.status !== 'qualified' && d.status !== 'running' }">{{
                    d.status === 'qualified' ? '合格' : d.status === 'running' ? '运行中' : '证据不足'
                  }}</b>
                  <p v-for="(n, reason) in d.reasons" :key="reason">{{ reasons[reason] || reason }} · {{ n }}</p>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
      <p class="authority" :class="{ 'is-live': liveEnabled }">
        {{
          liveEnabled
            ? '实盘执行权限：已开（经实盘授权审计开关）。收回后新实盘委托即刻 fail-closed。'
            : `实盘执行权限：未开放。运行天数达标后，仍需完成 ${report.other_required_evidence.join('、')}。`
        }}
      </p>
    </template>
    <!-- W3c 启用模态：申请走清单前置检查（必堵=准入证据）；收回走审计开关确认（同件双态）。 -->
    <NtModal v-model:open="grantOpen" :title="liveEnabled ? '实盘授权（当前已开）' : '申请实盘授权'" width="520px">
      <template v-if="liveEnabled">
        <p class="note">
          收回走同一审计开关关闭：新实盘委托即刻 fail-closed；已提交委托按原编号继续处理，不会自动撤单。
        </p>
      </template>
      <template v-else>
        <p class="note">「必需」项未达不能提交，逐项核对下面状态；链路与通道为提示项，不以在线为准。</p>
        <NtPreflightChecklist :items="grantItems" title="实盘授权前置检查" @navigate="onGuide" />
      </template>
      <NtErrorGuide :error="grantError" />
      <template #footer>
        <NtButton variant="quiet" size="sm" @click="grantOpen = false">先不处理</NtButton>
        <NtButton
          v-if="liveEnabled"
          size="sm"
          variant="danger"
          :loading="granting"
          :disabled="auth.isViewer() || !auth.operatorName"
          @click="void submitGrant(true)"
          >收回实盘授权</NtButton
        >
        <NtButton
          v-else
          size="sm"
          variant="primary"
          :loading="granting"
          :disabled="!!grantBlockText"
          :disabled-reason="grantBlockText"
          @click="void submitGrant(false)"
          >提交实盘授权申请</NtButton
        >
      </template>
    </NtModal>
  </section>
</template>
<style scoped>
/* §6 玻璃只许壳层：本卡内含准入日报数据表（数据背板），去 glass-sheen/blur、accent 描边收边框档，
 * 走 0.94+ 无模糊数据背板。 */
.admission {
  padding: var(--nt-pad-panel);
  margin-block: 12px;
  background: var(--nt-data-bg);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  box-shadow: var(--nt-shadow-depth);
}
header,
.headline {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.eyebrow {
  font: var(--nt-fs-micro) var(--nt-font-mono); /* 收编自 10px（§5.3 micro 档 10.5，抵近档） */
  letter-spacing: 0.16em;
  color: var(--nt-accent);
}
h3 {
  margin: 4px 0 12px;
  font-size: var(--nt-fs-title); /* 收编自 14px（§5.3 title 档 15，归并入档） */
}
.headline strong {
  font: var(--nt-fs-hero) var(--nt-font-mono); /* 收编自 24px（§5.3 hero 档，同值） */
  color: var(--nt-text-1);
}
small {
  font-size: var(--nt-fs-caption); /* 收编自 11px（caption 档 11.5，归并入档） */
  color: var(--nt-text-2);
}
.headline span {
  font-size: var(--nt-fs-caption); /* 收编自 12px（caption 档，归并入档） */
  color: var(--nt-warn); /* 「尚未达标」是真警告，保持琥珀 */
}

.headline span.ok {
  color: var(--nt-text-2); /* 已达门槛退回中性注，警报位让出 */
}
.note,
.authority,
details {
  font-size: var(--nt-fs-caption); /* 收编自 11px（caption 档，归并入档） */
  color: var(--nt-text-2);
  line-height: 1.7;
}
.authority {
  padding: 8px;
  border-left: 2px solid var(--nt-accent);
  background: var(--nt-bg-1);
}
.authority.is-live {
  /* 总闸开着是高危常态事实：走 danger 不走告警琥珀（与 risk-live 色谱一致）。 */
  border-left-color: var(--nt-danger);
}
.head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
summary {
  cursor: pointer;
  color: var(--nt-text-1);
}
.table-wrap {
  overflow-x: auto;
  margin-top: 8px;
}
table {
  width: 100%;
  border-collapse: collapse;
  background: var(--nt-bg-1);
  white-space: nowrap;
}
th,
td {
  padding: 7px;
  text-align: left;
  border-bottom: 1px solid var(--nt-border-1);
  vertical-align: top;
}
td p {
  margin: 0;
}
b {
  color: var(--nt-text-2); /* 默认（含「运行中」）休眠灰；警告只留 .bad */
}
b.good {
  color: var(--nt-accent);
}
b.bad {
  color: var(--nt-warn);
}
</style>
