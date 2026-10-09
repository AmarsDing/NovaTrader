<script setup lang="ts">
export interface TabItem {
  value: string | number
  label: string
}

withDefaults(
  defineProps<{ modelValue?: string | number | null; items?: TabItem[] }>(),
  { modelValue: '', items: () => [] },
)

const emit = defineEmits<{ (e: 'update:modelValue', value: string | number): void }>()
</script>

<template>
  <div class="nt-tabs" role="tablist">
    <button
      v-for="tab in items"
      :key="String(tab.value)"
      type="button"
      role="tab"
      :aria-selected="String(tab.value) === String(modelValue)"
      class="nt-tabs__item"
      :class="{ on: String(tab.value) === String(modelValue) }"
      @click="emit('update:modelValue', tab.value)"
    >
      {{ tab.label }}
    </button>
  </div>
</template>

<style scoped>
.nt-tabs {
  display: flex;
  gap: 2px;
  border-bottom: 1px solid var(--nt-border-1);
}

.nt-tabs__item {
  position: relative;
  min-height: 32px;
  padding: 0 14px;
  font-size: 13px;
  font-weight: 500;
  color: var(--nt-text-2);
  background: transparent;
  border: none;
  cursor: pointer;
  transition: color var(--nt-transition);
}

.nt-tabs__item:hover {
  color: var(--nt-text-1);
}

.nt-tabs__item.on {
  color: var(--nt-accent);
}

.nt-tabs__item.on::after {
  content: '';
  position: absolute;
  left: 10px;
  right: 10px;
  bottom: -1px;
  height: 2px;
  background: var(--nt-accent);
}
</style>
