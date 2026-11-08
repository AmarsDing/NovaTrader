import { describe, expect, it } from 'vitest'
import { buildCsv, csvCell } from '../csv'

describe('csvCell', () => {
  it('普通值原样；null/undefined/NaN → 空串（不写占位符）', () => {
    expect(csvCell('abc')).toBe('abc')
    expect(csvCell(12.5)).toBe('12.5')
    expect(csvCell(null)).toBe('')
    expect(csvCell(undefined)).toBe('')
    expect(csvCell(Number.NaN)).toBe('')
  })

  it('逗号/双引号/换行触发包裹；内嵌双引号翻倍', () => {
    expect(csvCell('a,b')).toBe('"a,b"')
    expect(csvCell('say "hi"')).toBe('"say ""hi"""')
    expect(csvCell('l1\nl2')).toBe('"l1\nl2"')
    expect(csvCell('r\r\n')).toBe('"r\r\n"')
  })

  it('引号+换行+逗号同格：包裹且引号翻倍（评审 P2-3 组合锁）', () => {
    expect(csvCell('"a"\nb,c')).toBe('"""a""\nb,c"')
  })
})

describe('buildCsv', () => {
  it('带 BOM 头（Excel 中文兼容）', () => {
    const out = buildCsv([{ title: '', header: ['a'], rows: [['1']] }])
    expect(out.charCodeAt(0)).toBe(0xfeff)
  })

  it('多段：标题行 + 表头 + 行，段间空行', () => {
    const out = buildCsv([
      { title: '概要', header: ['指标', '值'], rows: [['总收益', 0.12], ['胜率', null]] },
      { title: '明细', header: ['代码'], rows: [['600519']] },
    ])
    const text = out.slice(1) // 去 BOM
    expect(text).toBe('概要\r\n指标,值\r\n总收益,0.12\r\n胜率,\r\n\r\n明细\r\n代码\r\n600519')
  })

  it('空标题省略标题行；空表头省略表头行', () => {
    const out = buildCsv([{ title: '', header: [], rows: [['x']] }])
    expect(out.slice(1)).toBe('x')
  })
})
