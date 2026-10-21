<script setup lang="ts">
/**
 * 板块角标（U4，display 层）：创业板/科创板/北交所三档小标签。
 * 文案与 tone 唯一出自 status-map 册（board.cyb/kcb/bj 三键），板块判定唯一出自
 * utils/limit.boardTag（与 limitRatio 同前缀口径）；主板判定 null → 不渲染
 * （主板是默认态，角标只标记非常规档，防视觉噪声）。
 */
import { computed } from 'vue'
import NtBadge from '@/ui/NtBadge.vue'
import { statusOf } from '@/ui/status-map'
import { boardTag } from '@/utils/limit'

const props = defineProps<{ code: string }>()

const entry = computed(() => {
  const key = boardTag(props.code)
  return key ? statusOf(key) : null
})
</script>

<template>
  <NtBadge v-if="entry" :tone="entry.tone">{{ entry.text }}</NtBadge>
</template>
