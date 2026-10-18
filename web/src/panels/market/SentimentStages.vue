<script setup lang="ts">
/**
 * 市场情绪五阶段（M02 §6）：冰点 → 修复 → 升温 → 高潮 → 退潮。
 * 当前阶段高亮，并显示评分系数 / 仓位系数 / 风险等级（缺省用规则表默认值）。
 */
import { computed } from 'vue'

const STAGES = [
  { code: 'ICE', label: '冰点', coef: 0.8, scale: 0.3 },
  { code: 'RECOVER', label: '修复', coef: 0.95, scale: 0.6 },
  { code: 'WARM', label: '升温', coef: 1.0, scale: 1.0 },
  { code: 'HOT', label: '高潮', coef: 1.05, scale: 0.8 },
  { code: 'FADE', label: '退潮', coef: 0.85, scale: 0.3 },
] as const

const props = withDefaults(
  defineProps<{
    phase?: string
    scoreCoef?: number | null
    positionScale?: number | null
    riskLevel?: number | null
    stale?: boolean
  }>(),
  { phase: '', scoreCoef: null, positionScale: null, riskLevel: null, stale: false },
)

const current = computed(() => props.phase.trim().toUpperCase())
const stage = computed(() => STAGES.find((item) => item.code === current.value) ?? null)
const coef = computed(() => props.scoreCoef ?? stage.value?.coef ?? null)
const scale = computed(() => props.positionScale ?? stage.value?.scale ?? null)
</script>

<template>
  <section class="stages">
    <header class="stages__head">
      <h3 class="nt-panel__title">市场情绪</h3>
      <span class="stages__meta">
        <span v-if="stale" class="stages__stale">快照过期</span>
        <span v-if="riskLevel != null" class="stages__risk" :class="`r${riskLevel}`">风险 R{{ riskLevel }}</span>
        <span v-if="coef != null">评分系数 {{ coef.toFixed(2) }}</span>
        <span v-if="scale != null">仓位系数 {{ scale.toFixed(2) }}</span>
      </span>
    </header>

    <ol class="stages__track">
      <li v-for="item in STAGES" :key="item.code" class="stage" :class="{ on: item.code === current, past: !current }">
        <span class="stage__label">{{ item.label }}</span>
        <span class="stage__code">{{ item.code }}</span>
      </li>
    </ol>

    <p v-if="!current" class="nt-note">没有全市场快照，阶段先不标。</p>
  </section>
</template>

<style scoped>
.stages {
  padding: var(--nt-pad-panel);
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.stages__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}

.stages__meta {
  display: flex;
  gap: 10px;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.stages__stale {
  color: var(--nt-warn);
}

.stages__risk.r3 {
  color: var(--nt-danger);
}

.stages__risk.r2 {
  color: var(--nt-warn);
}

.stages__track {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.stage {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: 8px 4px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-0);
}

.stage__label {
  font-size: 12.5px;
  color: var(--nt-text-2);
}

.stage__code {
  font-family: var(--nt-font-mono);
  font-size: 10px;
  color: var(--nt-text-3);
}

.stage.on {
  border-color: var(--nt-accent-border);
  background: var(--nt-accent-soft);
}

.stage.on .stage__label {
  color: var(--nt-accent);
  font-weight: 600;
}

.stage.on .stage__code {
  color: var(--nt-accent);
}
</style>
