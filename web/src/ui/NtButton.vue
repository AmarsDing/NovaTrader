<script setup lang="ts">
import { computed, useAttrs } from 'vue'
import NtIcon from './NtIcon.vue'
import type { IconName } from './icons'

defineOptions({ inheritAttrs: false })

export type ButtonVariant = 'primary' | 'ghost' | 'danger' | 'success' | 'quiet'

const props = withDefaults(
  defineProps<{
    variant?: ButtonVariant
    icon?: IconName | null
    /** lg=主行动档（B3）：单屏唯一主行动（买卖/提交类）才用，非默认放大器。 */
    size?: 'sm' | 'md' | 'lg'
    loading?: boolean
    block?: boolean
    disabled?: boolean
    /** 禁用原因（B3）：禁用时作悬浮说明。禁用钮本身不发指针事件，经外层包裹 span 承载 title——
     *  仅在「禁用且给了原因」时加包裹，既有调用 DOM 结构零变化（golden 定格帧安全）。 */
    disabledReason?: string
    /** 默认 button；表单内主操作传 submit */
    type?: 'button' | 'submit' | 'reset'
  }>(),
  { variant: 'ghost', icon: null, size: 'md', loading: false, block: false, disabled: false, disabledReason: '', type: 'button' },
)

const attrs = useAttrs()
const classes = computed(() => [
  `nt-btn--${props.variant}`,
  { 'nt-btn--sm': props.size === 'sm', 'nt-btn--lg': props.size === 'lg', 'nt-btn--block': props.block },
])
/** 禁用且给了原因才包一层：浏览器对被禁用 button 不派发鼠标事件，title 必须挂在包裹物上。 */
const why = computed(() => (props.disabled && props.disabledReason ? props.disabledReason : ''))
</script>

<template>
  <span v-if="why" class="nt-btn-why" :title="why">
    <button v-bind="attrs" :type="props.type" class="nt-btn" :class="classes" disabled>
      <span v-if="props.loading" class="nt-btn__spin" aria-hidden="true" />
      <NtIcon v-else-if="props.icon" :name="props.icon" />
      <span class="nt-btn__label"><slot /></span>
    </button>
  </span>
  <button v-else v-bind="attrs" :type="props.type" class="nt-btn" :class="classes" :disabled="props.loading || props.disabled">
    <span v-if="props.loading" class="nt-btn__spin" aria-hidden="true" />
    <NtIcon v-else-if="props.icon" :name="props.icon" />
    <span class="nt-btn__label"><slot /></span>
  </button>
</template>

<style scoped>
.nt-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 32px;
  padding: 0 14px;
  font-size: 13px;
  font-weight: 600;
  line-height: 1;
  color: color-mix(in srgb, var(--nt-accent) 75%, var(--nt-text-1));
  background: var(--nt-accent-soft);
  border: 1px solid var(--nt-accent-border);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
  transition:
    background var(--nt-transition),
    border-color var(--nt-transition),
    filter var(--nt-transition);
}

/* 禁用原因包裹（B3）：自身不可点，悬停把原因托给 title；不影响内层按钮布局。 */
.nt-btn-why {
  display: inline-flex;
  cursor: not-allowed;
}

.nt-btn:hover:not(:disabled) {
  background: var(--nt-accent-hover);
  border-color: var(--nt-accent);
}

.nt-btn:active:not(:disabled) {
  transform: scale(0.98);
}

.nt-btn:focus-visible {
  outline: none;
  box-shadow: 0 0 0 3px var(--nt-focus-ring);
}

.nt-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.nt-btn--sm {
  min-height: 26px;
  padding: 0 9px;
  font-size: 12px;
}

.nt-btn--lg {
  min-height: 40px;
  padding: 0 20px;
  font-size: 14px;
}

.nt-btn--block {
  width: 100%;
}

.nt-btn--primary {
  color: var(--nt-on-accent);
  background: var(--nt-accent);
  border-color: transparent;
}

.nt-btn--primary:hover:not(:disabled) {
  background: var(--nt-accent);
  filter: brightness(0.94);
}

.nt-btn--danger {
  color: var(--nt-on-danger);
  background: var(--nt-danger);
  border-color: transparent;
}

.nt-btn--danger:hover:not(:disabled) {
  background: var(--nt-danger);
  filter: brightness(1.12);
}

.nt-btn--success {
  color: var(--nt-on-success);
  background: var(--nt-success);
  border-color: transparent;
}

.nt-btn--success:hover:not(:disabled) {
  background: var(--nt-success);
  filter: brightness(1.1);
}

.nt-btn--quiet {
  color: var(--nt-text-2);
  background: transparent;
  border-color: var(--nt-border-2);
}

.nt-btn--quiet:hover:not(:disabled) {
  color: var(--nt-text-1);
  background: var(--nt-bg-2);
  border-color: var(--nt-border-2);
}

.nt-btn__spin {
  width: 12px;
  height: 12px;
  border: 2px solid currentColor;
  border-top-color: transparent;
  border-radius: 50%;
  animation: nt-spin 0.7s linear infinite;
}

@keyframes nt-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
