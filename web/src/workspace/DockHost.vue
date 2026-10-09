<script setup lang="ts">
/**
 * 工作台宿主：dockview 实例 + 布局持久化 + 面板打开契约。
 * - 主题走 theme/dockview-theme.css（.dockview-theme-nt 把 --dv-* 映射到 --nt-*）；
 * - 布局：onDidLayoutChange 防抖 500ms → localStorage（stores/layout，按账号分键）；
 * - 无存储 / 存储损坏 → 默认预设；浮窗开启，popout 关（P5 再开能力）。
 */
import { getCurrentInstance, onBeforeUnmount, onMounted, provide, ref } from 'vue'
import { createDockview, type DockviewApi, type DockviewTheme } from 'dockview-core'
import { useAuthStore } from '@/stores/auth'
import { useLayoutStore } from '@/stores/layout'
import { WorkbenchKey, type WorkbenchApi } from './inject'
import { stagePanelParams } from './panel-bus'
import { getPanel, listPanels } from './panel-registry'
import { createVueRenderers } from './vue-renderer'

const SAVE_DEBOUNCE_MS = 500

/** 默认预设：总览居中，行情右侧同组标签，交易在下。 */
const DEFAULT_LAYOUT: { id: string; ref?: string; direction?: 'right' | 'below' | 'within' }[] = [
  { id: 'overview' },
  { id: 'market', ref: 'overview', direction: 'right' },
  { id: 'signals', ref: 'market', direction: 'within' },
  { id: 'trade', ref: 'overview', direction: 'below' },
]

const ntTheme: DockviewTheme = {
  name: 'nt',
  className: 'dockview-theme-nt',
  colorScheme: 'dark',
}

const host = ref<HTMLElement | null>(null)
const auth = useAuthStore()
const layout = useLayoutStore()

let api: DockviewApi | null = null
let offLayoutChange: { dispose: () => void } | null = null
let saveTimer: ReturnType<typeof setTimeout> | null = null
/** 正在程序化套用布局：期间不写盘，避免中间态覆盖存储。 */
let applying = false

function accountKey(): string {
  return auth.user?.name || 'local'
}

function buildDefault(dock: DockviewApi) {
  for (const item of DEFAULT_LAYOUT) {
    const def = getPanel(item.id)
    if (!def) continue
    dock.addPanel({
      id: def.id,
      component: def.id,
      title: def.title,
      position: item.ref ? { referencePanel: item.ref, direction: item.direction ?? 'within' } : undefined,
    })
  }
}

function openPanel(id: string, params?: Record<string, unknown>) {
  const def = getPanel(id)
  if (!def || !api) return
  if (params) stagePanelParams(id, params)
  const existing = api.getPanel(id)
  if (existing) {
    existing.api.setActive()
    return
  }
  const reference = api.activePanel?.id ?? api.panels[api.panels.length - 1]?.id
  api.addPanel({
    id: def.id,
    component: def.id,
    title: def.title,
    position: reference ? { referencePanel: reference, direction: 'within' } : undefined,
  })
}

function persist() {
  if (!api) return
  layout.save(accountKey(), api.toJSON())
}

function scheduleSave() {
  if (applying) return
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = setTimeout(() => {
    saveTimer = null
    persist()
  }, SAVE_DEBOUNCE_MS)
}

function applyLayout(next: unknown, opts: { persist?: boolean } = {}) {
  if (!api) return
  applying = true
  try {
    api.fromJSON(next as never)
  } catch {
    api.clear()
    buildDefault(api)
  }
  applying = false
  if (!api.panels.length) buildDefault(api)
  if (opts.persist !== false) persist()
}

function resetToDefault() {
  if (!api) return
  api.clear()
  buildDefault(api)
  persist()
}

const workbench: WorkbenchApi = {
  openPanel,
  captureLayout: () => api?.toJSON() ?? null,
  applyLayout,
  resetToDefault,
  panels: listPanels(),
}

provide(WorkbenchKey, workbench)
defineExpose({ openPanel, applyLayout, resetToDefault, captureLayout: workbench.captureLayout })

onMounted(() => {
  if (!host.value) return
  const instance = getCurrentInstance()
  const renderers = createVueRenderers(instance!.appContext)

  api = createDockview(host.value, {
    className: 'nt-dock',
    theme: ntTheme,
    // 自定义标签页：dockview 只在 defaultTabComponent 有值时才走 createTabComponent。
    defaultTabComponent: 'nt-tab',
    createComponent: renderers.createComponent,
    createTabComponent: renderers.createTabComponent,
    createWatermarkComponent: renderers.createWatermarkComponent,
    disableFloatingGroups: false,
  })

  const stored = layout.load(accountKey())
  if (stored) applyLayout(stored, { persist: false })
  if (!api.panels.length) buildDefault(api)

  offLayoutChange = api.onDidLayoutChange(scheduleSave)
  layout.loadPresets()
})

onBeforeUnmount(() => {
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = null
  offLayoutChange?.dispose()
  offLayoutChange = null
  api?.dispose()
  api = null
})
</script>

<template>
  <div ref="host" class="dock-host" />
</template>

<style scoped>
.dock-host {
  position: relative;
  flex: 1;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
}
</style>
