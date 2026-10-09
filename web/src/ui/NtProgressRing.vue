<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{ value?: number | null; size?: number; hot?: boolean; label?: string }>(),
  { value: null, size: 56, hot: false, label: '' },
)

const clamped = computed(() => {
  const v = Number(props.value)
  if (!Number.isFinite(v)) return 0
  return Math.max(0, Math.min(100, v))
})

const R = 21
const CIRC = 2 * Math.PI * R
const dash = computed(() => `${(clamped.value / 100) * CIRC} ${CIRC}`)
</script>

<template>
  <span class="nt-ring" :class="{ 'nt-ring--hot': hot }" :style="{ width: `${size}px`, height: `${size}px` }">
    <svg viewBox="0 0 48 48" aria-hidden="true">
      <circle class="nt-ring__bg" cx="24" cy="24" :r="R" />
      <circle class="nt-ring__bar" cx="24" cy="24" :r="R" :stroke-dasharray="dash" />
    </svg>
    <span class="nt-ring__value">{{ value == null ? '—' : Math.round(Number(value)) }}</span>
    <span v-if="label" class="nt-ring__label">{{ label }}</span>
  </span>
</template>

<style scoped>
.nt-ring {
  position: relative;
  display: inline-grid;
  place-items: center;
}

.nt-ring svg {
  position: absolute;
  inset: 0;
  transform: rotate(-90deg);
}

.nt-ring circle {
  fill: none;
  stroke-width: 4;
}

.nt-ring__bg {
  stroke: var(--nt-bg-3);
}

.nt-ring__bar {
  stroke: var(--nt-accent);
  stroke-linecap: round;
  transition: stroke-dasharray 0.4s ease;
}

/* 高价值（>=85）发光：发光预算允许的三处之一。 */
.nt-ring--hot .nt-ring__bar {
  stroke: var(--nt-accent);
  filter: drop-shadow(0 0 5px rgba(0, 229, 255, 0.8));
}

.nt-ring--hot .nt-ring__value {
  color: var(--nt-accent);
  text-shadow: 0 0 8px rgba(0, 229, 255, 0.6);
}

.nt-ring__value {
  font-family: var(--nt-font-mono);
  font-size: 15px;
  font-weight: 700;
  color: var(--nt-text-1);
}

.nt-ring__label {
  position: absolute;
  bottom: -16px;
  font-size: 10px;
  color: var(--nt-text-3);
  white-space: nowrap;
}
</style>
