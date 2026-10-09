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
import { startWatchdog } from './connection-watchdog'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const dock = ref<InstanceType<typeof DockHost> | null>(null)
const palette = ref<InstanceType<typeof CommandPalette> | null>(null)

const banner = computed(() => {
  if (isLive()) return ''
  const local = auth.user?.local ? ' · 本地会话' : ''
  if (link.lastAt) return `截至 ${clock(link.lastAt)} · 不是实时${local}`
  return `未连接 · 不是实时${local}`
})

let stopWatchdog: (() => void) | undefined

function onKeydown(event: KeyboardEvent) {
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
  window.addEventListener('keydown', onKeydown)
  consumeDeepLink()
})

onBeforeUnmount(() => {
  stopWatchdog?.()
  window.removeEventListener('keydown', onKeydown)
})

watch(() => route.fullPath, consumeDeepLink)
</script>

<template>
  <div class="shell">
    <CommandBar @palette="palette?.open('panel')" />
    <p v-if="banner" class="stale">{{ banner }}</p>
    <DockHost ref="dock" />
    <StatusBar />
    <ConfirmSheet />
    <CommandPalette ref="palette" />
  </div>
</template>

<style scoped>
.shell {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--nt-bg-0);
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
