/**
 * CSV 导出工具（TSP 四段式导出移植，研究报告 §3.1）：
 * - `\ufeff` BOM 头保 Excel 中文兼容（先例：CommandBar 布局导出用 Blob + a.download）；
 * - csvCell 转义：逗号/双引号/换行/回车触发包裹，内嵌双引号翻倍；
 * - 缺值（null/undefined/NaN）导出为空串——导出不写「—」占位字符，保持数据可再计算。
 */

/** 单元格转义：任意值 → CSV 安全文本。 */
export function csvCell(value: unknown): string {
  if (value == null) return ''
  if (typeof value === 'number' && Number.isNaN(value)) return ''
  const s = String(value)
  if (/[",\n\r]/.test(s)) return `"${s.replace(/"/g, '""')}"`
  return s
}

/** 一段 = 标题行 + 表头 + 数据行；段间空行分隔。 */
export interface CsvSection {
  /** 段标题（如「概要」）；空串省略标题行。 */
  title: string
  header: string[]
  rows: (readonly unknown[])[]
}

/** 组装多段 CSV 文本（含 BOM）。 */
export function buildCsv(sections: readonly CsvSection[]): string {
  const parts: string[] = []
  for (const sec of sections) {
    const lines: string[] = []
    if (sec.title) lines.push(csvCell(sec.title))
    if (sec.header.length) lines.push(sec.header.map(csvCell).join(','))
    for (const row of sec.rows) lines.push(row.map(csvCell).join(','))
    parts.push(lines.join('\r\n'))
  }
  return '\ufeff' + parts.join('\r\n\r\n')
}

/** 触发浏览器下载（Tauri webview 同用）；filename 建议带 .csv 后缀。 */
export function downloadCsv(filename: string, sections: readonly CsvSection[]): void {
  const blob = new Blob([buildCsv(sections)], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  // 延迟回收：click 同步派发后浏览器已接过引用，下一帧回收即可。
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
