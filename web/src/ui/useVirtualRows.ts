/**
 * useVirtualRows：固定行高虚拟滚动窗口计算（NtTable v2 配套，借法 FreqUI/Perspective
 * 「只渲染可视窗口」的专业表格范式；自研实现，不引第三方库——AGENTS.md §6 纪律）。
 *
 * 纯计算部分（windowRange）与框架解耦可单测；响应式部分负责 scroll/resize 监听。
 */
import { computed, onBeforeUnmount, ref, watch, type ComputedRef, type Ref } from 'vue'

export interface VirtualWindow {
  /** 首个渲染行（含 overscan）。 */
  start: number
  /** 末尾渲染行（不含，含 overscan）。 */
  end: number
  /** 上撑高 px（= start × rowHeight）。 */
  padTop: number
  /** 下撑高 px（= (total − end) × rowHeight）。 */
  padBottom: number
}

/** 可视窗口纯计算：scrollTop/viewportH → [start, end)。行高 ≤0 或总行数 ≤0 时退化为空窗。 */
export function windowRange(
  scrollTop: number,
  viewportH: number,
  total: number,
  rowHeight: number,
  overscan = 6,
): { start: number; end: number } {
  if (total <= 0 || rowHeight <= 0) return { start: 0, end: 0 }
  // 越界滚动（数据骤减后残留的大 scrollTop）收拢到末页窗口，不允许空渲染。
  const maxScroll = Math.max(0, total * rowHeight - viewportH)
  const st = Math.min(Math.max(0, scrollTop), maxScroll)
  const first = Math.max(0, Math.floor(st / rowHeight) - overscan)
  const last = Math.min(total, Math.ceil((st + viewportH) / rowHeight) + overscan)
  return { start: Math.min(first, last), end: last }
}

/**
 * 响应式虚拟窗口。viewportRef 是滚动容器；rowHeight 为 px（消费方负责与真实行高一致，
 * NtTable 会在未显式传入时实测首行兜底）。非 virtual 表格不应调用本函数。
 */
export function useVirtualRows(opts: {
  /** 只有 enabled 时才挂监听；非虚拟表格零开销（enabled 恒 false）。 */
  enabled: Ref<boolean>
  total: Ref<number>
  rowHeight: Ref<number>
  viewportRef: Ref<HTMLElement | null>
  overscan?: number
}): {
  window: ComputedRef<VirtualWindow>
  onScroll: (e: Event) => void
  viewportH: Ref<number>
} {
  const overscan = opts.overscan ?? 6
  const scrollTop = ref(0)
  const viewportH = ref(0)
  let ro: ResizeObserver | null = null

  function measure() {
    viewportH.value = opts.viewportRef.value?.clientHeight ?? 0
  }
  function onScroll(e: Event) {
    scrollTop.value = (e.target as HTMLElement).scrollTop
  }
  function teardown() {
    ro?.disconnect()
    ro = null
  }

  // enabled 翻正后等 DOM 落位再量；容器可能晚于 enabled 挂载，故同时盯 ref。
  watch(
    [opts.enabled, opts.viewportRef],
    ([on, el]) => {
      teardown()
      if (!on || !el) return
      measure()
      if (typeof ResizeObserver !== 'undefined') {
        ro = new ResizeObserver(measure)
        ro.observe(el)
      }
    },
    { immediate: true, flush: 'post' },
  )
  onBeforeUnmount(teardown)

  const window = computed<VirtualWindow>(() => {
    const { start, end } = windowRange(scrollTop.value, viewportH.value, opts.total.value, opts.rowHeight.value, overscan)
    return {
      start,
      end,
      padTop: start * opts.rowHeight.value,
      padBottom: (opts.total.value - end) * opts.rowHeight.value,
    }
  })

  return { window, onScroll, viewportH }
}
