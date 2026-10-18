/** Display quality only controls rendering; it never changes data subscriptions or execution permissions. */
import { onMounted, onScopeDispose, shallowRef, type Ref } from 'vue'

export type VisualQuality = 'full' | 'eco' | 'reduced'
export const VISUAL_DOCUMENT_EVENT = 'nt:visual-documents-changed'

export function visualQualityOf(value: unknown): VisualQuality {
  return value === 'eco' || value === 'reduced' ? value : 'full'
}

export function reduceVisualMotion(quality: VisualQuality, systemReduced: boolean): boolean {
  return systemReduced || quality !== 'full'
}

/** Chart DOM can migrate to a dockview popout; observe the document that currently owns it. */
export function useVisualRendering(host: Ref<HTMLElement | null>) {
  const state = shallowRef({ reducedMotion: false, visible: true })
  let owner: Document | null = null
  let observer: MutationObserver | null = null

  function sync() {
    const nextOwner = host.value?.ownerDocument ?? document
    if (owner !== nextOwner) {
      owner?.removeEventListener('visibilitychange', sync)
      owner = nextOwner
      owner.addEventListener('visibilitychange', sync)
      observer?.disconnect()
      observer?.observe(document.documentElement, {
        attributes: true, attributeFilter: ['data-motion', 'data-visual-quality', 'data-page-visibility'],
      })
      if (owner !== document) observer?.observe(owner.documentElement, {
        attributes: true, attributeFilter: ['data-motion', 'data-visual-quality', 'data-page-visibility'],
      })
    }
    const root = owner.documentElement
    const next = {
      reducedMotion: root.dataset.motion === 'reduced' || visualQualityOf(root.dataset.visualQuality) !== 'full' ||
        owner.defaultView?.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true,
      visible: owner.visibilityState !== 'hidden' && root.dataset.pageVisibility !== 'hidden',
    }
    if (next.reducedMotion !== state.value.reducedMotion || next.visible !== state.value.visible) state.value = next
  }

  onMounted(() => {
    observer = new MutationObserver(sync)
    document.addEventListener(VISUAL_DOCUMENT_EVENT, sync)
    sync()
  })
  onScopeDispose(() => {
    observer?.disconnect()
    owner?.removeEventListener('visibilitychange', sync)
    document.removeEventListener(VISUAL_DOCUMENT_EVENT, sync)
  })

  return state
}

/** One pending frame at most. Hidden/paused surfaces have no RAF chain; static modes repaint on invalidation. */
export function createVisualFrameLoop(options: {
  draw: () => void
  canRender: () => boolean
  animate: () => boolean
  request: (callback: FrameRequestCallback) => number
  cancel: (id: number) => void
}) {
  let frame: number | null = null
  let disposed = false

  function tick() {
    frame = null
    if (disposed || !options.canRender()) return
    options.draw()
    if (options.animate()) invalidate()
  }

  function invalidate() {
    if (!disposed && frame === null && options.canRender()) frame = options.request(tick)
  }

  function refresh() {
    if (frame !== null && !options.canRender()) {
      options.cancel(frame)
      frame = null
    }
    invalidate()
  }

  function dispose() {
    disposed = true
    if (frame !== null) options.cancel(frame)
    frame = null
  }

  return { invalidate, refresh, dispose }
}
