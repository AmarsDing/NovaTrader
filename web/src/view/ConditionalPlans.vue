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
import { getOpsReadiness, type ReadinessGate } from '@/api/ops'
import { onPush, onResync } from '@/ws/hub'
import { createPageRefresh } from './page-refresh'
import { amount, clock, messageOf, price } from '@/utils/format'
import { serverNow } from '@/utils/server-clock'
import { openDeeplink } from '@/utils/deeplink'
import { blockingGaps, gateItems, PLAN_AUTH_GATES } from '@/utils/preflight'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtModal from '@/ui/NtModal.vue'
import NtPreflightChecklist, { type PreflightItem } from '@/display/NtPreflightChecklist.vue'
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
/** 计划有效期判定走 P2-10 服务端钟（未校准回退本地）：过期判定与后端同一时源。 */
const now = ref(serverNow())
const focused = computed(() => rows.value.filter((r) => !props.signalId || String(r.plan.signal_id) === props.signalId))
const unknown = computed(() => intents.value.filter((i) => ['claimed', 'unknown'].includes(i.state)))
const selectedSignal = computed(() => Number(props.signalId))
/* 审计操作人单一出处（displayName || name，与 KillSwitchBar/trade-bar 同口径）：授权/执行/撤权全链共用。 */
const operator = computed(() => auth.operatorName)
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
/* ---------- W3c 启用模态：计划授权（策略启用）清单式前置检查（R-UI-30/36） ---------- */
const authOpen = ref(false),
  authBusy = ref(false),
  authError = ref('')
const authTarget = ref<{ plan: PlanRecord; title: string; mode: 'sim_auto' | 'sim_confirm' } | null>(null)
const gates = ref<ReadinessGate[]>([]),
  gatesBusy = ref(false)
let authFetchSeq = 0
function openAuth(plan: PlanRecord, mode: 'sim_auto' | 'sim_confirm') {
  if (!canManage.value || !operator.value) return
  authError.value = ''
  authTarget.value = { plan, title: mode === 'sim_auto' ? '授权 SIM 条件自动' : '授权逐次确认', mode }
  authOpen.value = true
  /* 不带上轮快照进场：取到前必堵行全按「未证实」落锁；就绪口不可达同锁（fail-closed，没取到≠0）。 */
  gates.value = []
  gatesBusy.value = true
  const seq = ++authFetchSeq
  void getOpsReadiness()
    .then((r) => {
      /* 代际+卸载双守门：重开/卸载后晚到的旧响应不得盖新（R-UI-35 慢响应顾序纪律）。 */
      if (seq === authFetchSeq && !disposed) gates.value = r.gates
    })
    .catch(() => {
      /* 保持未证实：不静默放行 */
    })
    .finally(() => {
      if (seq === authFetchSeq) gatesBusy.value = false
    })
}
const authItems = computed<PreflightItem[]>(() => gateItems(gates.value, PLAN_AUTH_GATES))
const authBlockText = computed(() => {
  if (!canManage.value) return '仅 owner 可授权计划'
  if (!operator.value) return '操作人身份缺失，不能留痕授权'
  if (gatesBusy.value) return '就绪口核验中，稍候'
  const g = blockingGaps(authItems.value)
  if (g.length) return g.map((i) => `${i.label}${i.why ? `：${i.why}` : ''}`).join('；')
  return ''
})
async function confirmAuth() {
  const t = authTarget.value
  if (!t || authBlockText.value) return
  authBusy.value = true
  authError.value = ''
  try {
    await authorizePlan(t.plan, t.mode, operator.value)
    authOpen.value = false
    await refresh.refresh()
  } catch (e) {
    authError.value = messageOf(e)
  } finally {
    authBusy.value = false
  }
}
/** 清单「去处理」：先收模态再跳修复位——全屏 scrim 不关会遮住刚打开的目标面板（跳转即白点）。 */
function onGuide(link: string) {
  authOpen.value = false
  openDeeplink(link)
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
    now.value = serverNow()
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
    <NtErrorGuide :error="error" />
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
          <!-- 交易时点缺失必须大声说，不能悄声空白（有效期直接关系到还作不作数） -->
          <dd>{{ clock(r.plan.valid_until) || '有效期未返回' }}</dd>
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
          <NtButton size="sm" :disabled="busy || !canManage || !operator" @click="openAuth(r, 'sim_confirm')">逐次确认</NtButton>
          <NtButton size="sm" :disabled="busy || !canManage || !operator" @click="openAuth(r, 'sim_auto')"
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
    <!-- W3c 启用模态（策略启用）：「必需」项未就位禁提交并逐条说明；提示项断了会空跑但不伤现状仓位。 -->
    <NtModal v-model:open="authOpen" :title="authTarget?.title || '计划授权'" width="520px">
      <p class="note">
        「必需」项未就位不能提交（fail-closed）；数据源/模型为提示项，断了会空跑但不伤现状仓位，可授权后补处理。
      </p>
      <NtPreflightChecklist :items="authItems" title="计划授权前置检查" @navigate="onGuide" />
      <NtErrorGuide :error="authError" />
      <template #footer>
        <NtButton variant="quiet" size="sm" @click="authOpen = false">先不授权</NtButton>
        <NtButton
          size="sm"
          variant="primary"
          :loading="authBusy"
          :disabled="!!authBlockText"
          :disabled-reason="authBlockText"
          @click="void confirmAuth()"
          >确认授权</NtButton
        >
      </template>
    </NtModal>
  </section>
</template>

<style scoped>
/* §6 玻璃只许壳层：本卡装着执行意图明细（数据背板），去 glass-sheen/blur、去 accent 描边，
 * 走数据背板不透明档（0.94+ 无模糊）。 */
.plans {
  padding: var(--nt-pad-panel);
  margin-block: 12px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  background: var(--nt-data-bg, var(--nt-bg-1));
  box-shadow: var(--nt-shadow-depth);
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
  font: var(--nt-fs-micro) var(--nt-font-mono); /* 收编自 10px 裸值（§5.3 micro 档 10.5，抵近档） */
  letter-spacing: 0.16em;
  color: var(--nt-accent);
}
h3 {
  margin: 4px 0;
  font-size: var(--nt-fs-title); /* 收编自 14px 裸值（§5.3 title 档 15，归并入档） */
}
.note,
.meta {
  font-size: var(--nt-fs-caption); /* 收编自 11px 裸值（§5.3 caption 档 11.5，归并入档） */
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
  color: var(--nt-warn); /* 删 !important：同色冲突交还级联，局部语义色不该压制全站 */
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
