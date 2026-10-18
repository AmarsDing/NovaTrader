// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const css = readFileSync(resolve(process.cwd(), 'src/theme/tokens.css'), 'utf8')

type Color = [number, number, number, number]

function tokens(block: string): Record<string, string> {
  return Object.fromEntries([...block.matchAll(/(--nt-[\w-]+):\s*([^;]+);/g)].map((match) => [match[1], match[2]]))
}

function color(value: string): Color {
  if (/^#[\da-f]{6}$/i.test(value)) return [1, 3, 5].map((index) => parseInt(value.slice(index, index + 2), 16)).concat(1) as Color
  const values = [...value.matchAll(/[\d.]+/g)].map((match) => Number(match[0]))
  if (values.length === 4) return values as Color
  throw new Error(`unsupported color: ${value}`)
}

function over(front: Color, back: Color): Color {
  return [0, 1, 2].map((index) => front[index] * front[3] + back[index] * (1 - front[3])).concat(1) as Color
}

function luminance(value: Color): number {
  const linear = value.slice(0, 3).map((item) => {
    const c = item / 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722
}

function contrast(front: Color, back: Color): number {
  const [low, high] = [luminance(front), luminance(back)].sort((a, b) => a - b)
  return (high + 0.05) / (low + 0.05)
}

const dark = tokens(css.split(':root {')[1].split('\n}')[0])
const light = { ...dark, ...tokens(css.split(":root[data-theme='light'] {")[1].split('\n}')[0]) }

describe('玻璃合成背景的文字对比', () => {
  for (const [theme, values] of Object.entries({ dark, light })) {
    const c = (name: string) => color(values[`--nt-${name}`])
    const space = over(c('space-blue'), over(c('space-cyan'), c('bg-0')))
    const glass = over(c('glass-bg'), space)
    const sheen = (background: Color) => over(c('glass-sheen-color'), background)
    const surfaces: Record<string, Color> = {
      space,
      glass: sheen(glass),
      raised: sheen(over(c('glass-raised'), glass)),
      data: sheen(over(c('data-bg'), glass)),
      dataRaised: over(c('data-raised'), glass),
      selectedTicket: over([...c('accent').slice(0, 3), 0.08] as Color, sheen(glass)),
    }
    for (const name of ['accent', 'accent-2', 'warn', 'danger', 'info', 'success', 'text-2', 'text-3']) {
      surfaces[`canvas:${name}`] = sheen(over(
        [...c(name).slice(0, 3), Number(values['--nt-canvas-tint-alpha'])] as Color, c('bg-2'),
      ))
    }
    for (const name of ['bg-0', 'bg-1', 'bg-2', 'bg-3']) surfaces[name] = c(name)

    it(`${theme}：普通文字在空间/玻璃/数据/标题/选中合成层均≥4.5`, () => {
      for (const [surface, background] of Object.entries(surfaces)) {
        for (const text of ['text-1', 'text-2', 'text-3']) {
          expect(contrast(c(text), background), `${theme}/${surface}/${text}`).toBeGreaterThanOrEqual(4.5)
        }
      }
    })

    it(`${theme}：语义小字在数据底层均≥4.5；亮色额外覆盖bg3`, () => {
      for (const text of ['accent', 'accent-2', 'warn', 'danger', 'info', 'up', 'down']) {
        for (const background of theme === 'light' ? ['bg-0', 'bg-1', 'bg-2', 'bg-3'] : ['bg-0', 'bg-1', 'bg-2']) {
          expect(contrast(c(text), c(background)), `${theme}/${background}/${text}`).toBeGreaterThanOrEqual(4.5)
        }
      }
    })
  }
})
