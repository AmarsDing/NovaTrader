/**
 * WCAG 2.x 对比度（G4 视觉工程化守护·对比度归一）。
 *
 * 与 color-vision.ts 分责：那边管「色盲下涨跌可辨」（OKLab 感知距离），
 * 这边管「文字/状态色在底板上读得出」（WCAG (L1+0.05)/(L2+0.05)）。
 * 审计只报不改色——低于 AA 的配对登记在 theme/contrast-baseline.json 集合里只降不升。
 */
import { hexToSrgb } from './color-vision'

/** 单通道线性化：c/255 → [0,1]，sRGB 分段函数（IEC 61966-2-1）。 */
function linearChannel(c255: number): number {
  const c = c255 / 255
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}

/** 相对亮度 L = 0.2126R + 0.7152G + 0.0722B（线性域，0=纯黑 1=纯白）。 */
export function relativeLuminance(hex: string): number {
  const [r, g, b] = hexToSrgb(hex)
  return 0.2126 * linearChannel(r) + 0.7152 * linearChannel(g) + 0.0722 * linearChannel(b)
}

/** 两色 WCAG 对比度：较亮者作分子，1.0（同林）—21.0（黑白）。 */
export function wcagContrast(hexA: string, hexB: string): number {
  const la = relativeLuminance(hexA)
  const lb = relativeLuminance(hexB)
  const hi = Math.max(la, lb)
  const lo = Math.min(la, lb)
  return (hi + 0.05) / (lo + 0.05)
}

/** AA 级正文下限。 */
export const WCAG_AA_NORMAL = 4.5
