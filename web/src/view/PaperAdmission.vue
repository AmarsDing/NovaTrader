<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { getAdmission, type AdmissionReport } from '@/api/admission'
import { clock, messageOf } from '@/utils/format'
import { createPageRefresh } from './page-refresh'
import NtButton from '@/ui/NtButton.vue'
const report = ref<AdmissionReport | null>(null),
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
      report.value = r
      error.value = ''
      return true
    } catch (e) {
      if (!disposed) {
        report.value = null
        error.value = messageOf(e)
      }
      return false
    } finally {
      if (!disposed) busy.value = false
    }
  },
})
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
      <NtButton size="sm" :loading="busy" @click="refresh.refresh()">核验</NtButton>
    </header>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="!report" class="note">真实交易日证据尚未核验；连续合格天数未知。</p>
    <template v-else>
      <div class="headline">
        <strong
          >{{ report.consecutive_days }}<small> / {{ report.required_days }} 个连续合格交易日</small></strong
        ><span>{{ report.run_evidence_ready ? '运行证据已达门槛' : '运行证据尚未达标' }}</span>
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
                  <b :class="{ good: d.status === 'qualified' }">{{
                    d.status === 'qualified' ? '合格' : d.status === 'running' ? '运行中' : '证据不足'
                  }}</b>
                  <p v-for="(n, reason) in d.reasons" :key="reason">{{ reasons[reason] || reason }} · {{ n }}</p>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
      <p class="authority">
        实盘执行权限：未开放。运行天数达标后，仍需完成 {{ report.other_required_evidence.join('、') }}。
      </p>
    </template>
  </section>
</template>
<style scoped>
.admission {
  padding: var(--nt-pad-panel);
  margin-block: 12px;
  background: var(--nt-glass-sheen), var(--nt-glass-bg);
  border: 1px solid var(--nt-accent-border);
  border-radius: var(--nt-radius-m);
  box-shadow: var(--nt-edge-light), var(--nt-shadow-depth);
  backdrop-filter: var(--nt-glass-filter);
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
  font: 10px var(--nt-font-mono);
  letter-spacing: 0.16em;
  color: var(--nt-accent);
}
h3 {
  margin: 4px 0 12px;
  font-size: 14px;
}
.headline strong {
  font: 24px var(--nt-font-mono);
  color: var(--nt-text-1);
}
small {
  font-size: 11px;
  color: var(--nt-text-2);
}
.headline span {
  font-size: 12px;
  color: var(--nt-warn);
}
.note,
.authority,
details {
  font-size: 11px;
  color: var(--nt-text-2);
  line-height: 1.7;
}
.authority {
  padding: 8px;
  border-left: 2px solid var(--nt-accent);
  background: var(--nt-bg-1);
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
  color: var(--nt-warn);
}
b.good {
  color: var(--nt-accent);
}
</style>
