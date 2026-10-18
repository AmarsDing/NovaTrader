<script setup lang="ts">
/** 自定义标签页：标题 + 链路状态点 + 弹窗（白名单内）+ 关闭。关闭走 dockview panel api。 */
import { computed } from 'vue'
import type { DockviewPanelApi } from 'dockview-core'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import { isLive, link } from '@/ws/hub'
import { canPopout } from './panel-registry'
import { getWorkbench } from './inject'

const props = defineProps<{
  title: string
  name: string
  api: DockviewPanelApi
}>()

const status = computed<'ok' | 'stale' | 'down' | 'off'>(() => {
  if (isLive()) return 'ok'
  if (link.ws === 'open' || link.ws === 'connecting' || link.lastAt) return 'stale'
  return link.health === 'down' ? 'down' : 'off'
})

const poppable = computed(() => canPopout(props.name))

function popout() {
  getWorkbench()?.popoutPanel(props.name)
}

function close() {
  props.api.close()
}
</script>

<template>
  <span class="nt-tab">
    <NtStatusDot :status="status" />
    <span class="nt-tab__title">{{ title }}</span>
    <button v-if="poppable" type="button" class="nt-tab__pop" title="弹出为独立窗口" @click.stop="popout">⧉</button>
    <button type="button" class="nt-tab__close" title="关闭面板" @click.stop="close">×</button>
  </span>
</template>

<style scoped>
.nt-tab__pop,
.nt-tab__close {
  border: none;
  background: transparent;
  color: var(--nt-text-3);
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
  padding: 2px 3px;
  border-radius: var(--nt-radius-s);
}

.nt-tab__pop:hover,
.nt-tab__close:hover {
  color: var(--nt-accent);
  background: var(--nt-accent-soft);
}
</style>
