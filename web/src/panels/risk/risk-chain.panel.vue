<script setup lang="ts">
/**
 * 风控链面板（M08）：R01–R15 规则链可视化 + 手动预检。
 * 对一笔假设订单跑 /api/risk/check，逐条显示规则通过/拦截情况。
 */
import { computed, onMounted, ref } from 'vue'
import { checkOrder, type RuleHit } from '@/api/risk'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import { clock, messageOf } from '@/utils/format'

const auth = useAuthStore()
const risk = useRiskStore()

const book = ref('SIM')
const symbol = ref('')
const side = ref('BUY')
const price = ref('')
const qty = ref('')
const checking = ref(false)
const error = ref('')
const result = ref<{
  pass: boolean
  rule: string
  detail: string
  hits: RuleHit[]
  traceId: string
  at: string
} | null>(null)

/** 当前风控阈值（R 系列参数），「风控夹了多少」直接读这里。 */
const paramRows = computed(() =>
  Object.entries(risk.params)
    .map(([key, value]) => ({ key, value: String(value ?? '—') }))
    .sort((a, b) => a.key.localeCompare(b.key)),
)

const canCheck = computed(() => /^\d{6}\.(SZ|SH|BJ)$/i.test(symbol.value.trim()) && Number(price.value) > 0 && Number(qty.value) >= 100)

async function runCheck() {
  if (!canCheck.value) return
  checking.value = true
  error.value = ''
  try {
    const reply = await checkOrder({
      client_order_id: `preview-${Date.now()}`,
      account_type: book.value,
      symbol: symbol.value.trim().toUpperCase(),
      side: side.value.toLowerCase(),
      price: Number(price.value),
      volume: Number(qty.value),
      source: 'manual',
      operator: auth.operatorName || 'admin',
    })
    result.value = { ...reply, at: new Date().toISOString() }
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    checking.value = false
  }
}

const hits = computed(() => result.value?.hits ?? [])
const passedCount = computed(() => hits.value.filter((h) => h.pass).length)

onMounted(() => {
  void risk.refresh()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">风控链 R01–R15</h1>
        <p class="nt-note">
          当前模式 <b>{{ risk.mode || '—' }}</b>
          <!-- §10-19 册消费研判：Kill Switch 为 L4 风险（§8.1），chapter 须 danger；
               册 alert.triggered「已触发」tone=warn 会降语义等级，audit.* 词义不符，
               故保持「Kill Switch 已触发」原文案待入册评审；熔断章带动态器名，册内无对应键，同保原文案。 -->
          <NtBadge v-if="risk.killActive" tone="danger" dot>Kill Switch 已触发</NtBadge>
          <NtBadge v-if="risk.marketBreaker" tone="warn">行情熔断 {{ risk.marketBreaker }}</NtBadge>
          <span v-if="risk.buysToday != null" class="meta">今日已买入 {{ risk.buysToday }}/5</span>
        </p>
      </div>
    </header>

    <section v-if="paramRows.length" class="nt-panel">
      <h2 class="nt-panel__title">当前阈值</h2>
      <ul class="params">
        <li v-for="row in paramRows" :key="row.key">
          <span class="params__k">{{ row.key }}</span>
          <b>{{ row.value }}</b>
        </li>
      </ul>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">订单预检</h2>
      <div class="form">
        <NtSelect
          v-model="book"
          :options="[
            { value: 'SIM', label: 'SIM 模拟' },
            { value: 'LIVE', label: 'LIVE 实盘' },
          ]"
        />
        <NtInput v-model="symbol" placeholder="000001.SZ" class="form__symbol" />
        <NtSelect
          v-model="side"
          :options="[
            { value: 'BUY', label: '买入' },
            { value: 'SELL', label: '卖出' },
          ]"
        />
        <NtInput v-model="price" placeholder="价格" type="number" class="form__num" />
        <NtInput v-model="qty" placeholder="数量" type="number" class="form__num" />
        <NtButton size="sm" variant="primary" :loading="checking" :disabled="!canCheck" @click="runCheck">预检</NtButton>
      </div>
      <p v-if="error" class="nt-err">{{ error }}</p>
    </section>

    <section v-if="result" class="nt-panel">
      <h2 class="nt-panel__title">
        结果
        <!-- §10-19 册消费研判：预检「通过/拦截」是规则放行语义，册 audit.success/failure（成功/失败）
             词义不等价，保持原文案待入册评审。 -->
        <NtBadge :tone="result.pass ? 'success' : 'danger'">{{ result.pass ? '通过' : '拦截' }}</NtBadge>
      </h2>
      <p class="nt-note">
        {{ passedCount }}/{{ hits.length }} 条规则通过 · {{ clock(result.at) }}
        <span v-if="result.rule"> · 拦截规则 {{ result.rule }}</span>
      </p>
      <p v-if="result.detail" class="detail">{{ result.detail }}</p>
      <ol class="chain">
        <li v-for="hit in hits" :key="hit.code" class="chain__step" :class="{ blocked: !hit.pass }">
          <span class="chain__code">{{ hit.code }}</span>
          <span class="chain__name">{{ hit.name }}</span>
          <span class="chain__verdict">{{ hit.pass ? '✓' : '✗' }}</span>
          <span v-if="hit.detail" class="chain__detail">{{ hit.detail }}</span>
        </li>
      </ol>
      <p v-if="!hits.length" class="nt-note">后端未返回逐条明细（pass={{ result.pass }}）。</p>
    </section>
  </div>
</template>

<style scoped>
.meta {
  margin-left: 8px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-2);
}

.params {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 6px;
}

.params li {
  display: flex;
  gap: 8px;
  align-items: baseline;
  font-size: 12px;
  padding: 4px 8px;
  background: var(--nt-bg-2);
  border-radius: var(--nt-radius-s);
}

.params__k {
  color: var(--nt-text-3);
  font-family: var(--nt-font-mono);
}

.params b {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  color: var(--nt-text-1);
}

.form {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
}

.form__symbol {
  width: 150px;
}

.form__num {
  width: 110px;
}

.detail {
  margin: 0 0 8px;
  font-size: 12.5px;
  color: var(--nt-text-1);
}

.chain {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 4px;
}

.chain__step {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 6px 10px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-2);
  font-size: 12.5px;
}

.chain__step.blocked {
  border-color: color-mix(in srgb, var(--nt-danger) 50%, transparent);
  background: color-mix(in srgb, var(--nt-danger) 6%, transparent);
}

.chain__code {
  font-family: var(--nt-font-mono);
  color: var(--nt-text-3);
  min-width: 3em;
}

.chain__name {
  color: var(--nt-text-1);
  flex: 1;
}

.chain__verdict {
  font-family: var(--nt-font-mono);
  color: var(--nt-success);
}

.chain__step.blocked .chain__verdict {
  color: var(--nt-danger);
}

.chain__detail {
  color: var(--nt-text-3);
  font-size: 11.5px;
}
</style>
