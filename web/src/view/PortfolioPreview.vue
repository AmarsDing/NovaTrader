<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { previewPortfolio, type AllocationPreview } from '@/api/allocation'
import { amount, clock, messageOf, price, ratioPct } from '@/utils/format'
import NtButton from '@/ui/NtButton.vue'
import SymbolName from '@/display/SymbolName.vue'
const props = defineProps<{ runId: string }>()
const preview = ref<AllocationPreview | null>(null),
  busy = ref(false),
  error = ref('')
let generation = 0,
  disposed = false
watch(
  () => props.runId,
  () => {
    generation++
    preview.value = null
    error.value = ''
    busy.value = false
  },
  { flush: 'sync' },
)
async function load() {
  if (busy.value) return
  const run = ++generation,
    id = props.runId
  busy.value = true
  error.value = ''
  preview.value = null
  try {
    const r = await previewPortfolio(id)
    if (!disposed && run === generation) preview.value = r
  } catch (e) {
    if (!disposed && run === generation) error.value = messageOf(e)
  } finally {
    if (!disposed && run === generation) busy.value = false
  }
}
onBeforeUnmount(() => {
  disposed = true
  generation++
})
</script>
<template>
  <section class="portfolio-preview" aria-label="组合股数影子预览">
    <header>
      <h4>影子权重 → 股数与现金</h4>
      <NtButton size="sm" :loading="busy" @click="load">按最新 SIM 账本核算</NtButton>
    </header>
    <p class="nt-note">
      读取原研究任务与当前行情，核算整手、T+1、费用、冻结和集中度。这里只生成待审建议，实际执行须有独立信号、计划授权与最终风控。
    </p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <template v-if="preview"
      ><p class="conclusion">
        {{
          preview.allocation_ready ? '离散约束已核验，仍需审阅和最终风控' : '当前组合存在阻断或未知，暂不能按目标调整'
        }}
      </p>
      <p v-if="preview.research.fixture_type" class="warn">
        研究输入标记 {{ preview.research.fixture_type }}；此结果不作为真实市场推荐。
      </p>
      <div class="metrics">
        <div>
          <span>按当前报价计算的权益</span><b>{{ amount(preview.summary.fresh_equity) }}</b>
        </div>
        <div>
          <span>可用 / 冻结现金</span
          ><b>{{ amount(preview.summary.available_cash) }} / {{ amount(preview.summary.frozen_cash) }}</b>
        </div>
        <div>
          <span>规则保留现金 / 预估费用</span
          ><b>{{ amount(preview.summary.required_cash) }} / {{ amount(preview.summary.estimated_fees) }}</b>
        </div>
        <div>
          <span>买入资金缺口</span><b>{{ amount(preview.summary.buy_funding_gap) }}</b>
        </div>
      </div>
      <p
        v-for="c in preview.summary.constraints"
        :key="c.code + c.message"
        class="constraint"
        :class="{ warn: c.status !== 'passed' }"
      >
        {{ c.status === 'passed' ? '已核验' : c.status === 'unknown' ? '待核验' : '阻断' }} · {{ c.message }}
      </p>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>证券 / 目标权重</th>
              <th>当前 / 目标股数</th>
              <th>当前可卖 / T+1 / 冻结</th>
              <th>买 / 卖差额（待审）</th>
              <th>参考价 / 源时间</th>
              <th>状态与原因</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in preview.rows" :key="r.symbol">
              <td>
                <!-- P-08 预览表代码位：SymbolName(inline) 名称+代码 -->
                <SymbolName :code="r.symbol" /><small>{{ ratioPct(r.target_weight) }}</small>
              </td>
              <td>{{ r.current_quantity }} / {{ r.target_quantity ?? '—' }}</td>
              <td>{{ r.available_to_sell }} / {{ r.t1_locked }} / {{ r.frozen_sell }}</td>
              <td>{{ r.buy_quantity }} / {{ r.sell_quantity }}</td>
              <td>
                {{ price(r.reference_price) }}<small>{{ clock(r.source_time) || '源时间未知' }}</small>
              </td>
              <td>
                <b>{{ r.status === 'ready' ? '基础约束已核' : r.status === 'unknown' ? '待核验' : '阻断' }}</b
                ><small v-for="reason in r.reasons" :key="reason">{{ reason }}</small>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="nt-note">
        股数取整后的权重偏差 {{ ratioPct(preview.summary.residual_weight) }} · 换手
        {{ ratioPct(preview.summary.turnover) }}。未成交卖出款不会提前作为买入资金。
      </p>
      <details>
        <summary>当前事实与执行边界</summary>
        <p>
          核算 {{ clock(preview.formed_at) }} · SIM 快照 {{ preview.account_generation }} · 配置 v{{
            preview.config_revision
          }}
        </p>
        <p v-for="u in preview.unknowns" :key="u">{{ u }}</p>
        <p>后续必需：{{ preview.execution_requirements.join('、') }}。</p>
        <code>{{ preview.research.result_hash }}</code>
      </details>
    </template>
  </section>
</template>
<style scoped>
.portfolio-preview {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--nt-border-1);
}
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
h4 {
  font-size: 12px;
  margin: 3px 0;
}
.conclusion {
  color: var(--nt-accent);
  font-weight: 600;
  font-size: 12px;
}
.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 10px;
  margin-block: 12px;
}
.metrics span {
  display: block;
  font-size: 10px;
  color: var(--nt-text-2);
}
.metrics b {
  display: block;
  font: 12px var(--nt-font-mono);
  margin-top: 5px;
}
.constraint,
details {
  font-size: 11px;
  line-height: 1.7;
}
.warn {
  color: var(--nt-warn);
}
.table-wrap {
  overflow-x: auto;
}
table {
  width: 100%;
  white-space: nowrap;
  border-collapse: collapse;
  font: 11px var(--nt-font-mono);
}
th,
td {
  text-align: left;
  padding: 8px;
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
summary {
  cursor: pointer;
}
code {
  overflow-wrap: anywhere;
}
</style>
