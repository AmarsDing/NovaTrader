<script setup lang="ts">
export interface SelectOption {
  value: string | number
  label: string
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue?: string | number | null
    options?: SelectOption[]
    label?: string
    placeholder?: string
  }>(),
  { modelValue: '', options: () => [], label: '', placeholder: '' },
)

const emit = defineEmits<{ (e: 'update:modelValue', value: string | number): void }>()

function onChange(event: Event) {
  const raw = (event.target as HTMLSelectElement).value
  const hit = props.options.find((opt) => String(opt.value) === raw)
  emit('update:modelValue', hit ? hit.value : raw)
}
</script>

<template>
  <label class="nt-select">
    <span v-if="label" class="nt-select__label">{{ label }}</span>
    <select :value="String(modelValue ?? '')" @change="onChange">
      <option v-if="placeholder" value="" disabled>{{ placeholder }}</option>
      <option v-for="opt in options" :key="String(opt.value)" :value="String(opt.value)" :disabled="opt.disabled">
        {{ opt.label }}
      </option>
    </select>
  </label>
</template>

<style scoped>
.nt-select {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.nt-select__label {
  font-size: 12px;
  color: var(--nt-text-3);
}

.nt-select select {
  appearance: none;
  min-height: 32px;
  padding: 0 28px 0 10px;
  font-size: 13px;
  color: var(--nt-text-1);
  background-color: var(--nt-bg-0);
  background-image:
    linear-gradient(45deg, transparent 50%, var(--nt-accent) 50%),
    linear-gradient(135deg, var(--nt-accent) 50%, transparent 50%);
  background-position:
    calc(100% - 15px) 52%,
    calc(100% - 10px) 52%;
  background-size:
    5px 5px,
    5px 5px;
  background-repeat: no-repeat;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  outline: none;
  cursor: pointer;
  transition: border-color var(--nt-transition);
}

.nt-select select:focus {
  border-color: rgba(0, 229, 255, 0.55);
  box-shadow: 0 0 0 3px rgba(0, 229, 255, 0.15);
}
</style>
