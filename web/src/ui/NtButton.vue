<script setup lang="ts">
import NtIcon from './NtIcon.vue'
import type { IconName } from './icons'

export type ButtonVariant = 'primary' | 'ghost' | 'danger' | 'success' | 'quiet'

withDefaults(
  defineProps<{
    variant?: ButtonVariant
    icon?: IconName | null
    size?: 'sm' | 'md'
    loading?: boolean
    block?: boolean
  }>(),
  { variant: 'ghost', icon: null, size: 'md', loading: false, block: false },
)
</script>

<template>
  <button
    type="button"
    class="nt-btn"
    :class="[`nt-btn--${variant}`, { 'nt-btn--sm': size === 'sm', 'nt-btn--block': block }]"
    :disabled="loading || $attrs.disabled !== undefined && !!($attrs.disabled as boolean)"
  >
    <span v-if="loading" class="nt-btn__spin" aria-hidden="true" />
    <NtIcon v-else-if="icon" :name="icon" />
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
  color: var(--nt-accent);
  background: rgba(0, 229, 255, 0.06);
  border: 1px solid rgba(0, 229, 255, 0.32);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
  transition:
    background var(--nt-transition),
    border-color var(--nt-transition),
    filter var(--nt-transition);
}

.nt-btn:hover:not(:disabled) {
  background: rgba(0, 229, 255, 0.13);
  border-color: rgba(0, 229, 255, 0.55);
}

.nt-btn:active:not(:disabled) {
  transform: scale(0.98);
}

.nt-btn:focus-visible {
  outline: none;
  box-shadow: 0 0 0 3px rgba(0, 229, 255, 0.22);
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

.nt-btn--block {
  width: 100%;
}

.nt-btn--primary {
  color: var(--nt-bg-0);
  background: var(--nt-accent);
  border-color: transparent;
}

.nt-btn--primary:hover:not(:disabled) {
  background: var(--nt-accent);
  filter: brightness(1.12);
}

.nt-btn--danger {
  color: #fff;
  background: var(--nt-danger);
  border-color: transparent;
}

.nt-btn--danger:hover:not(:disabled) {
  background: var(--nt-danger);
  filter: brightness(1.12);
}

.nt-btn--success {
  color: var(--nt-bg-0);
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
