import { describe, expect, it } from 'vitest'
import {
  buildIngest,
  categoryLabel,
  embedLabel,
  embedTone,
  extOf,
  fileNameFromUrl,
  formatSize,
  localToRFC3339,
  outcomeLabel,
  splitList,
  statusLabel,
  statusTone,
  textToBase64,
  toDocRow,
  type IngestForm,
} from './kb'

function form(patch: Partial<IngestForm> = {}): IngestForm {
  return {
    category: 'rule',
    sourceType: 'file',
    title: '',
    source: '',
    author: '',
    fileName: 'playbook.md',
    fileSize: 12,
    text: '',
    stockCodesText: '600519, 300750 600519',
    tagsText: '短线， 断板',
    expiresLocal: '',
    ...patch,
  }
}

describe('panels/kb 上传校验', () => {
  it('分类必选项领先拒绝', () => {
    const r = buildIngest(form({ category: '' }), 'aGk=')
    expect(r.ok).toBe(false)
    if (!r.ok) expect(r.reason).toContain('分类必选')
  })

  it('file：没读文件、坏后缀、超 20 MB 各自给出原因', () => {
    expect(buildIngest(form(), '').ok).toBe(false)
    const bad = buildIngest(form({ fileName: '旧报表.doc' }), 'aGk=')
    expect(bad.ok).toBe(false)
    if (!bad.ok) expect(bad.reason).toContain('只收 md / txt / docx / xlsx / pdf')
    const big = buildIngest(form({ fileName: 'a.pdf', fileSize: 21 * 1024 * 1024 }), 'aGk=')
    expect(big.ok).toBe(false)
    if (!big.ok) expect(big.reason).toContain('20 MB')
  })

  it('file：出处空时回退文件名，股票/标签列表切分去重', () => {
    const r = buildIngest(form({ source: '  ' }), 'aGk=')
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.params.source).toBe('playbook.md')
      expect(r.params.fileName).toBe('playbook.md')
      expect(r.params.stockCodes).toEqual(['600519', '300750'])
      expect(r.params.tags).toEqual(['短线', '断板'])
    }
  })

  it('url：非 http(s) 拒绝；通过后 file_name 从链接取，内容是文本的 base64', () => {
    const bad = buildIngest(form({ sourceType: 'url', source: 'ftp://x', text: '正文' }), '')
    expect(bad.ok).toBe(false)
    const ok = buildIngest(form({ sourceType: 'url', source: 'https://a.cn/rpt/note.pdf', text: '正文 ABC' }), '')
    expect(ok.ok).toBe(true)
    if (ok.ok) {
      expect(ok.params.fileName).toBe('note.pdf')
      expect(Buffer.from(ok.params.contentBase64, 'base64').toString('utf8')).toBe('正文 ABC')
    }
  })

  it('manual：作者与出处必填；通过后 file_name 缺省 note.md', () => {
    expect(buildIngest(form({ sourceType: 'manual', fileName: '', source: '复盘', text: '内文' }), '').ok).toBe(false)
    expect(buildIngest(form({ sourceType: 'manual', fileName: '', author: '王', source: '', text: '内文' }), '').ok).toBe(false)
    const ok = buildIngest(form({ sourceType: 'manual', fileName: '', author: '王', source: '复盘笔记', text: '内文' }), '')
    expect(ok.ok).toBe(true)
    if (ok.ok) expect(ok.params.fileName).toBe('note.md')
  })
})

describe('panels/kb 展示映射与编码', () => {
  it('extOf / fileNameFromUrl', () => {
    expect(extOf('a.DOCX')).toBe('docx')
    expect(extOf('noext')).toBe('')
    expect(fileNameFromUrl('https://a.cn/x/y.pdf?p=1')).toBe('y.pdf')
    expect(fileNameFromUrl('https://a.cn/x/')).toBe('note.md')
    expect(fileNameFromUrl('not a url')).toBe('note.md')
  })

  it('textToBase64 是可逆的 UTF-8 base64', () => {
    expect(Buffer.from(textToBase64('案例 复盘'), 'base64').toString('utf8')).toBe('案例 复盘')
  })

  it('localToRFC3339：空/坏值返回空，正常值转 ISO', () => {
    expect(localToRFC3339('')).toBe('')
    expect(localToRFC3339('not-a-date')).toBe('')
    expect(localToRFC3339('2026-10-18T10:00')).toContain(':00.000Z')
  })

  it('splitList 支持逗号/中文逗号/空格，去重去空', () => {
    expect(splitList('600519, 300750，600519  000001')).toEqual(['600519', '300750', '000001'])
    expect(splitList('  ')).toEqual([])
  })

  it('标签与 tone 映射对未知值回退', () => {
    expect(categoryLabel('case')).toBe('案例')
    expect(categoryLabel('gossip')).toBe('gossip')
    expect(statusLabel('active')).toBe('有效')
    expect(statusTone('deleted')).toBe('danger')
    expect(embedLabel('pending')).toBe('待补向量')
    expect(embedTone('done')).toBe('accent')
    expect(outcomeLabel('win')).toBe('盈利')
  })

  it('formatSize 按 KB/MB 取档', () => {
    expect(formatSize(500)).toBe('500 B')
    expect(formatSize(2048)).toBe('2.0 KB')
    expect(formatSize(3 * 1024 * 1024)).toBe('3.0 MB')
  })

  it('toDocRow：空分块数显示 —，标题回退文件名', () => {
    const row = toDocRow({
      id: '1',
      category: 'rule',
      title: '',
      source: 's',
      sourceType: 'file',
      author: '',
      format: 'md',
      fileName: 'playbook.md',
      sizeBytes: 12,
      contentHash: '',
      status: 'active',
      version: 2,
      stockCodes: [],
      tags: [],
      expiresAt: '',
      chunkCount: null,
      embedStatus: 'pending',
      createdAt: '2026-10-18T00:00:00Z',
      updatedAt: '',
    })
    expect(row.title).toBe('playbook.md')
    expect(row.chunks).toBe('—')
    expect(row.status).toBe('有效')
  })
})
