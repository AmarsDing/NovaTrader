<script setup lang="ts">
import { computed } from 'vue'
import { PALETTE } from '@/theme/tokens'

const props = withDefaults(
  defineProps<{
    values: (number | null)[]
    width?: number
    height?: number
    stroke?: string
    fill?: boolean
  }>(),
  { width: 96, height: 28, stroke: PALETTE.accent, fill: false },
)

const points = computed(() => {
  const nums = props.values.filter((v): v is number => typeof v === 'number' && Number.isFinite(v))
  if (nums.length < 2) return ''
  const min = Math.min(...nums)
  const max = Math.max(...nums)
  const range = max - min || 1
  return nums
    .map((v, i) => {
      const x = (i / (nums.length - 1)) * props.width
      const y = props.height - ((v - min) / range) * (props.height - 2) - 1
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
})

const area = computed(() => {
  if (!points.value) return ''
  return `0,${props.height} ${points.value} ${props.width},${props.height}`
})
</script>

<template>
  <svg
    class="nt-spark"
    :viewBox="`0 0 ${width} ${height}`"
    preserveAspectRatio="none"
    :width="width"
    :height="height"
    aria-hidden="true"
  >
    <polygon v-if="fill && area" :points="area" :fill="stroke" opacity="0.14" />
    <polyline v-if="points" :points="points" fill="none" :stroke="stroke" stroke-width="1.4" />
  </svg>
</template>

<style scoped>
.nt-spark {
  display: block;
  overflow: visible;
}
</style>
