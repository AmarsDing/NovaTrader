<script setup lang="ts">
export interface FunnelStage {
  label: string
  value: number | null
  hint?: string
}

defineProps<{ stages: FunnelStage[] }>()

function widthOf(stages: FunnelStage[], index: number): number {
  const first = stages[0]?.value
  const cur = stages[index]?.value
  if (first == null || cur == null || first <= 0) return 0
  return Math.max(2, Math.min(100, (cur / first) * 100))
}
</script>

<template>
  <ul class="nt-funnel">
    <li v-for="(stage, index) in stages" :key="stage.label" class="nt-funnel__row">
      <span class="nt-funnel__label">{{ stage.label }}</span>
      <span class="nt-funnel__track">
        <span class="nt-funnel__bar" :style="{ width: `${widthOf(stages, index)}%` }" />
      </span>
      <span class="nt-funnel__value">{{ stage.value == null ? '—' : stage.value }}</span>
      <span v-if="stage.hint" class="nt-funnel__hint">{{ stage.hint }}</span>
    </li>
  </ul>
</template>

<style scoped>
.nt-funnel {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.nt-funnel__row {
  display: grid;
  grid-template-columns: 84px 1fr 56px;
  gap: 8px;
  align-items: center;
  font-size: 12px;
}

.nt-funnel__label {
  color: var(--nt-text-2);
}

.nt-funnel__track {
  height: 8px;
  background: var(--nt-bg-3);
  border-radius: 4px;
  overflow: hidden;
}

.nt-funnel__bar {
  display: block;
  height: 100%;
  background: linear-gradient(90deg, rgba(0, 229, 255, 0.25), var(--nt-accent));
  border-radius: 4px;
  transition: width 0.4s ease;
}

.nt-funnel__value {
  font-family: var(--nt-font-mono);
  text-align: right;
  color: var(--nt-text-1);
}

.nt-funnel__hint {
  grid-column: 1 / -1;
  font-size: 11px;
  color: var(--nt-text-3);
}
</style>
