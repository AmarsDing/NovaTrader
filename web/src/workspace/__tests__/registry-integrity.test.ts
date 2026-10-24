/**
 * G2 注册表完整性锁定：面板助记符的结构性约束 + 快照。
 * - 每个面板必须挂 shortcut（新增面板漏挂即刻红）；
 * - prefix/aliases 全局唯一（大小写不敏感）——命令面板精确匹配的根基；
 * - 助记符表与功能卡快照：任何表变更必须显式更新快照进评审。
 * - U10：core 集合必须恰好等于默认屏 ids 且同序（移动 DEFAULT_LAYOUT
 *   在 desks.ts 单源，测试与 DockHost 构建同读）。
 */
import { describe, expect, it } from 'vitest'
import { functionIndex } from '../command-grammar'
import { listPanels } from '../panel-registry'
import { DEFAULT_LAYOUT } from '../desks'

const PREFIX_RE = /^[A-Z][A-Z0-9]{1,3}$/

describe('panel-registry shortcut 完整性', () => {
  it('每个面板都有 shortcut 且 prefix 形态合法', () => {
    for (const def of listPanels()) {
      expect(def.shortcut, `${def.id} 缺少 shortcut 助记符`).toBeTruthy()
      expect(def.shortcut!.prefix, `${def.id} prefix 非法`).toMatch(PREFIX_RE)
      expect(def.shortcut!.description, `${def.id} 缺少功能卡描述`).toBeTruthy()
      if (def.shortcut!.argKind !== 'none') {
        expect(def.shortcut!.argHint, `${def.id} 有参数但缺 argHint`).toBeTruthy()
      }
    }
  })

  it('prefix 与 aliases 全局唯一（大小写不敏感）', () => {
    const seen = new Map<string, string>()
    for (const def of listPanels()) {
      const s = def.shortcut
      if (!s) continue
      for (const key of [s.prefix, ...(s.aliases ?? [])]) {
        const up = key.toUpperCase()
        expect(up, `${s.prefix} 的 ${key} 形态非法`).toMatch(PREFIX_RE)
        expect(seen.has(up), `助记符 ${up} 冲突：${seen.get(up)} 与 ${def.id}`).toBe(false)
        seen.set(up, def.id)
      }
    }
  })

  it('面板数量与 popout 白名单不自相矛盾', () => {
    // +1 datasource-center（含 /api/config 凭证写面，与 settings 同级禁弹——不在 POPOUT_ALLOWED）
    expect(listPanels()).toHaveLength(23 + 3 + 1)
  })

  it('U10 core 集合恰为默认屏 ids（集合互证 + 默认屏内部序）', () => {
    const cores = listPanels().filter((d) => d.core === true).map((d) => d.id)
    const layoutIds = DEFAULT_LAYOUT.map((s) => s.id)
    expect([...cores].sort()).toEqual([...layoutIds].sort())
    // 默认屏落位参照必须先于依赖者出现（buildPanels 顺序构建的前提）。
    const seen = new Set<string>()
    for (const s of DEFAULT_LAYOUT) {
      if (s.ref) expect(seen.has(s.ref), `默认屏 ${s.id} 的 ref=${s.ref} 未先于它出现`).toBe(true)
      seen.add(s.id)
    }
  })
})

describe('助记符表与功能卡快照', () => {
  it('prefix → 面板映射快照', () => {
    const table = Object.fromEntries(
      listPanels().map((d) => [
        d.shortcut!.prefix,
        {
          id: d.id,
          title: d.title,
          aliases: d.shortcut!.aliases ?? [],
          arg: d.shortcut!.argKind + (d.shortcut!.argHint ? `(${d.shortcut!.argHint})` : ''),
        },
      ]),
    )
    expect(table).toMatchSnapshot()
  })

  it('functionIndex 全表快照', () => {
    expect(functionIndex()).toMatchSnapshot()
  })
})
