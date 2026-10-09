<script setup lang="ts">
import NtIcon from './NtIcon.vue'
import type { IconName } from './icons'

withDefaults(
  defineProps<{
    modelValue?: string | number | null
    label?: string
    placeholder?: string
    type?: 'text' | 'number' | 'password' | 'search'
    icon?: IconName | null
    mono?: boolean
    invalid?: boolean
    hint?: string
  }>(),
  {
    modelValue: '',
    label: '',
    placeholder: '',
    type: 'text',
    icon: null,
    mono: false,
    invalid: false,
    hint: '',
  },
)

const emit = defineEmits<{ (e: 'update:modelValue', value: string): void }>()

function onInput(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement).value)
}
</script>

<template>
  <label class="nt-input">
    <span v-if="label" class="nt-input__label">{{ label }}</span>
    <span class="nt-input__box" :class="{ 'nt-input__box--invalid': invalid }">
      <NtIcon v-if="icon" :name="icon" :size="14" />
      <input
        :type="type"
        :value="modelValue"
        :placeholder="placeholder"
        :class="{ 'nt-input__field--mono': mono }"
        @input="onInput"
      />
    </span>
    <span v-if="hint" class="nt-input__hint">{{ hint }}</span>
  </label>
</template>

<style scoped>
.nt-input {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.nt-input__label {
  font-size: 12px;
  color: var(--nt-text-3);
}

.nt-input__box {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 10px;
  background: var(--nt-bg-0);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  color: var(--nt-text-3);
  transition: border-color var(--nt-transition), box-shadow var(--nt-transition);
}

.nt-input__box:focus-within {
  border-color: rgba(0, 229, 255, 0.55);
  box-shadow: 0 0 0 3px rgba(0, 229, 255, 0.15);
}

.nt-input__box--invalid {
  border-color: var(--nt-danger);
}

.nt-input__box input {
  flex: 1;
  min-width: 0;
  min-height: 32px;
  padding: 0;
  font: inherit;
  font-size: 13px;
  color: var(--nt-text-1);
  background: transparent;
  border: none;
  outline: none;
}

.nt-input__field--mono {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.nt-input__hint {
  font-size: 11px;
  color: var(--nt-text-3);
}
</style>
