<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  authorizePlan,
  createPlan,
  executePlan,
  listIntents,
  listPlans,
  type ExecutionIntent,
  type PlanRecord,
} from '@/api/automation'
import { useAccountStore } from '@/stores/account'
import { useAuthStore } from '@/stores/auth'
import { onPush, onResync } from '@/ws/hub'
import { createPageRefresh } from './page-refresh'
import { amount, clock, messageOf, price } from '@/utils/format'
import NtButton from '@/ui/NtButton.vue'

import SymbolName from '@/display/SymbolName.vue'
const props = defineProps<{ signalId?: string }>()
const account = useAccountStore(),
  auth = useAuthStore()
const rows = ref<PlanRecord[]>([]),
  intents = ref<ExecutionIntent[]>([])
const ready = ref(false),
  loading = ref(false),
  busy = ref(false),
  error = ref('')
const now = ref(Date.now())
const focused = computed(() => rows.value.filter((r) => !props.signalId || String(r.plan.signal_id) === props.signalId))
const unknown = computed(() => intents.value.filter((i) => ['claimed', 'unknown'].includes(i.state)))
const selectedSignal = computed(() => Number(props.signalId))
const operator = computed(() => auth.user?.name ?? '')
const canManage = computed(() => auth.user?.role === 'owner')
let generation = 0,
  disposed = false
const refresh = createPageRefresh({
  key: () => `${account.book}:${generation}`,
  pushIntervalMs: 2000,
  read: async (key) => {
    loading.value = true
    try {
      const [p, i] = await Promise.all([listPlans(account.book), listIntents(account.book)])
      if (disposed || key !== `${account.book}:${generation}`) return false
      rows.value = p
      intents.value = i
      ready.value = true
      error.value = ''
      return true
    } catch (e) {
      if (!disposed && key === `${account.book}:${generation}`) {
        ready.value = false
        rows.value = []
        intents.value = []
        error.value = messageOf(e)
      }
      return false
    } finally {
      if (!disposed && key === `${account.book}:${generation}`) loading.value = false
    }
  },
})
async function mutate(action: () => Promise<unknown>) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await action()
    await refresh.refresh()
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    busy.value = false
  }
}
function expired(r: PlanRecord) {
  return Date.parse(r.plan.valid_until) <= now.value
}
function label(state: string) {
  return (
    (
      {
        draft: '待授权',
        authorized: '等待条件',
        executed: '已形成原订单',
        revoked: '已撤权',
        superseded: '旧版本',
        expired: '已过期',
        blocked: '已阻断',
      } as Record<string, string>
    )[state] ?? state
  )
}
function freeze() {
  return mutate(() => createPlan(selectedSignal.value, account.book))
}
let offPush: (() => void) | undefined,
  offResync: (() => void) | undefined,
  timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void refresh.refresh()
  offPush = onPush(async (msg) => {
    if (['position', 'signal', 'alert'].includes(msg.topic)) {
      if (!(await refresh.fromPush())) throw new Error('条件计划尚未恢复')
    }
  })
  offResync = onResync(['position'], async () => {
    if (!(await refresh.refresh())) throw new Error('执行意图尚未恢复')
  })
  timer = setInterval(() => {
    now.value = Date.now()
    if (!document.hidden) void refresh.refresh()
  }, 5000)
})
watch(
  () => account.book,
  () => {
    generation++
    ready.value = false
    rows.value = []
    intents.value = []
    void refresh.refresh()
  },
  { flush: 'sync' },
)
onBeforeUnmount(() => {
  disposed = true
  generation++
  refresh.dispose()
  offPush?.()
  offResync?.()
  if (timer) clearInterval(timer)
})
</script>

<template>
  <section class="plans" aria-label="持久条件计划">
    <header>
      <div>
        <span class="eyebrow">CONDITIONAL EXECUTION</span>
        <h3>条件计划 · {{ account.book }}</h3>
      </div>
      <NtButton size="sm" :loading="loading" @click="refresh.refresh()">核对</NtButton>
      <NtButton
        v-if="Number.isInteger(selectedSignal) && selectedSignal > 0"
        size="sm"
        :disabled="busy || !canManage || account.book !== 'SIM'"
        @click="freeze"
        >冻结当前事实</NtButton
      >
    </header>
    <p class="note">先核对何时行动与何时失效，再选择 SIM 授权。实际股数由规则预算和最新风控决定。</p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="!ready" class="note">计划与原订单状态尚未核验。</p>
    <p v-else-if="!focused.length" class="note">当前没有持久计划；可从有效信号冻结事实。</p>
    <aside v-if="unknown.length" class="unresolved">
      {{ unknown.length }} 个原意图待核对，保留原订单身份；不会按重试按钮生成新单。
    </aside>
    <article v-for="r in focused.slice(0, 12)" :key="r.plan.plan_id + ':' + r.plan.revision" class="plan">
      <div class="plan-title">
        <b
          ><!-- P-08 计划行证券位：SymbolName(inline) 名称+代码；f10 试点（§6.5）hover 速览/点击进抽屉 -->
          <SymbolName :code="r.plan.symbol" f10 /> · v{{ r.plan.revision }}</b
        ><span :class="{ warn: expired(r) }">{{
          expired(r) && r.state === 'authorized' ? '已过期' : label(r.state)
        }}</span>
      </div>
      <p class="summary">{{ r.plan.summary }}</p>
      <dl>
        <div>
          <dt>规则入场区间</dt>
          <dd>{{ price(r.plan.entry_low) }}–{{ price(r.plan.entry_high) }}</dd>
        </div>
        <div>
          <dt>止损 / 止盈</dt>
          <dd>{{ price(r.plan.stop_loss) }} / {{ price(r.plan.take_profit) }}</dd>
        </div>
        <div>
          <dt>金额预算上限</dt>
          <dd>{{ amount(r.plan.max_notional) }}</dd>
        </div>
        <div>
          <dt>有效至</dt>
          <dd>{{ clock(r.plan.valid_until) }}</dd>
        </div>
      </dl>
      <p class="meta">
        配置 v{{ r.snapshot.config_revision }} · 策略 v{{ r.snapshot.strategy_revision }} · 账户快照
        {{ r.snapshot.account_generation }} · {{ r.mode === 'sim_auto' ? '服务器自动等待条件' : '需要当前确认' }}
      </p>
      <details>
        <summary>证据、反证与失效条件</summary>
        <p>{{ r.snapshot.reason }}</p>
        <p v-for="line in [...r.plan.invalidation, ...r.snapshot.counter_evidence, ...r.snapshot.unknowns]" :key="line">
          {{ line }}
        </p>
        <p v-for="line in r.snapshot.evidence" :key="line">证据 {{ line }}</p>
        <code>{{ r.plan.plan_id }}</code>
      </details>
      <div class="actions">
        <template v-if="r.plan.account_type === 'SIM' && !expired(r) && ['draft', 'revoked'].includes(r.state)">
          <NtButton
            size="sm"
            :disabled="busy || !canManage || !operator"
            @click="mutate(() => authorizePlan(r, 'sim_confirm', operator))"
            >逐次确认</NtButton
          >
          <NtButton
            size="sm"
            :disabled="busy || !canManage || !operator"
            @click="mutate(() => authorizePlan(r, 'sim_auto', operator))"
            >授权 SIM 条件自动</NtButton
          >
        </template>
        <template v-if="r.state === 'authorized'">
          <NtButton
            v-if="r.mode === 'sim_confirm'"
            size="sm"
            :disabled="busy || !canManage || expired(r) || !operator"
            @click="mutate(() => executePlan(r, operator))"
            >核条件并提交 SIM</NtButton
          >
          <NtButton
            size="sm"
            variant="danger"
            :disabled="busy || !canManage || !operator"
            @click="mutate(() => authorizePlan(r, 'revoke', operator))"
            >撤销授权</NtButton
          >
        </template>
        <NtButton
          v-if="!['executed', 'superseded'].includes(r.state)"
          size="sm"
          :disabled="busy || !canManage"
          @click="mutate(() => createPlan(r.plan.signal_id, account.book, r))"
          >重新核验并生成新版</NtButton
        >
      </div>
      <p v-if="r.reason" class="note">{{ r.reason }}</p>
    </article>
    <details v-if="ready && intents.length">
      <summary>原订单与执行意图 · {{ intents.length }}</summary>
      <div v-for="i in intents.slice(0, 20)" :key="i.client_order_id" class="intent">
        <code>{{ i.client_order_id }}</code
        ><span>{{ i.state }}</span>
        <p>{{ i.reason }}</p>
      </div>
    </details>
  </section>
</template>

<style scoped>
.plans {
  padding: var(--nt-pad-panel);
  margin-block: 12px;
  border: 1px solid var(--nt-accent-border);
  border-radius: var(--nt-radius-m);
  background: var(--nt-glass-sheen), var(--nt-glass-bg);
  box-shadow: var(--nt-edge-light), var(--nt-shadow-depth);
  backdrop-filter: var(--nt-glass-filter);
}
header,
.plan-title,
.actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
header > div {
  flex: 1;
}
.eyebrow {
  font: 10px var(--nt-font-mono);
  letter-spacing: 0.16em;
  color: var(--nt-accent);
}
h3 {
  margin: 4px 0;
  font-size: 14px;
}
.note,
.meta {
  font-size: 11px;
  color: var(--nt-text-2);
}
.plan {
  margin-top: 10px;
  padding: 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}
.plan-title {
  justify-content: space-between;
}
.plan-title span {
  color: var(--nt-accent);
  font-size: 11px;
}
.summary {
  font-size: 13px;
  font-weight: 600;
  color: var(--nt-text-1);
}
dl {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 10px;
}
dt {
  font-size: 10px;
  color: var(--nt-text-2);
}
dd {
  margin: 3px 0;
  font: 13px var(--nt-font-mono);
  color: var(--nt-text-1);
}
details {
  margin: 8px 0;
  font-size: 11px;
  color: var(--nt-text-2);
}
summary {
  cursor: pointer;
  color: var(--nt-text-1);
}
.actions {
  margin-top: 10px;
}
.unresolved,
.warn {
  color: var(--nt-warn) !important;
}
.unresolved {
  padding: 8px;
  border: 1px solid var(--nt-warn);
  font-size: 12px;
}
code {
  overflow-wrap: anywhere;
}
.intent {
  padding: 8px 0;
  border-bottom: 1px solid var(--nt-border-1);
}
.intent span {
  margin-left: 10px;
}
</style>
