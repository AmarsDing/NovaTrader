/**
 * Vue ↔ dockview 桥：把 Vue 组件包装成 dockview 的 IContentRenderer / ITabRenderer /
 * IWatermarkRenderer（有意不用 dockview-vue，自己控 renderer 与主题）。
 * - 每个面板/标签页/水印各自挂一个独立 Vue app，并继承 DockHost 的 appContext，
 *   因此面板内 pinia / router / provide 与整机一致；
 * - dispose() 反挂载，面板关掉即释放 watcher 与 ECharts 实例。
 */
import { createApp, defineAsyncComponent, h, type App, type AppContext, type Component } from 'vue'
import type {
  GroupPanelPartInitParameters,
  IContentRenderer,
  ITabRenderer,
  IWatermarkRenderer,
  TabPartInitParameters,
  WatermarkRendererInitParameters,
} from 'dockview-core'
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

/** 单个 Vue 子应用的生命周期包装。 */
class VueMount {
  readonly element: HTMLElement
  private app: App | null = null

  constructor(element: HTMLElement) {
    this.element = element
  }

  mount(ctx: AppContext, root: Component, props: Record<string, unknown>) {
    const app = createApp({ render: () => h(root, props) })
    // 继承宿主上下文：全局组件、插件、provide/inject 与主应用一致。
    ;(app as unknown as { _context: AppContext })._context = ctx
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
    private name: string,
  ) {}

  init(params: TabPartInitParameters): void {
    this.host.mount(this.ctx, PanelTab, { title: params.title, name: this.name, api: params.api })
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
    createTabComponent: (options) => new VueTabRenderer(ctx, options.name),
    createWatermarkComponent: () => new VueWatermarkRenderer(ctx),
  }
}
