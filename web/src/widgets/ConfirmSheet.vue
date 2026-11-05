<script setup lang="ts">
/**
 * 实盘确认弹层（原 component/ConfirmDialog.vue）。
 * 一个 .mask/.sheet 实现统一交给 NtModal + useOverlay。
 * 红涨绿跌：买入红、卖出绿。
 */
import { computed } from 'vue'
import { acceptConfirm, confirmState, dismissConfirm } from '@/stores/confirm'
import { useAuthStore } from '@/stores/auth'
import { amount, pnlClass, pnlSign, price } from '@/utils/format'
import SymbolName from '@/display/SymbolName.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtModal from '@/ui/NtModal.vue'

const auth = useAuthStore()
const locked = computed(() => auth.isViewer())
const order = computed(() => (confirmState.order || {}) as Record<string, unknown>)

/** FE-T1：确认弹层带试算摘要（金额/费用/现金影响/缩量放行）——确认前先看到代价，不再只凭代码/价/量盲签。 */
function numOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}
const previewAmount = computed(() => numOrNull(order.value.previewAmount))
const previewFee = computed(() => numOrNull(order.value.previewFee))
const previewCashDelta = computed(() => numOrNull(order.value.previewCashDelta))
const previewAdjusted = computed(() => order.value.previewAdjusted === true)
const previewVolume = computed(() => numOrNull(order.value.previewVolume))
const hasPreview = computed(() => previewAmount.value != null || previewFee.value != null || previewCashDelta.value != null)
const orderIdText = computed(() => String(order.value.clientOrderId ?? order.value.client_order_id ?? order.value.id ?? ''))
/** 现金影响带符号（买入为负、卖出为正）；金额/费用无符号口径走 amount。 */
function signedMoney(v: number | null): string {
  return v == null ? '—' : `${pnlSign(v)}${amount(v)}`
}

const open = computed({
  get: () => confirmState.open,
  set: (value: boolean) => {
    confirmState.open = value
  },
})

function sideText(side: unknown): string {
  if (side === 'buy') return '买入'
  if (side === 'sell') return '卖出'
  return String(side || '—')
}

function sideClass(side: unknown): string {
  if (side === 'buy') return 'up'
  if (side === 'sell') return 'down'
  return ''
}
</script>

<template>
  <NtModal v-model:open="open" :closable="false" width="440px">
    <p class="tag">实盘确认 · LIVE</p>
    <h2 class="headline">{{ confirmState.seconds }} 秒内未确认则作废</h2>
    <dl class="fields">
      <div>
        <dt>标的</dt>
        <dd><SymbolName v-if="order.symbol" :code="String(order.symbol)" mode="inline" :clickable="false" /><template v-else>—</template></dd>
      </div>
      <div>
        <dt>方向</dt>
        <dd :class="sideClass(order.side)">{{ sideText(order.side) }}</dd>
      </div>
      <div>
        <dt>价格</dt>
        <dd>{{ price(order.price) }}</dd>
      </div>
      <div>
        <dt>数量</dt>
        <dd>{{ order.volume ?? order.qty ?? '—' }}</dd>
      </div>
      <div>
        <dt>账户</dt>
        <dd>LIVE</dd>
      </div>
    </dl>
    <!-- FE-T1：确认弹层含试算摘要——金额/费用/现金影响（缩量放行时显式标出缩量后股数）。 -->
    <section v-if="hasPreview" class="sum">
      <p class="sum__tag">落单前试算摘要</p>
      <dl class="fields">
        <div>
          <dt>金额</dt>
          <dd>{{ previewAmount == null ? '—' : amount(previewAmount) }}</dd>
        </div>
        <div>
          <dt>费用</dt>
          <dd>{{ previewFee == null ? '—' : amount(previewFee) }}</dd>
        </div>
        <div>
          <dt>现金影响</dt>
          <dd :class="pnlClass(previewCashDelta)">{{ signedMoney(previewCashDelta) }}</dd>
        </div>
        <div v-if="previewAdjusted && previewVolume != null">
          <dt>缩量放行</dt>
          <dd class="warn">{{ previewVolume }} 股</dd>
        </div>
      </dl>
    </section>
    <p v-if="orderIdText" class="idem" :title="orderIdText">幂等键 {{ orderIdText.slice(0, 18) }}… —— 同一编号重复提交不会重复下单</p>
    <p v-if="locked" class="nt-err">当前是查看角色，不能确认实盘委托。</p>
    <NtErrorGuide :error="confirmState.error" />
    <template #footer>
      <NtButton :disabled="confirmState.busy" @click="dismissConfirm">放弃</NtButton>
      <NtButton variant="success" :disabled="locked || confirmState.busy" @click="acceptConfirm">
        {{ confirmState.busy ? '提交中…' : '确认' }}
      </NtButton>
    </template>
  </NtModal>
</template>

<style scoped>
.tag {
  margin: 0;
  color: var(--nt-warn);
  font-size: 11.5px;
  font-weight: 600;
}

.headline {
  margin: 0;
  font-size: 17px;
  font-weight: 600;
  color: var(--nt-text-1);
  font-variant-numeric: tabular-nums;
}

.fields {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 16px;
  margin: 0;
}

dt {
  color: var(--nt-text-3);
  font-size: 11.5px;
}

dd {
  margin: 2px 0 0;
  font-size: 15px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

dd.up {
  color: var(--nt-up);
}

dd.down {
  color: var(--nt-down);
}

/* FE-T1：试算摘要区——与上方字段同栅格，前缀细线分节；缩量放行走 warn 语义色。 */
.sum {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid var(--nt-border-1);
}

.sum__tag {
  margin: 0 0 8px;
  color: var(--nt-text-3);
  font-size: 11.5px;
  font-weight: 600;
}

.sum dd.warn {
  color: var(--nt-warn);
}

.idem {
  margin: 10px 0 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--nt-text-3);
}
</style>
