<script setup lang="ts">
withDefaults(defineProps<{ modelValue?: boolean; label?: string; disabled?: boolean }>(), {
  modelValue: false,
  label: '',
  disabled: false,
})

const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()
</script>

<template>
  <label class="nt-switch">
    <button
      type="button"
      role="switch"
      :aria-checked="modelValue"
      :disabled="disabled"
      class="nt-switch__track"
      :class="{ on: modelValue }"
      @click="emit('update:modelValue', !modelValue)"
    >
      <span class="nt-switch__thumb" />
    </button>
    <span v-if="label" class="nt-switch__label">{{ label }}</span>
  </label>
</template>

<style scoped>
.nt-switch {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}

.nt-switch__track {
  position: relative;
}

.nt-switch__thumb {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--nt-text-3);
  transition:
    transform var(--nt-transition),
    background var(--nt-transition);
}

.nt-switch__track.on .nt-switch__thumb {
  transform: translateX(16px);
  background: var(--nt-text-1);
}

.nt-switch__label {
  font-size: 13px;
  color: var(--nt-text-1);
}
</style>
