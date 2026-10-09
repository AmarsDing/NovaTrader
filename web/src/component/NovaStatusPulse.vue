<script setup>
defineProps({
  active: { type: Boolean, default: true },
})
</script>

<template>
  <span class="pulse" :class="{ inactive: !active }" aria-hidden="true">
    <span class="dot" />
    <span class="ring" />
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
  background: radial-gradient(circle at 30% 30%, #7dfff8, var(--nt-accent));
  animation: nt-breathe 2.4s ease-in-out infinite;
  box-shadow: 0 0 10px rgba(0, 229, 255, 0.7);
}

.ring {
  position: absolute;
  inset: -4px;
  border-radius: 50%;
  border: 1px solid rgba(0, 229, 255, 0.45);
  animation: nt-pulse-ring 2.4s ease-out infinite;
}

.inactive .dot {
  animation: none;
  background: var(--nt-text-3);
  box-shadow: none;
}

.inactive .ring {
  animation: none;
  opacity: 0.3;
}
</style>
