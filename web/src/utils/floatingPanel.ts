/**
 * 浮窗几何纯函数（R-UI-15）：夹紧视口 + 步长吸附。
 * 无 DOM 依赖，面板/浮窗组件统一引用，禁在组件内手写同款算术。
 */

export interface PanelRect {
  x: number
  y: number
  width: number
  height: number
}

export interface Viewport {
  width: number
  height: number
}

export function clamp(value: number, min: number, max: number): number {
  if (max < min) return min
  return Math.min(max, Math.max(min, value))
}

/** 按步长吸附（默认 22px，对齐终端栅格）；step<=0 时原样返回。 */
export function snap(value: number, step = 22): number {
  if (step <= 0) return value
  return Math.round(value / step) * step
}

/**
 * 把面板矩形夹紧到视口内，四周保留 inset。
 * 面板大过视口时贴左上（可滚动定位交给容器），坐标来自返回值，不回写原对象。
 */
export function clampToViewport(rect: PanelRect, viewport: Viewport, inset = 10): PanelRect {
  const maxX = Math.max(inset, viewport.width - rect.width - inset)
  const maxY = Math.max(inset, viewport.height - rect.height - inset)
  return {
    ...rect,
    x: clamp(rect.x, inset, maxX),
    y: clamp(rect.y, inset, maxY),
  }
}
