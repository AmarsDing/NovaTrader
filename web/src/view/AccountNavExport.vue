<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { exportNav, type NavExport } from '@/api/nav'
import { amount, clock, messageOf } from '@/utils/format'
import NtButton from '@/ui/NtButton.vue'
const props = defineProps<{ manifestId?: string }>()
const emit = defineEmits<{ 'use-input': [{ inputJson: string; manifestId: string; asOf: string }] }>()
const today = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
}).format(new Date())
const draft = ref({
  start_date: today.slice(0, 7) + '-01',
  end_date: today,
  manifest_id: props.manifestId || '',
  benchmark_json: '',
})
const result = ref<NavExport | null>(null),
  busy = ref(false),
  error = ref('')
let generation = 0,
  disposed = false
watch(
  draft,
  () => {
    generation++
    result.value = null
    error.value = ''
  },
  { deep: true, flush: 'sync' },
)
watch(
  () => props.manifestId,
  (id) => {
    if (id) draft.value.manifest_id = id
  },
)
const labels: Record<string, string> = {
  missing_close_snapshot: '缺少实际收盘快照',
  missing_snapshot: '缺少实际快照',
  valuation_provenance_unknown: '估值来源尚未证实',
  missing_valuation_provenance: '缺少估值来源证据',
  account_unreconciled: '账户尚未完成对账',
  cash_flow_unknown: '现金流尚未对平',
  benchmark_missing: '显式基准未提供',
  manifest_missing: '冻结数据集未提供',
  trading_day_not_closed: '该交易日尚未完成闭市核验',
  actual_closing_snapshot_missing: '缺少实际收盘快照',
  full_snapshot_identity_invalid_or_legacy: '快照身份不完整或为旧版数据',
  reconciliation_unmatched_or_stale: '对账未匹配或对账时刻陈旧',
  day_start_unknown: '日初权益基线未知',
  nonpositive_equity: '权益非正值，不能生成收益率',
  closing_valuation_provenance_unknown: '缺少持仓收盘估值的来源证明',
  cash_flow_baseline_unknown: '缺少相邻真实快照现金基线',
  cash_delta_not_explained_by_actual_fills: '现金变化不能由实际成交与费用解释',
  manifest_required_or_unavailable: '冻结数据集未提供或不可用',
  manifest_unavailable: '冻结数据集未找到',
  manifest_identity_invalid: '冻结数据集身份或哈希不一致',
  manifest_calendar_not_covering_nav: '冻结数据集未覆盖全部净值交易日',
  manifest_cutoff_precedes_nav_observation: '数据集截止早于实际净值观察时间',
  explicit_aligned_benchmark_required: '需要与全部净值日期对齐的显式基准',
  benchmark_contract_invalid: '基准输入合同无效',
  benchmark_dates_not_aligned: '基准日期、类型或数值未正确对齐',
}
function label(reason: string) {
  const match = /^(\d{4}-\d{2}-\d{2}):(.*)$/.exec(reason)
  return match ? `${match[1]} · ${labels[match[2]!] || match[2]}` : labels[reason] || reason
}
async function load() {
  if (busy.value) return
  const run = generation
  busy.value = true
  error.value = ''
  result.value = null
  try {
    if (!draft.value.start_date || !draft.value.end_date || draft.value.start_date > draft.value.end_date)
      throw new Error('请选择正确的日期范围')
    if (draft.value.benchmark_json.trim()) JSON.parse(draft.value.benchmark_json)
    const r = await exportNav({ ...draft.value, account: 'SIM', frequency: '1d' })
    if (!disposed && run === generation) result.value = r
  } catch (e) {
    if (!disposed && run === generation) error.value = messageOf(e)
  } finally {
    if (!disposed) busy.value = false
  }
}
function useInput() {
  const r = result.value
  if (r?.inputReady)
    emit('use-input', { inputJson: r.inputJson, manifestId: r.report.manifest_id, asOf: r.report.research_as_of })
}
onBeforeUnmount(() => {
  disposed = true
  generation++
})
</script>
<template>
  <details class="nav-export">
    <summary>从正式模拟账本核验净值</summary>
    <p class="nt-note">
      选择区间后，核验前一交易日初始点、实际收盘估值和现金流。缺失日期保持缺失；历史旧快照没有估值依据时不会进入绩效研究。
    </p>
    <form @submit.prevent="load">
      <label>开始日期<input v-model="draft.start_date" type="date" required /></label
      ><label>结束日期<input v-model="draft.end_date" type="date" required /></label
      ><label>冻结 manifest_id（生成研究输入时必需）<input v-model="draft.manifest_id" maxlength="80" /></label>
      <details class="wide">
        <summary>提供显式基准（可先只核验账本）</summary>
        <p class="nt-note">基准须含初始点及全部交易日，使用已核验的价格指数或全收益指数；系统不会隐式下载。</p>
        <textarea
          v-model="draft.benchmark_json"
          rows="4"
          spellcheck="false"
          placeholder='{"benchmark_id":"已核验的指数","benchmark_kind":"price_index","points":[{"date":"2026-10-09","value":100}]}'
          aria-label="显式基准JSON"
        />
      </details>
      <NtButton type="submit" size="sm" :loading="busy">核验账本净值</NtButton>
    </form>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <template v-if="result"
      ><p class="status">
        {{ result.inputReady ? '研究输入证据完整，可填入待审草稿' : '账本记录已读取，研究输入证据尚未完整' }}
      </p>
      <p class="nt-note">
        初始比较日 {{ result.report.initial_date }} · 交易日历 {{ result.report.calendar_version }} ·
        读取费用后账面权益，估值依据与现金流仍需逐点核验。
      </p>
      <p v-for="r in result.report.reasons" :key="r" class="nt-note">{{ label(r) }}</p>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>日期 / 角色</th>
              <th>实际权益</th>
              <th>估值 / 现金流</th>
              <th>快照与原因</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in result.report.points" :key="p.date">
              <td>
                {{ p.date }}<small>{{ p.role === 'initial' ? '初始点' : '收盘点' }}</small>
              </td>
              <td>{{ amount(p.equity) }}</td>
              <td>
                {{ p.valuation_complete ? '估值已证实' : '估值未证实' }} /
                {{ p.cash_flow_known ? '现金流已对平' : '现金流未知' }}
              </td>
              <td>
                <small>快照 {{ p.generation || '未知' }} · {{ clock(p.as_of) || '未返回' }}</small
                ><small v-for="r in p.reasons" :key="r">{{ label(r) }}</small>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="result.report.missing_dates.length" class="nt-note">
        缺失实际收盘记录：{{ result.report.missing_dates.join('、') }}。
      </p>
      <NtButton size="sm" :disabled="!result.inputReady" @click="useInput">将合格输入填入研究草稿</NtButton>
      <p class="nt-note">仅填入草稿，不建立任务或提交订单。</p>
      <details>
        <summary>精确身份与输入哈希</summary>
        <p>账本 {{ result.ledgerHash }}</p>
        <p>输入 {{ result.inputSha256 || '未形成' }}</p>
      </details>
    </template>
  </details>
</template>
<style scoped>
.nav-export {
  margin-top: 14px;
  padding: 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  font-size: 12px;
}
summary {
  cursor: pointer;
  color: var(--nt-text-1);
}
form {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px;
  align-items: end;
}
label {
  display: grid;
  gap: 5px;
  color: var(--nt-text-2);
  font-size: 11px;
}
input,
textarea {
  width: 100%;
  box-sizing: border-box;
  background: var(--nt-bg-2);
  color: var(--nt-text-1);
  border: 1px solid var(--nt-border-1);
  padding: 7px;
  border-radius: var(--nt-radius-s);
}
.wide {
  grid-column: 1/-1;
}
.status {
  color: var(--nt-accent);
  font-weight: 600;
}
.table-wrap {
  overflow-x: auto;
  margin-block: 10px;
}
table {
  width: 100%;
  border-collapse: collapse;
  white-space: nowrap;
  font: 11px var(--nt-font-mono);
}
th,
td {
  padding: 8px;
  text-align: left;
  border-bottom: 1px solid var(--nt-border-1);
  vertical-align: top;
}
th,
small {
  color: var(--nt-text-2);
}
small {
  display: block;
  margin-top: 4px;
}
p {
  overflow-wrap: anywhere;
}
</style>
