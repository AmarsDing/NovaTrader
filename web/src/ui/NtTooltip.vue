<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

withDefaults(defineProps<{ text?: string; placement?: 'top' | 'bottom' }>(), {
  text: '',
  placement: 'top',
})

const open = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null

function show() {
  timer = setTimeout(() => {
    open.value = true
  }, 200)
}

function hide() {
  if (timer) clearTimeout(timer)
  timer = null
  open.value = false
}

onMounted(() => {})
onUnmounted(() => {
  if (timer) clearTimeout(timer)
})
</script>

<template>
  <span class="nt-tip" @mouseenter="show" @mouseleave="hide" @focusin="show" @focusout="hide">
    <slot />
    <span v-if="open && text" class="nt-tip__bubble" :class="`nt-tip__bubble--${placement}`" role="tooltip">
      {{ text }}
    </span>
  </span>
</template>

<style scoped>
.nt-tip {
  position: relative;
  display: inline-flex;
}

.nt-tip__bubble {
  position: absolute;
  left: 50%;
  z-index: 60;
  max-width: 260px;
  padding: 5px 9px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--nt-text-1);
  white-space: normal;
  background: var(--nt-bg-3);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.4);
  pointer-events: none;
}

.nt-tip__bubble--top {
  bottom: calc(100% + 7px);
  transform: translateX(-50%);
}

.nt-tip__bubble--bottom {
  top: calc(100% + 7px);
  transform: translateX(-50%);
}
</style>
