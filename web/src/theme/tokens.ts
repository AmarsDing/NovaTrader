/** Script-side colors come from tokens.css; charts share one live theme observer. */
import { onScopeDispose, shallowRef, type ShallowRef } from 'vue'

const TOKEN_NAMES = {
  bg0: '--nt-bg-0',
  bg1: '--nt-bg-1',
  bg2: '--nt-bg-2',
  bg3: '--nt-bg-3',
  border1: '--nt-border-1',
  border2: '--nt-border-2',
  text0: '--nt-text-0',
  text1: '--nt-text-1',
  text2: '--nt-text-2',
  text3: '--nt-text-3',
  accent: '--nt-accent',
  accent2: '--nt-accent-2',
  warn: '--nt-warn',
  danger: '--nt-danger',
  success: '--nt-success',
  info: '--nt-info',
  up: '--nt-up',
  down: '--nt-down',
} as const

export type Palette = { readonly [K in keyof typeof TOKEN_NAMES]: string }

function readPalette(): Palette {
  const style = typeof document === 'undefined' ? null : getComputedStyle(document.documentElement)
  return Object.fromEntries(
    Object.entries(TOKEN_NAMES).map(([key, token]) => [
      key,
      // Only neutral fallbacks for SSR/unloaded styles; no second theme color table.
      style?.getPropertyValue(token).trim() || (key.startsWith('bg') || key.startsWith('border') ? 'transparent' : '#808080'),
    ]),
  ) as Palette
}

const currentPalette = shallowRef<Palette>(readPalette())
let subscribers = 0
let observer: MutationObserver | null = null

function refreshPalette() {
  const next = readPalette()
  if (Object.keys(TOKEN_NAMES).some((key) => next[key as keyof Palette] !== currentPalette.value[key as keyof Palette])) {
    currentPalette.value = next
  }
}

/** Call in setup. The last unmounted chart releases the shared DOM observer. */
export function useChartPalette(): Readonly<ShallowRef<Palette>> {
  refreshPalette()
  subscribers += 1
  if (!observer && typeof document !== 'undefined') {
    observer = new MutationObserver(refreshPalette)
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style', 'class'] })
  }
  onScopeDispose(() => {
    subscribers -= 1
    if (subscribers === 0) {
      observer?.disconnect()
      observer = null
    }
  })
  return currentPalette
}

/** Compatible live getters for canvas/marker consumers; never cache a second palette. */
export const PALETTE = Object.defineProperties(
  {},
  Object.fromEntries(
    Object.keys(TOKEN_NAMES).map((key) => [key, {
      enumerable: true,
      get: () => {
        // Track chart theme changes inside Vue computed consumers as well.
        const value = currentPalette.value[key as keyof Palette]
        if (typeof document === 'undefined') return value
        return getComputedStyle(document.documentElement).getPropertyValue(TOKEN_NAMES[key as keyof Palette]).trim() || value
      },
    }]),
  ),
) as Palette

/** Canvas libraries require actual colors, so never append hex alpha to an rgb token. */
export function colorWithAlpha(color: string, alpha: number): string {
  const opacity = Math.max(0, Math.min(1, alpha))
  const hex = /^#([\da-f]{3}|[\da-f]{4}|[\da-f]{6}|[\da-f]{8})$/i.exec(color)
  if (hex) {
    const value = hex[1].length <= 4 ? [...hex[1]].map((digit) => digit + digit).join('') : hex[1]
    const [r, g, b] = [0, 2, 4].map((index) => parseInt(value.slice(index, index + 2), 16))
    const sourceAlpha = value.length === 8 ? parseInt(value.slice(6, 8), 16) / 255 : 1
    return `rgba(${r}, ${g}, ${b}, ${opacity * sourceAlpha})`
  }
  const rgb = /^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:\s*[,/]\s*([\d.]+))?\s*\)$/i.exec(color)
  if (rgb) return `rgba(${rgb[1]}, ${rgb[2]}, ${rgb[3]}, ${opacity * Number(rgb[4] ?? 1)})`
  return color
}

export const CHART_TEXT_STYLE = {
  get color() { return PALETTE.text2 },
  fontFamily: "'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif",
  fontSize: 11,
} as const
