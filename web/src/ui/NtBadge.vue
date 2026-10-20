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
  font-size: var(--nt-fs-caption);
  font-weight: 600;
  line-height: 18px;
  border-radius: 999px;
  border: 1px solid var(--nt-border-2);
  color: var(--nt-text-2);
  background: var(--nt-bg-2);
  white-space: nowrap;
}

.nt-badge--accent {
  color: color-mix(in srgb, var(--nt-accent) 75%, var(--nt-text-1));
  border-color: var(--nt-accent-border);
  background: var(--nt-accent-soft);
}

.nt-badge--success {
  color: color-mix(in srgb, var(--nt-success) 75%, var(--nt-text-1));
  border-color: color-mix(in srgb, var(--nt-success) 40%, transparent);
  background: color-mix(in srgb, var(--nt-success) 8%, transparent);
}

.nt-badge--warn {
  color: color-mix(in srgb, var(--nt-warn) 75%, var(--nt-text-1));
  border-color: color-mix(in srgb, var(--nt-warn) 40%, transparent);
  background: color-mix(in srgb, var(--nt-warn) 8%, transparent);
}

.nt-badge--danger {
  color: color-mix(in srgb, var(--nt-danger) 75%, var(--nt-text-1));
  border-color: color-mix(in srgb, var(--nt-danger) 40%, transparent);
  background: color-mix(in srgb, var(--nt-danger) 8%, transparent);
}

.nt-badge--info {
  color: color-mix(in srgb, var(--nt-info) 75%, var(--nt-text-1));
  border-color: color-mix(in srgb, var(--nt-info) 40%, transparent);
  background: color-mix(in srgb, var(--nt-info) 8%, transparent);
}

.nt-badge__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
</style>
