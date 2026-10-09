<script setup lang="ts">
export type BadgeTone = 'neutral' | 'accent' | 'success' | 'warn' | 'danger' | 'info'

withDefaults(defineProps<{ tone?: BadgeTone; dot?: boolean }>(), { tone: 'neutral', dot: false })
</script>

<template>
  <span class="nt-badge" :class="`nt-badge--${tone}`">
    <i v-if="dot" class="nt-badge__dot" aria-hidden="true" />
    <slot />
  </span>
</template>

<style scoped>
.nt-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 8px;
  font-size: 11.5px;
  font-weight: 600;
  line-height: 18px;
  border-radius: 999px;
  border: 1px solid var(--nt-border-2);
  color: var(--nt-text-2);
  background: var(--nt-bg-2);
  white-space: nowrap;
}

.nt-badge--accent {
  color: var(--nt-accent);
  border-color: rgba(0, 229, 255, 0.4);
  background: rgba(0, 229, 255, 0.08);
}

.nt-badge--success {
  color: var(--nt-success);
  border-color: rgba(0, 208, 132, 0.4);
  background: rgba(0, 208, 132, 0.08);
}

.nt-badge--warn {
  color: var(--nt-warn);
  border-color: rgba(255, 107, 0, 0.4);
  background: rgba(255, 107, 0, 0.08);
}

.nt-badge--danger {
  color: var(--nt-danger);
  border-color: rgba(255, 58, 58, 0.4);
  background: rgba(255, 58, 58, 0.08);
}

.nt-badge--info {
  color: var(--nt-info);
  border-color: rgba(76, 154, 255, 0.4);
  background: rgba(76, 154, 255, 0.08);
}

.nt-badge__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
</style>
