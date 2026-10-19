/**
 * Vue ↔ dockview 桥：把 Vue 组件包装成 dockview 的 IContentRenderer / ITabRenderer /
 * IWatermarkRenderer（有意不用 dockview-vue，自己控 renderer 与主题）。
 * - 每个面板/标签页/水印各自挂一个独立 Vue app；
 * - 共享宿主 provides（pinia 等），并显式桥接当前路由（面板不在 RouterView 子树内，
 *   直接复用宿主 _context 时 useRoute() 会拿不到 Symbol(route location)）。
 */
import { createApp, defineAsyncComponent, h, reactive, watch, type App, type AppContext, type Component } from 'vue'
import { routeLocationKey, routerKey } from 'vue-router'
import type {
  GroupPanelPartInitParameters,
  IContentRenderer,
  ITabRenderer,
  IWatermarkRenderer,
  TabPartInitParameters,
  WatermarkRendererInitParameters,
} from 'dockview-core'
import { router } from '@/router'
import { getPanel } from './panel-registry'
import PanelTab from './PanelTab.vue'
import DockWatermark from './DockWatermark.vue'

const asyncCache = new Map<string, Component>()

function componentFor(name: string): Component | null {
  const cached = asyncCache.get(name)
  if (cached) return cached
  const def = getPanel(name)
  if (!def) return null
  const comp = defineAsyncComponent(def.loader)
  asyncCache.set(name, comp)
  return comp
}

/** 给面板用的可变路由代理，属性始终跟 router.currentRoute 对齐。 */
function bridgeRoute() {
  const state = reactive({ ...router.currentRoute.value })
  watch(
    router.currentRoute,
    (to) => {
      Object.assign(state, to)
    },
    { flush: 'sync' },
  )
  return state
}

/** 单个 Vue 子应用的生命周期包装。 */
class VueMount {
  readonly element: HTMLElement
  private app: App | null = null

  constructor(element: HTMLElement) {
    this.element = element
  }

  mount(ctx: AppContext, root: Component, props: Record<string, unknown>) {
    const app = createApp({ render: () => h(root, props) })
    const dest = app._context as AppContext & {
      provides: object
      components: AppContext['components']
      directives: AppContext['directives']
      mixins: AppContext['mixins']
    }
    const src = ctx as typeof dest
    // 原型链挂上宿主 provides，本地 provide 不污染主应用。
    dest.provides = Object.create(src.provides) as object
    dest.config = src.config
    dest.components = src.components
    dest.directives = src.directives
    dest.mixins = src.mixins
    app.provide(routerKey, router)
    app.provide(routeLocationKey, bridgeRoute())
    app.mount(this.element)
    this.app = app
  }

  unmount() {
    this.app?.unmount()
    this.app = null
  }
}

class VueContentRenderer implements IContentRenderer {
  readonly element = document.createElement('div')

  private host = new VueMount(this.element)

  constructor(
    private ctx: AppContext,
    private name: string,
  ) {
    this.element.className = 'nt-panel-host'
  }

  init(_params: GroupPanelPartInitParameters): void {
    const comp = componentFor(this.name)
    if (!comp) {
      this.element.textContent = `未知面板：${this.name}`
      return
    }
    this.host.mount(this.ctx, comp, {})
  }

  dispose(): void {
    this.host.unmount()
  }
}

class VueTabRenderer implements ITabRenderer {
  readonly element = document.createElement('div')

  private host = new VueMount(this.element)

  constructor(
    private ctx: AppContext,
    private panelId: string,
  ) {}

  init(params: TabPartInitParameters): void {
    this.host.mount(this.ctx, PanelTab, { title: params.title, panelId: this.panelId, api: params.api })
  }

  dispose(): void {
    this.host.unmount()
  }
}

class VueWatermarkRenderer implements IWatermarkRenderer {
  readonly element = document.createElement('div')

  private host = new VueMount(this.element)

  constructor(private ctx: AppContext) {}

  init(_params: WatermarkRendererInitParameters): void {
    this.host.mount(this.ctx, DockWatermark, {})
  }

  dispose(): void {
    this.host.unmount()
  }
}

export interface VueRenderers {
  createComponent: (options: { id: string; name: string }) => IContentRenderer
  createTabComponent: (options: { id: string; name: string }) => ITabRenderer | undefined
  createWatermarkComponent: () => IWatermarkRenderer
}

export function createVueRenderers(ctx: AppContext): VueRenderers {
  return {
    createComponent: (options) => new VueContentRenderer(ctx, options.name),
    // tab 的 options.name 是 tabComponent 名（'nt-tab'），面板 id 在 options.id（dockview 以 this.id 调用）；
    // 白名单/popout 判定必须取后者——传 name 会让 canPopout('nt-tab') 恒 false（⧉ 死按钮）。
    createTabComponent: (options) => new VueTabRenderer(ctx, options.id),
    createWatermarkComponent: () => new VueWatermarkRenderer(ctx),
  }
}
