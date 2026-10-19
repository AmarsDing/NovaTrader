/**
 * G4 对比度审计（视觉工程化守护·可选做项「主题对比度归一」落地）：
 * 文字/状态色在其底板上必须读得出——WCAG 2.x (L1+0.05)/(L2+0.05)。
 * 口径「报而不改色」：
 * - floor 集合（基线时实测 ≥4.5 的配对）永久硬卡 4.5，曾达标不许滑落；
 * - belowAA 集合只缩不扩（新出现的低对比配对即红；修复者同 PR 移出登记）；
 * - 全部配对实测值只许 ≥ 基线 − 0.005（不许静默劣化，改善不需改基线）。
 * 令牌解析走 css-vars.ts 共享件（与色觉守护同一 tokens.css 发布值口径）。
 */
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { THEMES, requireVar, type ThemeCase } from './css-vars'
import { relativeLuminance, wcagContrast, WCAG_AA_NORMAL } from '@/utils/contrast'

interface Baseline {
  version: number
  note: string
  /** 每个主题：配对键 → 建档时实测对比度（2 位小数）。 */
  themes: Record<string, Record<string, number>>
  /** 每个主题：永久硬卡 ≥4.5 的配对键（建档即达标者）。 */
  floor: Record<string, string[]>
  /** 每个主题：建档时 <4.5 的配对键（登记待修复，集合只缩不扩）。 */
  belowAA: Record<string, string[]>
}

const BASELINE_PATH = join(dirname(fileURLToPath(import.meta.url)), '../contrast-baseline.json')
const baseline = JSON.parse(readFileSync(BASELINE_PATH, 'utf8')) as Baseline

/** 审计配对：前景令牌 @ 底板令牌（14 配对 × 4 主题切片）。 */
const PAIRS: { fg: string; bg: string }[] = [
  { fg: 'text-1', bg: 'bg-0' },
  { fg: 'text-1', bg: 'bg-1' },
  { fg: 'text-1', bg: 'bg-2' },
  { fg: 'text-2', bg: 'bg-0' },
  { fg: 'text-2', bg: 'bg-1' },
  { fg: 'text-2', bg: 'bg-2' },
  { fg: 'text-3', bg: 'bg-0' },
  { fg: 'text-3', bg: 'bg-1' },
  { fg: 'text-3', bg: 'bg-2' },
  { fg: 'accent', bg: 'bg-1' },
  { fg: 'up', bg: 'bg-1' },
  { fg: 'down', bg: 'bg-1' },
  { fg: 'warn', bg: 'bg-1' },
  { fg: 'danger', bg: 'bg-1' },
]

const pairKey = (pair: { fg: string; bg: string }) => `${pair.fg}@${pair.bg}`

function measured(theme: ThemeCase): Record<string, number> {
  const out: Record<string, number> = {}
  for (const pair of PAIRS) {
    out[pairKey(pair)] = wcagContrast(requireVar(theme, pair.fg), requireVar(theme, pair.bg))
  }
  return out
}

describe('contrast.ts 基础换算', () => {
  it('黑白对比度为 21，同林为 1，且左右对称', () => {
    expect(wcagContrast('#ffffff', '#000000')).toBeCloseTo(21, 0)
    expect(wcagContrast('#000000', '#000000')).toBeCloseTo(1, 10)
    expect(wcagContrast('#d3dee6', '#0b1b2b')).toBeCloseTo(wcagContrast('#0b1b2b', '#d3dee6'), 10)
  })

  it('纯黑/纯白相对亮度为 0 与 1', () => {
    expect(relativeLuminance('#000000')).toBeCloseTo(0, 10)
    expect(relativeLuminance('#ffffff')).toBeCloseTo(1, 10)
  })
})

describe('G4 对比度审计：四主题切片 × 14 配对', () => {
  it('floor 集合实测值不低于基线 − 0.005，且全部配对不许静默劣化', () => {
    for (const theme of THEMES) {
      const base = baseline.themes[theme.name]
      if (!base) throw new Error(`contrast-baseline.json 缺主题「${theme.name}」`)
      const now = measured(theme)
      for (const pair of PAIRS) {
        const key = pairKey(pair)
        const recorded = base[key]
        if (recorded == null) throw new Error(`基线缺配对「${theme.name} ${key}」——新增配对须同 PR 登记`)
        expect(
          now[key],
          `${theme.name} ${key}：实测 ${now[key].toFixed(2)} 低于基线 ${recorded}（不许劣化；有意调低须同 PR 改基线并说明）`,
        ).toBeGreaterThanOrEqual(recorded - 0.005)
      }
    }
  })

  it('floor 集合（曾达标者）必须守住 AA 4.5', () => {
    for (const theme of THEMES) {
      const now = measured(theme)
      for (const key of baseline.floor[theme.name] ?? []) {
        expect(
          now[key],
          `${theme.name} ${key}：建档时 ≥4.5，现 ${now[key].toFixed(2)}——曾达标的文字底板对比度不许滑落`,
        ).toBeGreaterThanOrEqual(WCAG_AA_NORMAL)
      }
    }
  })

  it('belowAA 集合只缩不扩（报而不改色：低对比配对登记在案，修复后移出）', () => {
    for (const theme of THEMES) {
      const now = measured(theme)
      const actual = PAIRS.map(pairKey)
        .filter((key) => now[key] < WCAG_AA_NORMAL)
        .sort()
      const recorded = [...(baseline.belowAA[theme.name] ?? [])].sort()
      expect(
        actual,
        `${theme.name} 低于 AA 的配对集合变化：实测 ${JSON.stringify(actual)} vs 登记 ${JSON.stringify(recorded)}。` +
          '新出现低对比=红；已修复配对请同 PR 从 belowAA 移出（若同步 ≥4.5 移入 floor）。',
      ).toEqual(recorded)
    }
  })
})
