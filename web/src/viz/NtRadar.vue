<script setup lang="ts">
import { computed } from 'vue'

export interface RadarDim {
  label: string
  value: number | null
}

const props = withDefaults(
  defineProps<{
    dims: RadarDim[]
    size?: number
  }>(),
  { size: 96 },
)

const CENTER = 50
const RADIUS = 34

function point(index: number, ratio: number, count: number): string {
  const angle = -Math.PI / 2 + (index * 2 * Math.PI) / count
  return `${CENTER + Math.cos(angle) * RADIUS * ratio},${CENTER + Math.sin(angle) * RADIUS * ratio}`
}

const frame = computed(() => {
  const count = props.dims.length
  if (count < 3) return ''
  return props.dims.map((_, index) => point(index, 1, count)).join(' ')
})

const shape = computed(() => {
  const count = props.dims.length
  if (count < 3) return ''
  return props.dims
    .map((dim, index) => {
      const value = Math.max(0, Math.min(100, dim.value ?? 0)) / 100
      return point(index, Math.max(value, 0.04), count)
    })
    .join(' ')
})
</script>

<template>
  <svg class="nt-radar" :viewBox="'0 0 100 100'" :width="size" :height="size" aria-hidden="true">
    <polygon :points="frame" fill="none" stroke="rgba(0,229,255,0.25)" />
    <polygon :points="shape" fill="rgba(0,229,255,0.16)" stroke="#00E5FF" stroke-width="1.2" />
  </svg>
</template>
