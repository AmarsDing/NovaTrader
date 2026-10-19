<script setup lang="ts">
/**
 * 工作台（`/` 默认路由）：顶栏 chrome + dockview 面板区 + 底栏 chrome。
 * 深链 `/panel/:id` 复用本视图：挂载/路由变化时开面板，然后把地址剥回 `/`。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ConfirmSheet from '@/widgets/ConfirmSheet.vue'
import { useAuthStore } from '@/stores/auth'
import { useSettingsStore } from '@/stores/settings'
import { VISUAL_DOCUMENT_EVENT } from '@/theme/visual-quality'
import { clock } from '@/utils/format'
import { isLive, link } from '@/ws/hub'
import CommandBar from './CommandBar.vue'
import CommandPalette from './CommandPalette.vue'
import FunctionIndexOverlay from './FunctionIndexOverlay.vue'
import StatusBar from './StatusBar.vue'
import DockHost from './DockHost.vue'
import AlertCenter from './AlertCenter.vue'
import ConfigCenter from '@/view/ConfigCenter.vue'
import NtToast from '@/ui/NtToast.vue'
import { startWatchdog } from './connection-watchdog'
import { getWorkbench } from './inject'
import { isEditable, startHotkeys } from './shortcuts'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const settings = useSettingsStore()

const dock = ref<InstanceType<typeof DockHost> | null>(null)
const palette = ref<InstanceType<typeof CommandPalette> | null>(null)
const help = ref<InstanceType<typeof FunctionIndexOverlay> | null>(null)
const configOpen = ref(false)

const banner = computed(() => {
  if (isLive()) return ''
  const local = auth.user?.local ? ' · 本地会话' : ''
  if (link.health === 'down') return `admin 不可达 · 不是实时${local}`
  if (link.ws === 'idle' || link.ws === 'connecting') return `正在连接实时通道${local}`
  if (link.ws === 'open' && link.sync === 'syncing') return `推送已连接 · 正在核对权威状态 · 不是实时${local}`
  if (link.ws === 'open' && link.sync === 'required') return `待恢复 · ${link.error || '权威状态尚未核对'} · 不是实时${local}`
  if (link.lastAt) return `截至 ${clock(link.lastAt)} · 不是实时${local}`
  return `实时通道中断 · 正在重连${local}`
})

let stopWatchdog: (() => void) | undefined
let stopHotkeys: (() => void) | undefined
let stopPopoutAppearance: (() => void) | undefined

function openConfig() {
  configOpen.value = true
}

function onKeydown(event: KeyboardEvent) {
  // Esc 先由 useOverlay 栈消费（栈非空即 preventDefault）——这里只收拾未消费时的 config 层，一按一层。
  if (event.key === 'Escape' && configOpen.value && !event.defaultPrevented) {
    configOpen.value = false
    return
  }
  if (!event.ctrlKey && !event.metaKey) return
  const key = event.key.toLowerCase()
  if (key === 'k' && event.shiftKey) {
    event.preventDefault()
    palette.value?.open('command')
  } else if (key === 'k') {
    event.preventDefault()
    palette.value?.open('panel')
  } else if (key === 'p' && event.shiftKey) {
    event.preventDefault()
    palette.value?.open('command')
  } else if (key === 'z') {
    // 布局撤销/重做（G6）：输入态让位给文本框原生撤销。
    if (isEditable(event.target)) return
    event.preventDefault()
    if (event.shiftKey) getWorkbench()?.redoLayout()
    else getWorkbench()?.undoLayout()
  }
}

function consumeDeepLink() {
  const id = route.params.id
  if (typeof id === 'string' && id) {
    const params: Record<string, unknown> = {}
    Object.entries(route.query).forEach(([key, value]) => {
      if (typeof value === 'string') params[key] = value
    })
    dock.value?.openPanel(id, params)
  }
  if (route.path !== '/') void router.replace({ path: '/' })
}

onMounted(() => {
  const api = dock.value?.getApi()
  if (api) {
    const attached = new Map<string, () => void>()
    const attach = (item: { id: string; window: Window }) => {
      attached.get(item.id)?.()
      attached.set(item.id, settings.attachVisualDocument(item.window.document))
      queueMicrotask(() => document.dispatchEvent(new Event(VISUAL_DOCUMENT_EVENT)))
    }
    const detach = (item: { id: string }) => {
      attached.get(item.id)?.()
      attached.delete(item.id)
      queueMicrotask(() => document.dispatchEvent(new Event(VISUAL_DOCUMENT_EVENT)))
    }
    const added = api.onDidAddPopoutGroup(attach)
    const removed = api.onDidRemovePopoutGroup(detach)
    const closing = api.onWillClosePopoutWindow(detach)
    api.getPopouts().forEach(attach)
    stopPopoutAppearance = () => {
      added.dispose()
      removed.dispose()
      closing.dispose()
      for (const release of attached.values()) release()
      attached.clear()
    }
  }
  stopWatchdog = startWatchdog()
  stopHotkeys = startHotkeys({
    dock: () => dock.value?.getApi() ?? null,
    openKillSwitch: () => window.dispatchEvent(new CustomEvent('nt:kill-switch')),
    openHelp: () => help.value?.open(),
  })
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('nt:open-config', openConfig)
  consumeDeepLink()
})

onBeforeUnmount(() => {
  stopWatchdog?.()
  stopHotkeys?.()
  stopPopoutAppearance?.()
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('nt:open-config', openConfig)
})

watch(() => route.fullPath, consumeDeepLink)
</script>

<template>
  <div class="shell">
    <CommandBar @palette="palette?.open('panel')" @config="openConfig" />
    <p v-if="banner" class="stale">{{ banner }}</p>
    <DockHost ref="dock" />
    <StatusBar />
    <ConfirmSheet />
    <CommandPalette ref="palette" />
    <FunctionIndexOverlay ref="help" />
    <div v-if="configOpen" class="config-mask" @mousedown.self="configOpen = false">
      <ConfigCenter overlay @close="configOpen = false" />
    </div>
    <AlertCenter />
    <NtToast />
  </div>
</template>

<style scoped>
.shell {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--nt-space-bg);
}

.config-mask {
  position: fixed;
  inset: 0;
  z-index: 70;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: var(--nt-scrim);
}

.stale {
  margin: 8px 14px 0;
  padding: 6px 12px;
  border-radius: var(--nt-radius-s);
  color: color-mix(in srgb, var(--nt-warn) 80%, var(--nt-text-1));
  background: color-mix(in srgb, var(--nt-warn) 9%, var(--nt-bg-1));
  border: 1px solid color-mix(in srgb, var(--nt-warn) 45%, var(--nt-border-1));
  font-size: 12.5px;
  box-shadow: inset 3px 0 var(--nt-warn);
}
</style>
