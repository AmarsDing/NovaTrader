<script setup lang="ts">
export type DotStatus = 'live' | 'ok' | 'stale' | 'down' | 'off'

withDefaults(defineProps<{ status?: DotStatus; label?: string }>(), { status: 'off', label: '' })
</script>

<template>
  <span class="nt-dot" :class="`nt-dot--${status}`">
    <i class="nt-dot__core" aria-hidden="true" />
    <span v-if="label" class="nt-dot__label">{{ label }}</span>
  </span>
</template>

<style scoped>
.nt-dot {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.nt-dot__core {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--nt-text-3);
}

.nt-dot__label {
  font-size: 12px;
  color: var(--nt-text-2);
}

/* §7 禁常驻呼吸灯：live 常亮实点，状态迁移由父级重挂载/翻转类承担，不留眨眼。 */
.nt-dot--live .nt-dot__core {
  background: var(--nt-success);
}

.nt-dot--ok .nt-dot__core {
  background: var(--nt-success);
}

.nt-dot--stale .nt-dot__core {
  background: var(--nt-warn);
}

.nt-dot--down .nt-dot__core {
  background: var(--nt-danger);
}
</style>
