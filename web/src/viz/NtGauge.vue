<script setup lang="ts">
import { computed } from 'vue'
import { PALETTE } from '@/theme/tokens'

export type GaugeTone = 'accent' | 'warn' | 'danger' | 'up' | 'down'

const props = withDefaults(
  defineProps<{
    value?: number | null
    min?: number
    max?: number
    label?: string
    unit?: string
    tone?: GaugeTone
    size?: number
  }>(),
  { value: null, min: 0, max: 100, label: '', unit: '', tone: 'accent', size: 120 },
)

const TONE_COLOR: Record<GaugeTone, string> = {
  accent: PALETTE.accent,
  warn: PALETTE.warn,
  danger: PALETTE.danger,
  up: PALETTE.up,
  down: PALETTE.down,
}

// 半环（-220° 起扫 260°）
const START = -220
const SWEEP = 260

const puree = computed(() => {
  const v = Number(props.value)
  if (!Number.isFinite(v)) return 0
  return Math.max(0, Math.min(1, (v - props.min) / (props.max - props.min || 1)))
})

function polar(angleDeg: number, r: number): [number, number] {
  const rad = (angleDeg * Math.PI) / 180
  return [50 + Math.cos(rad) * r, 50 + Math.sin(rad) * r]
}

function arcPath(from: number, to: number, r: number): string {
  const [x1, y1] = polar(from, r)
  const [x2, y2] = polar(to, r)
  const large = to - from > 180 ? 1 : 0
  return `M ${x1.toFixed(2)} ${y1.toFixed(2)} A ${r} ${r} 0 ${large} 1 ${x2.toFixed(2)} ${y2.toFixed(2)}`
}

const trackPath = computed(() => arcPath(START, START + SWEEP, 38))
const barPath = computed(() => arcPath(START, START + SWEEP * Math.max(0.004, puree.value), 38))
const color = computed(() => TONE_COLOR[props.tone])
</script>

<template>
  <figure class="nt-gauge" :style="{ width: `${size}px` }">
    <svg viewBox="0 0 100 66" class="nt-gauge__svg">
      <path :d="trackPath" fill="none" stroke="var(--nt-bg-3)" stroke-width="7" stroke-linecap="round" />
      <path
        :d="barPath"
        fill="none"
        :stroke="color"
        stroke-width="7"
        stroke-linecap="round"
        :style="{ transition: 'stroke-dasharray .4s' }"
      />
    </svg>
    <figcaption class="nt-gauge__text">
      <b class="nt-gauge__value" :style="{ color }">{{ value == null ? '—' : value }}{{ unit }}</b>
      <span v-if="label" class="nt-gauge__label">{{ label }}</span>
    </figcaption>
  </figure>
</template>

<style scoped>
.nt-gauge {
  margin: 0;
  display: inline-flex;
  flex-direction: column;
  align-items: center;
}

.nt-gauge__svg {
  width: 100%;
}

.nt-gauge__text {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-top: -14px;
}

.nt-gauge__value {
  font-family: var(--nt-font-mono);
  font-size: 17px;
  font-weight: 700;
}

.nt-gauge__label {
  font-size: 11px;
  color: var(--nt-text-3);
}
</style>
