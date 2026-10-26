<script setup lang="ts">
import NtModal from './NtModal.vue'
import NtButton from './NtButton.vue'

/** 通用确认壳（B3）：删除/重置/提交前的二次确认统一走这里，不再各面板自描 modal。
 *  危险动作用 tone="danger"（红主钮+章），普通确认维持 primary。 */
withDefaults(
  defineProps<{
    open: boolean
    /** 标题写成动作句：「删除这条规则？」「重置为默认配置？」 */
    title: string
    /** 正文说明：动作后果一句话。也可用默认 slot 自填富内容。 */
    body?: string
    /** danger=不可逆/破坏性动作（红钮）；primary=普通确认。 */
    tone?: 'danger' | 'primary'
    confirmLabel?: string
    cancelLabel?: string
    /** 提交中锁双钮防连点。 */
    loading?: boolean
  }>(),
  { body: '', tone: 'primary', confirmLabel: '确认', cancelLabel: '取消', loading: false },
)
const emit = defineEmits<{ (e: 'update:open', value: boolean): void; (e: 'confirm'): void; (e: 'cancel'): void }>()

function onCancel() {
  emit('cancel')
  emit('update:open', false)
}
</script>

<template>
  <NtModal :open="open" :title="title" width="400px" @update:open="emit('update:open', $event)">
    <slot>
      <p class="nt-confirm__body">{{ body }}</p>
    </slot>
    <template #footer>
      <NtButton variant="quiet" :disabled="loading" @click="onCancel">{{ cancelLabel }}</NtButton>
      <NtButton :variant="tone === 'danger' ? 'danger' : 'primary'" :loading="loading" @click="emit('confirm')">
        {{ confirmLabel }}
      </NtButton>
    </template>
  </NtModal>
</template>

<style scoped>
.nt-confirm__body {
  margin: 0;
  font-size: var(--nt-fs-body);
  line-height: 1.6;
  color: var(--nt-text-2);
}
</style>
