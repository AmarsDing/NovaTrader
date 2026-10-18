/**
 * G4 色觉守护：
 * ① 默认红涨绿跌与色觉安全（朱红/钴蓝）在 protanopia/deuteranopia/tritanopia
 *    模拟下 up/down、danger/success 的 OKLab 距离都必须 ≥0.15；
 * ② 安全模式相对默认只允许提升（涨跌幅可辨度 ≥1.3×），不允许劣化 warn 关系。
 * 令牌直接解析 tokens.css 原文——测的是发布值，不是代码里抄一份的副本。
 */
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  DICHROMACY_TYPES,
  hexToSrgb,
  OKLAB_DISTINCT,
  oklabDistance,
  oklabDistanceUnder,
  simulateDichromacy,
} from '@/utils/color-vision'

const CSS_PATH = join(dirname(fileURLToPath(import.meta.url)), '../tokens.css')
const css = readFileSync(CSS_PATH, 'utf8')

/** 按精确 selector 抽出块内 --nt-* hex 变量（只取 hex 值，rgba 派生令牌不在门禁范围）。
 *  tokens.css 块是扁平无嵌套的：定位「selector {」，取到首个行首 } 之间的体。 */
function blockVars(selector: string): Record<string, string> {
  const head = css.indexOf(`${selector} {`)
  if (head < 0) return {}
  const open = css.indexOf('{', head)
  const close = css.indexOf('\n}', open)
  const body = close < 0 ? '' : css.slice(open, close)
  const vars: Record<string, string> = {}
  const re = /--([\w-]+)\s*:\s*(#[0-9a-fA-F]{3,8})\b/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body))) vars[m[1]] = m[2].toLowerCase()
  return vars
}

const dark = blockVars(':root')
const light = { ...dark, ...blockVars(":root[data-theme='light']") }
const darkSafe = { ...dark, ...blockVars(":root[data-colorvision='safe']") }
const lightSafe = { ...light, ...blockVars(":root[data-theme='light'][data-colorvision='safe']") }

interface ThemeCase {
  name: string
  vars: Record<string, string>
}
const THEMES: ThemeCase[] = [
  { name: '暗色/默认', vars: dark },
  { name: '暗色/色觉安全', vars: darkSafe },
  { name: '亮色/默认', vars: light },
  { name: '亮色/色觉安全', vars: lightSafe },
]

function requireVar(theme: ThemeCase, key: string): string {
  const value = theme.vars[`nt-${key}`]
  if (!value) throw new Error(`${theme.name} 缺少令牌 --nt-${key}`)
  return value
}

describe('color-vision 基础换算', () => {
  it('hex 解析与非法输入', () => {
    expect(hexToSrgb('#f95738')).toEqual([249, 87, 56])
    expect(hexToSrgb('#fff')).toEqual([255, 255, 255])
    expect(() => hexToSrgb('#zzzzzz')).toThrow()
  })

  it('红色盲模拟纯红：Machado 矩阵第一列', () => {
    const [r, g, b] = simulateDichromacy('#ff0000', 'protanopia')
    expect(r).toBeCloseTo(0.567, 3)
    expect(g).toBeCloseTo(0.558, 3)
    expect(b).toBeCloseTo(0, 3)
  })

  it('黑白 OKLab 距离约为 1 且对称', () => {
    const d = oklabDistance('#ffffff', '#000000')
    expect(Math.abs(d - 1)).toBeLessThan(0.01)
    expect(oklabDistance('#ff6b81', '#35dfb1')).toBeCloseTo(oklabDistance('#35dfb1', '#ff6b81'), 10)
  })
})

describe('主题状态色可辨度硬门禁', () => {
  for (const theme of THEMES) {
    it(`${theme.name}：up/down 与 danger/success 正常视觉距离 ≥0.25`, () => {
      expect(oklabDistance(requireVar(theme, 'up'), requireVar(theme, 'down'))).toBeGreaterThanOrEqual(0.25)
      expect(oklabDistance(requireVar(theme, 'danger'), requireVar(theme, 'success'))).toBeGreaterThanOrEqual(0.25)
    })

    for (const type of DICHROMACY_TYPES) {
      it(`${theme.name}：${type} 下 up/down 与 danger/success ≥ ${OKLAB_DISTINCT}`, () => {
        expect(
          oklabDistanceUnder(requireVar(theme, 'up'), requireVar(theme, 'down'), type),
        ).toBeGreaterThanOrEqual(OKLAB_DISTINCT)
        expect(
          oklabDistanceUnder(requireVar(theme, 'danger'), requireVar(theme, 'success'), type),
        ).toBeGreaterThanOrEqual(OKLAB_DISTINCT)
      })
    }
  }

  it('亮/暗两块色觉安全块存在且覆盖 up/down/danger/success', () => {
    for (const [name, vars] of [['darkSafe', darkSafe], ['lightSafe', lightSafe]] as const) {
      for (const key of ['up', 'down', 'danger', 'success']) {
        expect(vars[`nt-${key}`], `${name} 必须显式覆盖 --nt-${key}`).toBeTruthy()
      }
    }
  })
})

describe('色觉安全模式只允许提升不允许劣化', () => {
  for (const [name, base, safe] of [['暗色', dark, darkSafe], ['亮色', light, lightSafe]] as const) {
    for (const type of DICHROMACY_TYPES) {
      it(`${name} 安全模式 ${type} 下涨跌幅可辨度 ≥ 默认 ×1.3`, () => {
        const baseD = oklabDistanceUnder(requireVar({ name, vars: base }, 'up'), requireVar({ name, vars: base }, 'down'), type)
        const safeD = oklabDistanceUnder(requireVar({ name, vars: safe }, 'up'), requireVar({ name, vars: safe }, 'down'), type)
        expect(safeD).toBeGreaterThanOrEqual(baseD * 1.3)
      })
    }

    it(`${name} 安全模式对 warn 的可辨度不差于默认`, () => {
      const worstUnder = (vars: Record<string, string>, a: string, b: string) =>
        Math.min(...DICHROMACY_TYPES.map((t) => oklabDistanceUnder(vars[`nt-${a}`], vars[`nt-${b}`], t)))
      for (const pair of [['up', 'warn'], ['down', 'warn']] as const) {
        for (const [a, b] of [pair]) {
          expect(worstUnder(safe, a, b)).toBeGreaterThanOrEqual(worstUnder(base, a, b) - 1e-9)
        }
      }
    })
  }
})
