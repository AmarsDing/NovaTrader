<script setup lang="ts">
/**
 * 实盘确认弹层（原 component/ConfirmDialog.vue）。
 * 一个 .mask/.sheet 实现统一交给 NtModal + useOverlay。
 * 红涨绿跌：买入红、卖出绿。
 */
import { computed } from 'vue'
import { acceptConfirm, confirmState, dismissConfirm } from '@/store/confirm'
import { useAuthStore } from '@/stores/auth'
import { price } from '@/utils/format'
import NtButton from '@/ui/NtButton.vue'
import NtModal from '@/ui/NtModal.vue'

const auth = useAuthStore()
const locked = computed(() => auth.isViewer())
const order = computed(() => (confirmState.order || {}) as Record<string, unknown>)

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
      <div><dt>标的</dt><dd>{{ order.symbol || '—' }}</dd></div>
      <div><dt>方向</dt><dd :class="sideClass(order.side)">{{ sideText(order.side) }}</dd></div>
      <div><dt>价格</dt><dd>{{ price(order.price) }}</dd></div>
      <div><dt>数量</dt><dd>{{ order.volume ?? order.qty ?? '—' }}</dd></div>
      <div><dt>账户</dt><dd>LIVE</dd></div>
    </dl>
    <p v-if="locked" class="nt-err">当前是查看角色，不能确认实盘委托。</p>
    <p v-if="confirmState.error" class="nt-err">{{ confirmState.error }}</p>
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
</style>
