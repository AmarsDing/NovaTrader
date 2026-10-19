/**
 * theme 测试共用的 tokens.css 解析件：直接读发布值（不是代码里抄一份的副本）。
 * 色觉守护与对比度审计共用同一套主题四切片，口径只能有一处。
 */
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const CSS_PATH = join(dirname(fileURLToPath(import.meta.url)), '../tokens.css')
const css = readFileSync(CSS_PATH, 'utf8')

/** tokens.css 原文：rgba()/color-mix 等非 hex 派生令牌的解析场景（如 glow 同源守护）。 */
export const tokensCss = css

/** 按精确 selector 抽出块内 --nt-* hex 变量（只取 hex 值，rgba 派生令牌不在门禁范围）。
 *  tokens.css 块是扁平无嵌套的：定位「selector {」，取到首个行首 } 之间的体。 */
export function blockVars(selector: string): Record<string, string> {
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

export interface ThemeCase {
  name: string
  vars: Record<string, string>
}

const dark = blockVars(':root')
const light = { ...dark, ...blockVars(":root[data-theme='light']") }
const darkSafe = { ...dark, ...blockVars(":root[data-colorvision='safe']") }
const lightSafe = { ...light, ...blockVars(":root[data-theme='light'][data-colorvision='safe']") }

/** 四切片散件：安全×默认成对比较（safe≥base）等场景按名取用。 */
export const THEME_VARS = { dark, light, darkSafe, lightSafe }

export const THEMES: ThemeCase[] = [
  { name: '暗色/默认', vars: dark },
  { name: '暗色/色觉安全', vars: darkSafe },
  { name: '亮色/默认', vars: light },
  { name: '亮色/色觉安全', vars: lightSafe },
]

export function requireVar(theme: ThemeCase, key: string): string {
  const value = theme.vars[`nt-${key}`]
  if (!value) throw new Error(`${theme.name} 缺少令牌 --nt-${key}`)
  return value
}
