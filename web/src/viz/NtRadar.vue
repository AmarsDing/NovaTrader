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

/* §5.2：缺失的维度画成断开的弧而不是 0。相邻维度都有分时连成一段；
 * null 维断开，单独给一枚小缺口标记（菱形），不 clamp 到 0.04。 */
const arcs = computed(() => {
  const count = props.dims.length
  if (count < 3) return []
  const ratioOf = (dim: RadarDim) => (dim.value == null ? null : Math.max(0, Math.min(100, dim.value)) / 100)
  const segments: string[] = []
  let run: string[] = []
  const flush = () => {
    if (run.length >= 2) segments.push(run.join(' '))
    run = []
  }
  for (let index = 0; index < count; index += 1) {
    const ratio = ratioOf(props.dims[index])
    if (ratio == null) {
      flush()
      continue
    }
    run.push(point(index, Math.max(ratio, 0.04), count))
  }
  /* 首尾都在同一条弧里时接上（雷达是闭合的） */
  if (run.length > 0 && segments.length > 0) {
    const first = segments[0].split(' ')
    segments[0] = [...run, ...first].join(' ')
  } else if (run.length >= 2) {
    segments.push(run.join(' '))
  } else if (run.length === 1 && segments.length === 0) {
    /* 只有一维有分：画不出弧，给一个最小环点提示 */
    segments.push(`${run[0]} ${run[0]}`)
  }
  return segments
})

const gaps = computed(() => {
  const count = props.dims.length
  if (count < 3) return []
  return props.dims
    .map((dim, index) => (dim.value == null ? point(index, 0.1, count) : null))
    .filter((dot): dot is string => dot != null)
})
</script>

<template>
  <svg class="nt-radar" :viewBox="'0 0 100 100'" :width="size" :height="size" aria-hidden="true">
    <polygon :points="frame" fill="none" stroke="rgba(0,229,255,0.25)" />
    <polygon
      v-for="arc in arcs"
      :key="arc"
      :points="arc"
      fill="rgba(0,229,255,0.16)"
      stroke="#00E5FF"
      stroke-width="1.2"
    />
    <circle v-for="gap in gaps" :key="gap" :cx="gap.split(',')[0]" :cy="gap.split(',')[1]" r="2" fill="none" stroke="rgba(255,170,0,0.8)" stroke-dasharray="1.5 1.5" />
  </svg>
</template>
