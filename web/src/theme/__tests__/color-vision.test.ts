/**
 * G4 色觉守护：
 * ① 默认红涨绿跌与色觉安全（朱红/钴蓝）在 protanopia/deuteranopia/tritanopia
 *    模拟下 up/down、danger/success 的 OKLab 距离都必须 ≥0.15；
 * ② 安全模式相对默认只允许提升（涨跌幅可辨度 ≥1.3×），不允许劣化 warn 关系。
 * 令牌解析走 css-vars.ts 共享件——测的是 tokens.css 发布值，与对比度审计同一口径。
 */
import { describe, expect, it } from 'vitest'
import { THEMES, requireVar, THEME_VARS, tokensCss as css } from './css-vars'
import {
  DICHROMACY_TYPES,
  hexToSrgb,
  minDichromacyDistance,
  OKLAB_DISTINCT,
  oklabDistance,
  oklabDistanceUnder,
  simulateDichromacy,
} from '@/utils/color-vision'

const { dark, light, darkSafe, lightSafe } = THEME_VARS

describe('color-vision 基础换算', () => {
  it('hex 解析与非法输入', () => {
    expect(hexToSrgb('#f95738')).toEqual([249, 87, 56])
    expect(hexToSrgb('#fff')).toEqual([255, 255, 255])
    expect(() => hexToSrgb('#zzzzzz')).toThrow()
  })

  it('红色盲模拟纯红：仿真矩阵第一列（Vischeck 系）', () => {
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

describe('warn 关系硬门禁（G4 weak-pair 加固）', () => {
  /**
   * 2026-10-19 G4 定色：暗 --nt-warn #ffe852（h52 黄琥珀，safe≥base 成对成立）、亮 #705600（h47 深金琥珀）。
   * 实测三类二色觉最小 OKLab 距离：暗 up-warn 0.044→0.080、亮 up-warn 0.088→0.133；
   * down-warn 暗 0.218（2026-10-21 明度调和后实测，原 0.216）/ 亮 0.132；warn vs danger·success 暗 0.087 / 亮 0.132。
   * 余量警戒（2026-10-21 复核）：多路 warn 地板只剩 1.6%–6.9%（亮 down/success-warn 0.132 vs 地板 0.13 最薄），warn 或涨跌族微调必须先复算再放数。
   * 地板按实测值再留余量钉死——任何 dilute warn 的改动必须在此显式放数评审。
   */
  const FLOORS: Record<'dark' | 'light', { up: number; down: number; bad: number }> = {
    dark: { up: 0.075, down: 0.2, bad: 0.085 },
    light: { up: 0.13, down: 0.13, bad: 0.13 },
  }
  for (const theme of THEMES) {
    const floor = theme.name.startsWith('亮色') ? FLOORS.light : FLOORS.dark
    it(`${theme.name}：warn 对 up/down/danger/success 三类二色觉最小距离达到 G4 地板`, () => {
      expect(minDichromacyDistance(requireVar(theme, 'up'), requireVar(theme, 'warn'))).toBeGreaterThanOrEqual(floor.up)
      expect(minDichromacyDistance(requireVar(theme, 'down'), requireVar(theme, 'warn'))).toBeGreaterThanOrEqual(floor.down)
      expect(minDichromacyDistance(requireVar(theme, 'danger'), requireVar(theme, 'warn'))).toBeGreaterThanOrEqual(floor.bad)
      expect(minDichromacyDistance(requireVar(theme, 'success'), requireVar(theme, 'warn'))).toBeGreaterThanOrEqual(floor.bad)
    })
  }
  it('两轮主题 --nt-glow-warn 与 --nt-warn 必须同源（rgba 派生不自说自话）', () => {
    const glowRgb = (selector: string): [number, number, number] => {
      const head = css.indexOf(`${selector} {`)
      expect(head, `${selector} 块必须存在`).toBeGreaterThanOrEqual(0)
      const open = css.indexOf('{', head)
      const close = css.indexOf('\n}', open)
      const body = css.slice(open, close)
      const m = /--nt-glow-warn\s*:\s*[^;]*\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/.exec(body)
      expect(m, `${selector} 必须声明 --nt-glow-warn`).toBeTruthy()
      return [Number(m![1]), Number(m![2]), Number(m![3])]
    }
    expect(glowRgb(':root')).toEqual(hexToSrgb(dark['nt-warn']))
    expect(glowRgb(":root[data-theme='light']")).toEqual(hexToSrgb(light['nt-warn']))
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
