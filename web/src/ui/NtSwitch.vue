<script setup lang="ts">
withDefaults(defineProps<{ modelValue?: boolean; label?: string }>(), {
  modelValue: false,
  label: '',
})

const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()
</script>

<template>
  <label class="nt-switch">
    <button
      type="button"
      role="switch"
      :aria-checked="modelValue"
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
  width: 34px;
  height: 18px;
  padding: 0;
  background: var(--nt-bg-3);
  border: 1px solid var(--nt-border-2);
  border-radius: 9px;
  cursor: pointer;
  transition: background var(--nt-transition), border-color var(--nt-transition);
}

.nt-switch__track.on {
  background: rgba(0, 229, 255, 0.24);
  border-color: var(--nt-accent);
}

.nt-switch__thumb {
  position: absolute;
  top: 1px;
  left: 1px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--nt-text-2);
  transition: transform var(--nt-transition), background var(--nt-transition);
}

.nt-switch__track.on .nt-switch__thumb {
  transform: translateX(16px);
  background: var(--nt-accent);
}

.nt-switch__label {
  font-size: 13px;
  color: var(--nt-text-2);
}
</style>
