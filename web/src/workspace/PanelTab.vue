<script setup lang="ts">
/** 自定义标签页：标题 + 链路状态点 + 关闭。关闭走 dockview panel api。 */
import { computed } from 'vue'
import type { DockviewPanelApi } from 'dockview-core'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import { isLive, link } from '@/ws/hub'

const props = defineProps<{
  title: string
  name: string
  api: DockviewPanelApi
}>()

const status = computed<'live' | 'stale' | 'down'>(() => {
  if (isLive()) return 'live'
  return link.lastAt ? 'stale' : 'down'
})

function close() {
  props.api.close()
}
</script>

<template>
  <span class="nt-tab">
    <NtStatusDot :status="status" />
    <span class="nt-tab__title">{{ title }}</span>
    <button type="button" class="nt-tab__close" title="关闭面板" @click.stop="close">×</button>
  </span>
</template>
