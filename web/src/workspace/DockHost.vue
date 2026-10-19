<script setup lang="ts">
/**
 * 工作台宿主：dockview 实例 + 布局持久化 + 面板打开契约。
 * - 主题走 theme/dockview-theme.css（.dockview-theme-nt 把 --dv-* 映射到 --nt-*）；
 * - 布局：onDidLayoutChange 防抖 500ms → localStorage（stores/layout，按账号分键）；
 * - 无存储 / 存储损坏 → 默认预设；浮窗开启，popout 关（P5 再开能力）。
 */
import { getCurrentInstance, onBeforeUnmount, onMounted, ref } from 'vue'
import { createDockview, type DockviewApi, type DockviewTheme } from 'dockview-core'
import { useAuthStore } from '@/stores/auth'
import { useLayoutStore } from '@/stores/layout'
import { registerWorkbench, unregisterWorkbench, type WorkbenchApi } from './inject'
import { stagePanelParams } from './panel-bus'
import { getPanel, listPanels, canPopout } from './panel-registry'
import { DESKS, getDesk, type DeskPanelSpec } from './desks'
import { recordLayout, undoLayout, redoLayout } from './layout-history'
import { createVueRenderers } from './vue-renderer'
import { buildPanelTabMenu } from './panel-tab-menu'

const SAVE_DEBOUNCE_MS = 500

/** 盘中全览：中决策台，右涨停/行情/信号/情报同组，单券侧栏独立一列，下交易＋下单条。布局版本升级后旧布局作废。 */
const DEFAULT_LAYOUT: DeskPanelSpec[] = [
  { id: 'overview' },
  { id: 'limit-ladder', ref: 'overview', direction: 'right' },
  { id: 'market', ref: 'limit-ladder', direction: 'within' },
  { id: 'signals', ref: 'limit-ladder', direction: 'within' },
  { id: 'intel', ref: 'limit-ladder', direction: 'within' },
  { id: 'quote-sidebar', ref: 'limit-ladder', direction: 'right' },
  { id: 'trade', ref: 'overview', direction: 'below' },
  { id: 'trade-bar', ref: 'trade', direction: 'within' },
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

/** 按声明顺序铺面板（默认屏与桌面包共用同一构建管线；ref 必须先于依赖者出现）。 */
function buildPanels(dock: DockviewApi, specs: DeskPanelSpec[]) {
  for (const spec of specs) {
    const def = getPanel(spec.id)
    if (!def) continue
    if (spec.params) stagePanelParams(spec.id, spec.params)
    dock.addPanel({
      id: def.id,
      component: def.id,
      title: def.title,
      position: spec.ref ? { referencePanel: spec.ref, direction: spec.direction ?? 'within' } : undefined,
    })
  }
}

function buildDefault(dock: DockviewApi) {
  buildPanels(dock, DEFAULT_LAYOUT)
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

/** 丢弃存储布局里已下线/未知的面板，避免开出一堆「未知面板」。 */
function pruneUnknownPanels() {
  if (!api) return
  for (const panel of [...api.panels]) {
    if (!getPanel(panel.id)) panel.api.close()
  }
}

function applyLayout(next: unknown, opts: { persist?: boolean; record?: boolean } = {}) {
  if (!api) return
  // 程序化整版切换前先留快照（G6 撤销）；启动恢复等内部套用显式 record:false。
  if (opts.record !== false) recordLayout(api.toJSON())
  applying = true
  try {
    api.fromJSON(next as never)
  } catch {
    api.clear()
    buildDefault(api)
  }
  pruneUnknownPanels()
  applying = false
  if (!api.panels.length) buildDefault(api)
  if (opts.persist !== false) persist()
}

function resetToDefault() {
  if (!api) return
  recordLayout(api.toJSON())
  api.clear()
  buildDefault(api)
  persist()
}

/** G6 桌面包：记录快照 → 清空 → 按声明重建（参数走 panel-bus）→ 聚焦并持久化。切桌销毁浮窗。 */
function applyDesk(id: string): boolean {
  const desk = getDesk(id)
  if (!api || !desk) return false
  recordLayout(api.toJSON())
  applying = true
  api.clear()
  buildPanels(api, desk.panels)
  applying = false
  if (!api.panels.length) buildDefault(api)
  api.getPanel(desk.focus)?.api.setActive()
  persist()
  return true
}

function undoLast(): boolean {
  if (!api) return false
  const prev = undoLayout(api.toJSON())
  if (prev == null) return false
  applyLayout(prev, { record: false })
  return true
}

function redoNext(): boolean {
  if (!api) return false
  const next = redoLayout(api.toJSON())
  if (next == null) return false
  applyLayout(next, { record: false })
  return true
}

const workbench: WorkbenchApi = {
  openPanel,
  captureLayout: () => api?.toJSON() ?? null,
  applyLayout,
  resetToDefault,
  applyDesk,
  undoLayout: undoLast,
  redoLayout: redoNext,
  popoutPanel: (id: string) => void popoutPanel(id),
  activePanelId: () => api?.activePanel?.id ?? undefined,
  panels: listPanels(),
  desks: DESKS,
}

/** 把面板弹成独立窗口（dockview popout）；不允许的面板静默拒绝。 */
async function popoutPanel(id: string): Promise<boolean> {
  if (!api || !canPopout(id)) return false
  const panel = api.getPanel(id)
  if (!panel) return false
  try {
    return await api.addPopoutGroup(panel)
  } catch {
    return false
  }
}

// 模块级注册：须在 setup 同步完成，供同页先挂载的 CommandBar 等懒取。
registerWorkbench(workbench)
defineExpose({
  openPanel,
  applyLayout,
  resetToDefault,
  captureLayout: workbench.captureLayout,
  /** 热键等宿主逻辑用：拿 dockview 实例（可能还没建好）。 */
  getApi: () => api,
})

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
    // G2 面板菜单：tab 右键/长按菜单项纯层（功能卡 F1 / 弹出（白名单把关）/ 关闭）；渲染与 Esc 由 dockview 自营。
    getTabContextMenuItems: buildPanelTabMenu,
    disableFloatingGroups: false,
    popoutUrl: '/popout.html',
  })

  const stored = layout.load(accountKey())
  if (stored) applyLayout(stored, { persist: false, record: false })
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
  unregisterWorkbench(workbench)
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
