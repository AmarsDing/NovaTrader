<script setup lang="ts">
/** 自定义标签页：标题 + 链路状态点 + 告警角标（§8.6）+ 弹窗（白名单内，按 panelId 判定）+ 关闭。关闭走 dockview panel api。 */
import { computed, onScopeDispose, ref, watch } from 'vue'
import type { DockviewPanelApi } from 'dockview-core'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import { useNotifyStore } from '@/stores/notify'
import { isLive, link } from '@/ws/hub'
import { canPopout } from './panel-registry'
import { getWorkbench } from './inject'

const props = defineProps<{
  title: string
  panelId: string
  api: DockviewPanelApi
}>()

const status = computed<'ok' | 'stale' | 'down' | 'off'>(() => {
  if (isLive()) return 'ok'
  if (link.ws === 'open' || link.ws === 'connecting' || link.lastAt) return 'stale'
  return link.health === 'down' ? 'down' : 'off'
})

const poppable = computed(() => canPopout(props.panelId))

const notify = useNotifyStore()
/** 本页签的未读告警角标（notify store attention，面板激活即清）。 */
const attention = computed(() => notify.attention[props.panelId])

/* dockview api.isActive 非 Vue 响应式，靠 onDidActiveChange 同步进 ref 再 watch。 */
const tabActive = ref(props.api.isActive)
const disposeActive = props.api.onDidActiveChange((e) => {
  tabActive.value = e.isActive
})
onScopeDispose(() => disposeActive.dispose())

/* 面板成为激活页签 → 已读语义，清掉它的角标。 */
watch(tabActive, (active) => {
  if (active) notify.clearAttention(props.panelId)
})

function popout() {
  getWorkbench()?.popoutPanel(props.panelId)
}

function close() {
  props.api.close()
}
</script>

<template>
  <span class="nt-tab">
    <NtStatusDot :status="status" />
    <span class="nt-tab__title">{{ title }}</span>
    <span v-if="attention" class="nt-tab__badge" :data-level="attention.level">×{{ attention.count }}</span>
    <button v-if="poppable" type="button" class="nt-tab__pop" title="弹出为独立窗口" @click.stop="popout">⧉</button>
    <button type="button" class="nt-tab__close" title="关闭面板" @click.stop="close">×</button>
  </span>
</template>

<style scoped>
.nt-tab__badge {
  display: inline-flex;
  align-items: center;
  margin-left: 2px;
  padding: 0 4px;
  border-radius: var(--nt-radius-s);
  border: 1px solid var(--nt-accent-border);
  background: var(--nt-accent-soft);
  color: var(--nt-accent);
  font-size: var(--nt-fs-micro);
  line-height: 1.5;
}

/* §8.6 L3 琥珀单次脉冲；L2 走上面的静态 accent 章。 */
.nt-tab__badge[data-level='L3'] {
  border-color: var(--nt-warn);
  color: var(--nt-warn);
  animation: nt-tab-badge-pulse 0.9s ease-out 1;
}

@keyframes nt-tab-badge-pulse {
  0% {
    transform: scale(1);
    box-shadow: 0 0 0 0 var(--nt-accent-border);
  }
  45% {
    transform: scale(1.18);
    box-shadow: 0 0 8px 1px var(--nt-accent-border);
  }
  100% {
    transform: scale(1);
    box-shadow: 0 0 0 0 transparent;
  }
}

/* 减少动态档：脉冲退化为静态章（全局 animation:none 之外的显式兜底，与 f10-hover 同约）。 */
:root[data-motion='reduced'] .nt-tab__badge[data-level='L3'] {
  animation: none;
}

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
