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

/* 轨道样式整块收回 scoped（基件自洽）：原散在全局 controls.css，scoped 选择器对元素级
 * button 规则天然高优先级；focus-visible 走全局 button:focus-visible 通用描边（controls.css:74）。 */
.nt-switch__track {
  position: relative;
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: flex-start;
  gap: 0;
  box-sizing: border-box;
  width: 36px;
  min-width: 36px;
  height: 20px;
  min-height: 20px;
  padding: 0;
  font-weight: 400;
  letter-spacing: 0;
  line-height: 0;
  color: inherit;
  background: var(--nt-bg-3);
  border: 1px solid var(--nt-border-2);
  border-radius: 999px;
  box-shadow: none;
  transform: none;
}

.nt-switch__track:hover {
  background: var(--nt-bg-3);
  border-color: var(--nt-text-3);
  transform: none;
}

.nt-switch__track:active {
  transform: none;
}

.nt-switch__track.on {
  background: color-mix(in srgb, var(--nt-accent) 28%, var(--nt-bg-3));
  border-color: var(--nt-accent);
}

.nt-switch__track.on:hover {
  background: color-mix(in srgb, var(--nt-accent) 36%, var(--nt-bg-3));
  border-color: var(--nt-accent);
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
  font-size: var(--nt-fs-panel); /* 收编自 13px 裸值（§5.3 panel 档，同值） */
  color: var(--nt-text-1);
}
</style>
