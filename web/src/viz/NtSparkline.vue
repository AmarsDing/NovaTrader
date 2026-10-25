<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    values: (number | null)[]
    width?: number
    height?: number
    stroke?: string
    fill?: boolean
  }>(),
  { width: 96, height: 28, stroke: 'var(--nt-accent)', fill: false },
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
  <!-- 数据不足两点：显态占位（直觉5 数据缺失先设计），断线/全 null 不留空白装没事 -->
  <span v-if="!points" class="nt-spark__empty" :style="{ width: `${width}px`, height: `${height}px` }">无数据</span>
  <svg
    v-else
    class="nt-spark"
    :viewBox="`0 0 ${width} ${height}`"
    preserveAspectRatio="none"
    :width="width"
    :height="height"
    aria-hidden="true"
  >
    <polygon v-if="fill && area" :points="area" :fill="stroke" opacity="0.14" />
    <polyline :points="points" fill="none" :stroke="stroke" stroke-width="1.4" />
  </svg>
</template>

<style scoped>
.nt-spark {
  display: block;
  overflow: visible;
}

.nt-spark__empty {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}
</style>
