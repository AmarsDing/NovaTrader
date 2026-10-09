<script setup lang="ts">
import { computed } from 'vue'
import { ICONS, type IconName } from './icons'

const props = withDefaults(defineProps<{ name: IconName; size?: number | string }>(), { size: 16 })

const path = computed(() => ICONS[props.name] || '')
</script>

<template>
  <svg
    class="nt-icon"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    v-html="path"
  />
</template>

<style scoped>
.nt-icon {
  display: inline-block;
  flex: none;
  vertical-align: -0.15em;
}
</style>
