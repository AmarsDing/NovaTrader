<script setup lang="ts">
export type StatTone = 'neutral' | 'up' | 'down' | 'accent' | 'warn' | 'danger'

withDefaults(
  defineProps<{
    label: string
    value: string
    sub?: string
    tone?: StatTone
    hero?: boolean
  }>(),
  { sub: '', tone: 'neutral', hero: false },
)
</script>

<template>
  <article class="nt-stat" :class="{ 'nt-stat--hero': hero }">
    <p class="nt-stat__k">{{ label }}</p>
    <p class="nt-stat__v" :class="[tone, { 'nt-holo-num': hero }]">{{ value }}</p>
    <p v-if="sub" class="nt-stat__s">{{ sub }}</p>
    <slot />
  </article>
</template>

<style scoped>
.nt-stat {
  padding: 10px 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.nt-stat__k {
  margin: 0;
  font-size: 11px;
  color: var(--nt-text-3);
}

.nt-stat__v {
  margin: 4px 0 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 19px;
  font-weight: 700;
  color: var(--nt-text-1);
}

.nt-stat--hero .nt-stat__v {
  font-size: 24px;
}

.nt-stat__v.up {
  color: var(--nt-up);
}

.nt-stat__v.down {
  color: var(--nt-down);
}

.nt-stat__v.accent {
  color: var(--nt-accent);
}

.nt-stat__v.warn {
  color: var(--nt-warn);
}

.nt-stat__v.danger {
  color: var(--nt-danger);
}

.nt-stat__s {
  margin: 3px 0 0;
  font-size: 11px;
  color: var(--nt-text-2);
}
</style>
