/**
 * G4 UI 字面量守护：panels/、display/、viz/ 内禁止裸写颜色字面量
 * （#hex / rgb() / rgba() / hsl() / hsla()），状态色必须走 theme/tokens.css 的 --nt-* 令牌。
 * 移植 gloomberb architecture/ui-literals.test.ts 纪律：
 * - 例外必须登记 ui-literals-allowlist.json 并给出理由；
 * - countLock 只降不升——新增裸色的 PR 直接红，收编令牌的 PR 才允许把锁往下调。
 */
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, extname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const SRC_ROOT = join(dirname(fileURLToPath(import.meta.url)), '../..')
const SCAN_DIRS = ['panels', 'display', 'viz']
const EXTS = new Set(['.vue', '.ts', '.css'])
const RAW_COLOR = /#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(/g

interface AllowEntry {
  file: string
  literal: string
  reason: string
}
interface Allowlist {
  countLock: number
  entries: AllowEntry[]
}

const allowlist = JSON.parse(
  readFileSync(join(dirname(fileURLToPath(import.meta.url)), '../ui-literals-allowlist.json'), 'utf8'),
) as Allowlist

function* walk(dir: string): Generator<string> {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name)
    const stat = statSync(full)
    if (stat.isDirectory()) {
      if (name === '__tests__') continue
      yield* walk(full)
    } else if (EXTS.has(extname(name))) {
      yield full
    }
  }
}

/** 全量扫描 → 「文件::字面量」多重集合键列表（同值多处各自计数）。 */
function scanRawColors(): string[] {
  const found: string[] = []
  for (const scanDir of SCAN_DIRS) {
    for (const file of walk(join(SRC_ROOT, scanDir))) {
      const rel = relative(SRC_ROOT, file).split(sep).join('/')
      const text = readFileSync(file, 'utf8')
      RAW_COLOR.lastIndex = 0
      let m: RegExpExecArray | null
      while ((m = RAW_COLOR.exec(text))) found.push(`${rel}::${m[0]}`)
    }
  }
  return found
}

describe('ui-literals 守护（裸色必须走 --nt-* 令牌）', () => {
  it('扫描结果只允许出现白名单登记的例外，且无失效条目', () => {
    const found = scanRawColors()
    const foundCount = new Map<string, number>()
    for (const key of found) foundCount.set(key, (foundCount.get(key) ?? 0) + 1)

    const allowedCount = new Map<string, number>()
    for (const entry of allowlist.entries) {
      expect(entry.reason.trim(), `${entry.file} ${entry.literal} 必须写明豁免理由`).not.toBe('')
      allowedCount.set(`${entry.file}::${entry.literal}`, (allowedCount.get(`${entry.file}::${entry.literal}`) ?? 0) + 1)
    }

    const unknown = found.filter((key) => (allowedCount.get(key) ?? 0) <= 0)
    const stale = [...allowedCount.keys()].filter((key) => !foundCount.has(key))

    expect(
      unknown,
      `发现未登记的裸色字面量，请改用 --nt-* 令牌（确属例外的登记 ui-literals-allowlist.json）：\n${[...new Set(unknown)].join('\n')}`,
    ).toEqual([])
    expect(
      stale,
      `白名单存在已失效的条目（裸色已收编），请同步删除并让 countLock 只降不升：\n${stale.join('\n')}`,
    ).toEqual([])
  })

  it('裸色总数不超过 countLock（锁只降不升）', () => {
    const found = scanRawColors()
    expect(
      found.length,
      `当前裸色 ${found.length} 处超过白名单锁 ${allowlist.countLock}——只能收编或登记后下调，不得上调`,
    ).toBeLessThanOrEqual(allowlist.countLock)
  })
})
