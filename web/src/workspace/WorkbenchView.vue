<script setup lang="ts">
/**
 * 工作台（`/` 默认路由）：顶栏 chrome + dockview 面板区 + 底栏 chrome。
 * 深链 `/panel/:id` 复用本视图：挂载/路由变化时开面板，然后把地址剥回 `/`。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ConfirmSheet from '@/widgets/ConfirmSheet.vue'
import { useAuthStore } from '@/stores/auth'
import { clock } from '@/utils/format'
import { isLive, link } from '@/ws/hub'
import CommandBar from './CommandBar.vue'
import CommandPalette from './CommandPalette.vue'
import StatusBar from './StatusBar.vue'
import DockHost from './DockHost.vue'
import AlertCenter from './AlertCenter.vue'
import ConfigCenter from '@/view/ConfigCenter.vue'
import NtToast from '@/ui/NtToast.vue'
import { startWatchdog } from './connection-watchdog'
import { startHotkeys } from './shortcuts'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const dock = ref<InstanceType<typeof DockHost> | null>(null)
const palette = ref<InstanceType<typeof CommandPalette> | null>(null)
const configOpen = ref(false)

const banner = computed(() => {
  if (isLive()) return ''
  const local = auth.user?.local ? ' · 本地会话' : ''
  if (link.ws === 'idle' || link.ws === 'connecting') return `正在连接实时通道${local}`
  if (link.health === 'down') return `admin 不可达 · 不是实时${local}`
  if (link.lastAt) return `截至 ${clock(link.lastAt)} · 不是实时${local}`
  return `实时通道中断 · 正在重连${local}`
})

let stopWatchdog: (() => void) | undefined
let stopHotkeys: (() => void) | undefined

function openConfig() {
  configOpen.value = true
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && configOpen.value) {
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
  stopWatchdog = startWatchdog()
  stopHotkeys = startHotkeys({
    dock: () => dock.value?.getApi() ?? null,
    openKillSwitch: () => window.dispatchEvent(new CustomEvent('nt:kill-switch')),
  })
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('nt:open-config', openConfig)
  consumeDeepLink()
})

onBeforeUnmount(() => {
  stopWatchdog?.()
  stopHotkeys?.()
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
  background: var(--nt-bg-0);
}

.config-mask {
  position: fixed;
  inset: 0;
  z-index: 70;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(0, 0, 0, 0.55);
}

.stale {
  margin: 8px 14px 0;
  padding: 6px 12px;
  border-radius: var(--nt-radius-s);
  color: #ffd7b0;
  background: rgba(255, 107, 0, 0.14);
  border: 1px solid rgba(255, 107, 0, 0.45);
  font-size: 12.5px;
}
</style>
