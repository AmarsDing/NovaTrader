/**
 * 色觉模拟与感知距离（G4 视觉工程化守护）。
 *
 * 移植自 gloomberb color-vision 测试思路：
 * - 二色觉模拟矩阵：Vischeck/Wickline 系 RGB 域仿真，作用于线性 sRGB；
 *   2026-10-21 对抗复核勘正：原注释误书 Machado 2009——Machado severity=1.0 官方表
 *   含负项（prot 首列 0.152/0.115/-0.004），与本装载值（0.567/0.558/0）根本不同；
 *   门禁数值口径不变（≥0.25 / 二色觉 ≥0.15 / warn 地板 / safe≥base×1.3 均按本模型实测校准），
 *   是否改采真 Machado 2009 重校准留独立批决策。
 * - 感知空间：OKLab（Ottosson 2017），欧氏距离 ≥0.15 视为色盲用户可稳定区分的阈值。
 * 纯函数、无 DOM 依赖；守护测试与本模块共用同一实现，避免测试"自己骗自己"。
 */

export type Dichromacy = 'protanopia' | 'deuteranopia' | 'tritanopia'

/** Vischeck/Wickline 系二色觉模拟矩阵（行主序 3×3，作用于线性 sRGB；非 Machado 2009 表，勘正见文件头）。 */
const MATRICES: Record<Dichromacy, [number, number, number, number, number, number, number, number, number]> = {
  protanopia: [0.567, 0.433, 0, 0.558, 0.442, 0, 0, 0.242, 0.758],
  deuteranopia: [0.625, 0.375, 0, 0.7, 0.3, 0, 0, 0.3, 0.7],
  tritanopia: [0.95, 0.05, 0, 0, 0.433, 0.567, 0, 0.475, 0.525],
}

export const DICHROMACY_TYPES: Dichromacy[] = ['protanopia', 'deuteranopia', 'tritanopia']

/** OKLab 可辨阈值：低于它，色盲用户难以区分两种状态色。 */
export const OKLAB_DISTINCT = 0.15

type LinearRgb = [number, number, number]
type Oklab = [number, number, number]

/** "#rgb"/"#rrggbb"/"#rrggbbaa" → sRGB 0-255 三通道（alpha 忽略）。 */
export function hexToSrgb(hex: string): [number, number, number] {
  const raw = hex.trim().replace(/^#/, '')
  if (!/^[0-9a-fA-F]{3}$|^[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$/.test(raw)) {
    throw new Error(`不是合法 hex 颜色: ${hex}`)
  }
  const full = raw.length === 3 ? [...raw].map((c) => c + c).join('') : raw
  return [
    parseInt(full.slice(0, 2), 16),
    parseInt(full.slice(2, 4), 16),
    parseInt(full.slice(4, 6), 16),
  ]
}

function srgbChannelToLinear(value: number): number {
  const c = value / 255
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}

/** hex → 线性 sRGB。 */
export function hexToLinear(hex: string): LinearRgb {
  const [r, g, b] = hexToSrgb(hex)
  return [srgbChannelToLinear(r), srgbChannelToLinear(g), srgbChannelToLinear(b)]
}

/** 模拟二色觉观察者看到的颜色（线性 sRGB 域）。 */
export function simulateDichromacy(hex: string, type: Dichromacy): LinearRgb {
  const [r, g, b] = hexToLinear(hex)
  const m = MATRICES[type]
  return [
    m[0] * r + m[1] * g + m[2] * b,
    m[3] * r + m[4] * g + m[5] * b,
    m[6] * r + m[7] * g + m[8] * b,
  ]
}

function oklab([r, g, b]: LinearRgb): Oklab {
  const l = 0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b
  const m = 0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b
  const s = 0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b
  const l_ = Math.cbrt(l)
  const m_ = Math.cbrt(m)
  const s_ = Math.cbrt(s)
  return [
    0.2104542553 * l_ + 0.793617785 * m_ - 0.0040720468 * s_,
    1.9779984951 * l_ - 2.428592205 * m_ + 0.4505937099 * s_,
    0.0259040371 * l_ + 0.7827717662 * m_ - 0.808675766 * s_,
  ]
}

function distance(a: Oklab, b: Oklab): number {
  return Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2])
}

/** 正常视觉下两个 hex 的 OKLab 感知距离。 */
export function oklabDistance(hexA: string, hexB: string): number {
  return distance(oklab(hexToLinear(hexA)), oklab(hexToLinear(hexB)))
}

/** 二色觉模拟下两个 hex 的 OKLab 感知距离——色盲用户实际能拉开的差距。 */
export function oklabDistanceUnder(hexA: string, hexB: string, type: Dichromacy): number {
  return distance(oklab(simulateDichromacy(hexA, type)), oklab(simulateDichromacy(hexB, type)))
}

/** 三种二色觉下的最小距离（最不利的那一类色盲决定可用性）。 */
export function minDichromacyDistance(hexA: string, hexB: string): number {
  return Math.min(...DICHROMACY_TYPES.map((type) => oklabDistanceUnder(hexA, hexB, type)))
}
