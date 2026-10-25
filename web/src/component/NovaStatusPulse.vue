<script setup>
import { ref, watch } from 'vue'

const props = defineProps({
  active: { type: Boolean, default: true },
})

/* 视觉需求 §7 禁常驻呼吸灯：live = 静态 accent 实点；
 * 脉冲环只在 inactive→active 翻转瞬间单发一圈（0.9s，对齐 PanelTab L3 单发纪律）。 */
const ping = ref(false)
let pingTimer = 0
watch(
  () => props.active,
  (now, before) => {
    if (!now || before) return
    ping.value = false
    requestAnimationFrame(() => {
      ping.value = true
      clearTimeout(pingTimer)
      pingTimer = setTimeout(() => {
        ping.value = false
      }, 950)
    })
  },
)
</script>

<template>
  <span class="pulse" :class="{ inactive: !active }" aria-hidden="true">
    <span class="dot" />
    <span class="ring" :class="{ ping }" />
  </span>
</template>

<style scoped>
.pulse {
  position: relative;
  display: inline-flex;
  width: 12px;
  height: 12px;
  vertical-align: middle;
}

.dot {
  position: relative;
  z-index: 1;
  width: 10px;
  height: 10px;
  margin: auto;
  border-radius: 50%;
  background: radial-gradient(circle at 30% 30%, var(--nt-pulse-glow), var(--nt-accent));
}

.ring {
  position: absolute;
  inset: -4px;
  border-radius: 50%;
  border: 1px solid var(--nt-accent-border);
  opacity: 0;
}

.ring.ping {
  animation: nt-pulse-ring 0.9s ease-out 1;
}

.inactive .dot {
  background: var(--nt-text-3);
}
</style>
