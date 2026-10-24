<script setup lang="ts">
/**
 * HUD 迷你趋势线：100×14 的 SVG 折线，数据由父级用 RingSeries 采样喂入。
 * 涨跌色只按最后一个有效值对首个有效值的方向给（红涨绿跌仅限金额/百分比类）。
 * 全 null 或单点不画线（不伪造走势）。
 */
import { computed } from 'vue'
import { sparkPath } from './sampler'

const props = defineProps<{
  points: (number | null)[]
  tone: 'up' | 'down' | 'flat'
}>()

const path = computed(() => sparkPath(props.points, 100, 14))

const stroke = computed(() => {
  // tokens.css 在 :root 恒定义三令牌，var() 兜底值永远触不到且是陈旧色——
  // G4 门禁下直接删兜底，纠错路径只有「令牌缺失=渲染断」一种（比读错色好）。
  if (props.tone === 'up') return 'var(--nt-up)'
  if (props.tone === 'down') return 'var(--nt-down)'
  return 'var(--nt-text-3)'
})
</script>

<template>
  <svg v-if="path" class="nt-spark" viewBox="0 0 100 14" preserveAspectRatio="none" aria-hidden="true">
    <path :d="path" fill="none" :stroke="stroke" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" />
  </svg>
</template>

<style scoped>
.nt-spark {
  display: block;
  width: 100%;
  height: 14px;
  opacity: 0.85;
}
</style>
