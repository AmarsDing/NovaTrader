<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { cancelResearchJob, createResearchJob, listResearchJobs, type ResearchJob } from '@/api/research'
import { clock, messageOf, num, ratioPct } from '@/utils/format'
import NtButton from '@/ui/NtButton.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import { createPageRefresh } from './page-refresh'
import { onPush, onResync, subscribe } from '@/ws/hub'
import AccountNavExport from './AccountNavExport.vue'
import PortfolioPreview from './PortfolioPreview.vue'

const jobs = ref<ResearchJob[]>([]),
  ready = ref(false),
  loading = ref(false),
  writing = ref(false),
  error = ref('')
const selectedId = ref('')
const draftPrepared = ref(false)
function useNavInput(input: { inputJson: string; manifestId: string; asOf: string }) {
  draft.value.kind = 'performance'
  draft.value.manifest_id = input.manifestId
  draft.value.as_of = input.asOf
  draft.value.input_json = input.inputJson
  draftPrepared.value = true
}
const draft = ref({
  kind: 'performance',
  manifest_id: '',
  as_of: '',
  input_json: '',
  options_json: '{}',
  timeout_seconds: 60,
  seed: 0,
})
const selected = computed(() => jobs.value.find((j) => j.id === selectedId.value) ?? null)
const active = (j: ResearchJob) => ['queued', 'running'].includes(j.status)
const output = computed(() => {
  const v = selected.value?.result?.result
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null
})
const metrics = computed(() => {
  const v = output.value?.metrics
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null
})
const weights = computed(() => {
  const v = output.value?.weights
  return v && typeof v === 'object' && !Array.isArray(v)
    ? Object.entries(v).filter(([, n]) => typeof n === 'number')
    : []
})
const kinds: Record<string, string> = {
  performance: '费用后净值 · QuantStats',
  portfolio: '影子组合 · PyPortfolioOpt',
  vectorbt: '可选参数筛选 · vectorbt',
  jev_evaluate: 'Jev 标签评价',
  prediction_archive: '前瞻预测归档',
  prediction_evaluate: '预测样本外评价',
}
const statuses: Record<string, string> = {
  queued: '排队',
  running: '运行中',
  succeeded: '已完成',
  unavailable: '依赖不可用',
  rejected: '输入被拒绝',
  failed: '失败',
  timed_out: '超时',
  cancelled: '已取消',
  unknown: '结果待核对',
}
let disposed = false,
  timer: ReturnType<typeof setInterval> | undefined
let offPush: (() => void) | undefined, offResync: (() => void) | undefined, offSubscription: (() => void) | undefined
const refresh = createPageRefresh({
  key: () => 'research',
  pushIntervalMs: 2000,
  read: async () => {
    loading.value = true
    try {
      const list = await listResearchJobs()
      if (disposed) return false
      jobs.value = list
      ready.value = true
      error.value = ''
      return true
    } catch (e) {
      if (!disposed) {
        jobs.value = []
        ready.value = false
        error.value = messageOf(e)
      }
      return false
    } finally {
      if (!disposed) loading.value = false
    }
  },
})
async function create() {
  if (writing.value) return
  writing.value = true
  error.value = ''
  try {
    const input: unknown = JSON.parse(draft.value.input_json),
      options: unknown = JSON.parse(draft.value.options_json)
    if (
      !input ||
      typeof input !== 'object' ||
      Array.isArray(input) ||
      !options ||
      typeof options !== 'object' ||
      Array.isArray(options)
    )
      throw new Error('输入和选项须为完整 JSON 对象')
    if (!draft.value.manifest_id.trim() || !Number.isFinite(Date.parse(draft.value.as_of)))
      throw new Error('请填写冻结数据集与明确的截止时刻')
    const job = await createResearchJob(draft.value)
    selectedId.value = job.id
    await refresh.refresh()
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    writing.value = false
  }
}
async function cancel(j: ResearchJob) {
  if (writing.value) return
  writing.value = true
  try {
    await cancelResearchJob(j.id)
    await refresh.refresh()
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    writing.value = false
  }
}
onMounted(() => {
  void refresh.refresh()
  offSubscription = subscribe('brain')
  offPush = onPush(async (msg) => {
    if (msg.metadata?.subject === 'sys.research' && !(await refresh.fromPush())) throw new Error('研究状态尚未恢复')
  })
  offResync = onResync(['brain'], async () => {
    if (!(await refresh.refresh())) throw new Error('研究状态尚未恢复')
  })
  timer = setInterval(() => {
    if (!document.hidden && jobs.value.some(active)) void refresh.refresh()
  }, 5000)
})
onBeforeUnmount(() => {
  disposed = true
  refresh.dispose()
  offPush?.()
  offResync?.()
  offSubscription?.()
  if (timer) clearInterval(timer)
})
</script>

<template>
  <section class="research" aria-label="隔离研究任务">
    <header>
      <div>
        <span class="eyebrow">RESEARCH PIPELINE</span>
        <h2>研究任务与产物</h2>
      </div>
      <NtButton size="sm" :loading="loading" @click="refresh.refresh()">核对任务</NtButton>
    </header>
    <p class="nt-note">冻结数据 → 有界研究 → 结果核验。研究产物可供审阅，策略晋级和交易授权另行核验。</p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="!ready" class="nt-note">研究任务尚未核验。</p>
    <p v-else-if="!jobs.length" class="nt-note">当前没有研究任务。</p>
    <div class="research-grid">
      <div class="job-list">
        <button
          v-for="j in jobs"
          :key="j.id"
          type="button"
          :class="{ selected: selectedId === j.id }"
          @click="selectedId = j.id"
        >
          <b>{{ kinds[j.kind] || j.kind }}</b
          ><span>{{ statuses[j.status] || j.status }}</span
          ><small>{{ clock(j.createdAt) }} · v{{ j.revision }}</small>
        </button>
      </div>
      <article v-if="selected" class="result">
        <h3>{{ kinds[selected.kind] || selected.kind }} · {{ statuses[selected.status] || selected.status }}</h3>
        <p v-if="selected.cancelRequested" class="warn">已请求取消，等待服务器停止与状态回执。</p>
        <p v-if="selected.reason" class="warn">{{ selected.reason }}</p>
        <div v-if="metrics" class="metrics">
          <NtStatCard label="费用后累计收益" :value="ratioPct(metrics.total_return)" /><NtStatCard
            label="最大回撤"
            :value="ratioPct(metrics.max_drawdown)"
          /><NtStatCard label="Sharpe" :value="num(metrics.sharpe)?.toFixed(2) ?? '—'" /><NtStatCard
            label="收益观测期数"
            :value="num(metrics.return_periods) == null ? '—' : String(metrics.return_periods)"
          />
        </div>
        <template v-if="weights.length"
          ><p class="nt-note">影子目标权重；实际股数、可卖数量和最新风控尚未转换。</p>
          <table>
            <thead>
              <tr>
                <th>标的</th>
                <th>目标权重</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="[symbol, weight] in weights" :key="symbol">
                <td>{{ symbol }}</td>
                <td>{{ ratioPct(weight) }}</td>
              </tr>
            </tbody>
          </table></template
        >
        <p v-if="output?.solver_status" class="nt-note">求解状态 {{ output.solver_status }}</p>
        <PortfolioPreview
          v-if="selected.kind === 'portfolio' && selected.status === 'succeeded'"
          :key="selected.id"
          :run-id="selected.id"
        />
        <details>
          <summary>数据版本与结果依据</summary>
          <p>冻结数据集 {{ selected.manifestId }}</p>
          <code>{{ selected.manifestHash }}</code>
          <p>输入哈希 {{ selected.inputHash }}</p>
          <p>产物哈希 {{ selected.artifactHash || '尚未返回' }}</p>
          <pre v-if="selected.result">{{ JSON.stringify(selected.result, null, 2) }}</pre>
        </details>
        <NtButton
          v-if="active(selected)"
          variant="danger"
          size="sm"
          :disabled="writing || selected.cancelRequested"
          @click="cancel(selected)"
          >请求取消</NtButton
        >
      </article>
    </div>
    <AccountNavExport :manifest-id="draft.manifest_id" @use-input="useNavInput" />
    <details class="new-job" :open="draftPrepared">
      <summary>从已审核输入建立研究任务</summary>
      <p class="nt-note">
        输入须绑定数据集哈希和截止时刻。正式账户净值需完整估值、费用与显式基准；不能用信号分数代替。研究 worker
        未启用时服务器会明确拒绝。
      </p>
      <form @submit.prevent="create">
        <label
          >研究能力<select v-model="draft.kind">
            <option v-for="(label, kind) in kinds" :key="kind" :value="kind">{{ label }}</option>
          </select></label
        ><label>冻结 manifest_id<input v-model="draft.manifest_id" required maxlength="80" /></label
        ><label
          >截止时刻（含时区）<input v-model="draft.as_of" placeholder="2026-10-16T15:00:00+08:00" required /></label
        ><label>墙钟预算（秒）<input v-model.number="draft.timeout_seconds" type="number" min="1" max="300" /></label
        ><label class="wide"
          >精确输入 JSON<textarea v-model="draft.input_json" rows="7" spellcheck="false" required /></label
        ><label class="wide">研究选项 JSON<textarea v-model="draft.options_json" rows="3" spellcheck="false" /></label
        ><NtButton type="submit" variant="primary" :loading="writing">建立隔离研究任务</NtButton>
      </form>
    </details>
  </section>
</template>

<style scoped>
.research {
  margin-top: 18px;
  padding: var(--nt-pad-panel);
  border: 1px solid var(--nt-accent-border);
  border-radius: var(--nt-radius-m);
  background: var(--nt-glass-sheen), var(--nt-glass-bg);
  box-shadow: var(--nt-edge-light), var(--nt-shadow-depth);
}
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.eyebrow {
  font: 10px var(--nt-font-mono);
  letter-spacing: 0.16em;
  color: var(--nt-accent);
}
h2 {
  font-size: 15px;
  margin: 4px 0;
}
.research-grid {
  display: grid;
  grid-template-columns: minmax(180px, 250px) 1fr;
  gap: 14px;
}
.job-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.job-list > button {
  display: grid;
  text-align: left;
  gap: 5px;
  padding: 10px;
  background: var(--nt-bg-1);
  color: var(--nt-text-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}
.job-list > button.selected {
  border-color: var(--nt-accent);
}
.job-list span,
.job-list small {
  font-size: 11px;
  color: var(--nt-text-2);
}
.result {
  min-width: 0;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  padding: 12px;
  border-radius: var(--nt-radius-s);
}
h3 {
  font-size: 13px;
}
.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 8px;
}
details {
  margin-top: 12px;
  font-size: 12px;
}
summary {
  cursor: pointer;
  color: var(--nt-accent);
}
code,
pre {
  font: 11px var(--nt-font-mono);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
pre {
  max-height: 350px;
  overflow: auto;
}
form {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  margin-top: 12px;
}
label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 11px;
  color: var(--nt-text-2);
}
input,
select,
textarea {
  padding: 8px;
  background: var(--nt-bg-1);
  color: var(--nt-text-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}
textarea {
  font: 11px var(--nt-font-mono);
  resize: vertical;
}
.wide {
  grid-column: 1/-1;
}
.warn {
  color: var(--nt-warn);
}
table {
  width: 100%;
  border-collapse: collapse;
  text-align: left;
}
td,
th {
  padding: 7px;
  border-bottom: 1px solid var(--nt-border-1);
  font-size: 12px;
}
@media (max-width: 700px) {
  .research-grid {
    grid-template-columns: 1fr;
  }
  form {
    grid-template-columns: 1fr;
  }
}
</style>
