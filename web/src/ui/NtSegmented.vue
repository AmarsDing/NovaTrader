<script setup lang="ts">
export interface SegmentOption {
  value: string | number
  label: string
}

const props = withDefaults(
  defineProps<{
    modelValue?: string | number | null
    options?: SegmentOption[]
    size?: 'sm' | 'md'
  }>(),
  { modelValue: '', options: () => [], size: 'md' },
)

const emit = defineEmits<{ (e: 'update:modelValue', value: string | number): void }>()

function pick(value: string | number) {
  emit('update:modelValue', value)
}
</script>

<template>
  <div class="nt-seg" :class="{ 'nt-seg--sm': size === 'sm' }" role="radiogroup">
    <button
      v-for="opt in props.options"
      :key="String(opt.value)"
      type="button"
      role="radio"
      :aria-checked="String(opt.value) === String(modelValue)"
      class="nt-seg__item"
      :class="{ on: String(opt.value) === String(modelValue) }"
      @click="pick(opt.value)"
    >
      {{ opt.label }}
    </button>
  </div>
</template>

<style scoped>
.nt-seg {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--nt-bg-0);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}

.nt-seg__item {
  min-height: 26px;
  padding: 0 12px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--nt-text-2);
  background: transparent;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  transition: color var(--nt-transition), background var(--nt-transition);
}

.nt-seg--sm .nt-seg__item {
  min-height: 22px;
  padding: 0 9px;
  font-size: 12px;
}

.nt-seg__item:hover {
  color: var(--nt-text-1);
}

.nt-seg__item.on {
  color: var(--nt-accent);
  background: rgba(0, 229, 255, 0.1);
}
</style>
