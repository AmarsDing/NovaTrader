<script setup lang="ts">
import { useAlertsStore } from '@/stores/alerts'
import NtIcon from './NtIcon.vue'
import type { IconName } from './icons'

const alerts = useAlertsStore()

const ICON: Record<string, IconName> = {
  info: 'bell',
  success: 'check',
  warn: 'warning',
  error: 'close',
}
</script>

<template>
  <Teleport to="body">
    <div class="nt-toasts" aria-live="polite">
      <TransitionGroup name="nt-toast">
        <div v-for="t in alerts.toasts" :key="t.id" class="nt-toast" :class="`nt-toast--${t.kind}`">
          <NtIcon :name="ICON[t.kind] || 'bell'" :size="14" />
          <div class="nt-toast__body">
            <p class="nt-toast__title">{{ t.title }}</p>
            <p v-if="t.body" class="nt-toast__text">{{ t.body }}</p>
          </div>
          <button type="button" class="nt-toast__close" @click="alerts.dismissToast(t.id)">
            <NtIcon name="close" :size="12" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.nt-toasts {
  position: fixed;
  top: 16px;
  right: 16px;
  z-index: 120;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: min(340px, calc(100vw - 32px));
  pointer-events: none;
}

.nt-toast {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 10px 12px;
  background: var(--nt-bg-glass);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-m);
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(10px);
  pointer-events: auto;
}

.nt-toast--info {
  border-left: 3px solid var(--nt-info);
}

.nt-toast--success {
  border-left: 3px solid var(--nt-success);
}

.nt-toast--warn {
  border-left: 3px solid var(--nt-warn);
}

.nt-toast--error {
  border-left: 3px solid var(--nt-danger);
}

.nt-toast__body {
  flex: 1;
  min-width: 0;
}

.nt-toast__title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-toast__text {
  margin: 3px 0 0;
  font-size: 12px;
  color: var(--nt-text-2);
  word-break: break-all;
}

.nt-toast__close {
  padding: 2px;
  color: var(--nt-text-3);
  background: none;
  border: none;
  cursor: pointer;
}

.nt-toast__close:hover {
  color: var(--nt-text-1);
}

.nt-toast-enter-active,
.nt-toast-leave-active {
  transition: all 0.22s ease;
}

.nt-toast-enter-from {
  opacity: 0;
  transform: translateX(18px);
}

.nt-toast-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}
</style>
