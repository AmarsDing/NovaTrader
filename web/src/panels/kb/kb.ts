/**
 * kb 面板的展示映射与上传表单校验（纯函数，可单测）。
 * 口径来自 FR-04-01（只收 md/txt/docx/xlsx/pdf；旧 .doc/.xls、扫描 PDF 拒收）
 * 和 knowledge.proto / biz.kb.Ingest：url 只是出处格式校验，内容字节仍由调用方给。
 */
import type { KbDocument, KbIngestParams } from '@/api/types/kb'
import type { BadgeTone } from '@/ui/NtBadge.vue'
import { phaseName } from '@/utils/format'

export const CATEGORY_OPTIONS = [
  { value: 'case', label: '案例' },
  { value: 'rule', label: '规则' },
  { value: 'compliance', label: '合规' },
  { value: 'review', label: '复盘' },
]

const CATEGORY_LABELS: Record<string, string> = {
  case: '案例',
  rule: '规则',
  compliance: '合规',
  review: '复盘',
}

export function categoryLabel(v: string): string {
  return CATEGORY_LABELS[v] || v || '—'
}

export const SOURCE_TYPE_OPTIONS = [
  { value: 'file', label: '文件' },
  { value: 'url', label: '链接' },
  { value: 'manual', label: '手工录入' },
]

const SOURCE_LABELS: Record<string, string> = { file: '文件', url: '链接', manual: '手工', signal: '信号' }

export function sourceLabel(v: string): string {
  return SOURCE_LABELS[v] || v || '—'
}

export const STATUS_OPTIONS = [
  { value: 'active', label: '有效' },
  { value: 'superseded', label: '被替换' },
  { value: 'expired', label: '已过期' },
  { value: 'deleted', label: '已删除' },
]

const STATUS_LABELS: Record<string, string> = {
  active: '有效',
  superseded: '被替换',
  expired: '已过期',
  deleted: '已删除',
}

export function statusLabel(v: string): string {
  return STATUS_LABELS[v] || v || '—'
}

export function statusTone(v: string): BadgeTone {
  if (v === 'active') return 'success'
  if (v === 'superseded' || v === 'expired') return 'warn'
  if (v === 'deleted') return 'danger'
  return 'neutral'
}

export function embedLabel(v: string): string {
  return v === 'done' ? '已向量' : v === 'pending' ? '待补向量' : v || '—'
}

export function embedTone(v: string): BadgeTone {
  return v === 'done' ? 'accent' : v === 'pending' ? 'warn' : 'neutral'
}

const OUTCOME_LABELS: Record<string, string> = {
  win: '盈利',
  loss: '亏损',
  flat: '持平',
  unfilled: '未成交',
  cancelled: '已撤销',
}

export function outcomeLabel(v: string): string {
  return OUTCOME_LABELS[v] || v || '—'
}

const FINAL_STATUS_LABELS: Record<string, string> = {
  closed: '已平仓',
  expired: '信号过期',
  cancelled: '信号撤销',
}

export function finalStatusLabel(v: string): string {
  return FINAL_STATUS_LABELS[v] || v || '—'
}

const ATTRIBUTION_LABELS: Record<string, string> = {
  indicator_failed: '指标失效',
  news_misread: '消息误判',
  unfilled: '未成交',
  emotion_mismatch: '情绪错配',
}

export function attributionLabel(v: string): string {
  return ATTRIBUTION_LABELS[v] || v || ''
}

export const EMOTION_OPTIONS = ['ICE', 'RECOVER', 'WARM', 'HOT', 'FADE'].map((v) => ({
  value: v,
  label: phaseName(v),
}))

export const BOOK_OPTIONS = [
  { value: 'paper', label: '模拟' },
  { value: 'live', label: '实盘' },
]

/* ---------- 数字/件数展示 ---------- */

export function formatSize(bytes: number | null): string {
  if (bytes == null) return '—'
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MB`
  if (bytes >= 1 << 10) return `${(bytes / (1 << 10)).toFixed(1)} KB`
  return `${bytes} B`
}

/** 逗号/空白分词，去重去空。 */
export function splitList(text: string): string[] {
  const out: string[] = []
  for (const part of text.split(/[\s,，、;；]+/)) {
    const v = part.trim()
    if (v && !out.includes(v)) out.push(v)
  }
  return out
}

/* ---------- 上传校验与编码 ---------- */

/** FR-04-01 支持的后缀；后端用 file_name 后缀判格式，manual/url 的文本缺省给 note.md。 */
export const SUPPORTED_EXTS = ['md', 'txt', 'docx', 'xlsx', 'pdf'] as const

export const MAX_UPLOAD_BYTES = 20 * 1024 * 1024

export function extOf(name: string): string {
  const i = name.lastIndexOf('.')
  return i < 0 ? '' : name.slice(i + 1).toLowerCase()
}

/** URL 路径带有支持的后缀时取 basename 作 file_name，否则用 note.md。 */
export function fileNameFromUrl(url: string): string {
  try {
    const u = new URL(url)
    const base = u.pathname.split('/').filter(Boolean).pop() ?? ''
    return SUPPORTED_EXTS.includes(extOf(base) as (typeof SUPPORTED_EXTS)[number]) ? base : 'note.md'
  } catch {
    return 'note.md'
  }
}

export function bytesToBase64(bytes: Uint8Array): string {
  let out = ''
  for (let i = 0; i < bytes.length; i += 0x8000) {
    out += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  }
  return btoa(out)
}

export function textToBase64(text: string): string {
  return bytesToBase64(new TextEncoder().encode(text))
}

/** datetime-local（本地时区）→ RFC3339。 */
export function localToRFC3339(dtLocal: string): string {
  if (!dtLocal) return ''
  const d = new Date(dtLocal)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

export interface IngestForm {
  category: string
  sourceType: 'file' | 'url' | 'manual'
  title: string
  /** 文件→文件名；链接→URL；手工→出处描述 */
  source: string
  author: string
  /** file 模式下已读到的文件名，与字节配套 */
  fileName: string
  /** file 模式下已读字节大小 */
  fileSize: number | null
  /** url/manual 模式的文本内容 */
  text: string
  stockCodesText: string
  tagsText: string
  expiresLocal: string
}

/**
 * 校验并拼 IngestDocument 请求。file 模式的 contentBase64 由调用方读文件后塞进来。
 * 返回 reason 即失败原因（也已经展示了界面要提醒的话）。
 */
export function buildIngest(
  form: IngestForm,
  contentBase64: string,
): { ok: true; params: KbIngestParams } | { ok: false; reason: string } {
  const base: Omit<KbIngestParams, 'source' | 'fileName' | 'contentBase64'> = {
    category: form.category,
    title: form.title.trim(),
    sourceType: form.sourceType,
    author: form.author.trim(),
    stockCodes: splitList(form.stockCodesText),
    tags: splitList(form.tagsText),
    expiresAt: localToRFC3339(form.expiresLocal) || undefined,
  }
  if (!base.category) return { ok: false, reason: '分类必选：案例 / 规则 / 合规 / 复盘' }
  if (form.expiresLocal && !base.expiresAt) return { ok: false, reason: '过期时间格式不对' }

  if (form.sourceType === 'file') {
    if (!form.fileName || !contentBase64) return { ok: false, reason: '先选择要上传的文件' }
    if (!SUPPORTED_EXTS.includes(extOf(form.fileName) as (typeof SUPPORTED_EXTS)[number])) {
      return { ok: false, reason: '只收 md / txt / docx / xlsx / pdf；旧 .doc/.xls 与扫描 PDF 不收' }
    }
    if (form.fileSize != null && form.fileSize > MAX_UPLOAD_BYTES) {
      return { ok: false, reason: '文件超过 20 MB，库内上传上限' }
    }
    const source = form.source.trim() || form.fileName
    return { ok: true, params: { ...base, source, fileName: form.fileName, contentBase64 } }
  }

  const text = form.text.trim()
  if (!text) return { ok: false, reason: '内容文本不能为空（界面不抓网页，贴原文）' }
  if (form.sourceType === 'url') {
    const source = form.source.trim()
    if (!/^https?:\/\/.+/i.test(source)) return { ok: false, reason: 'url 出处必须是 http(s) 地址' }
    return { ok: true, params: { ...base, source, fileName: fileNameFromUrl(source), contentBase64: textToBase64(text) } }
  }
  if (!base.author) return { ok: false, reason: '手工录入必须写作者/录入人' }
  const source = form.source.trim()
  if (!source) return { ok: false, reason: '手工录入也要一处出手（来源字段，来源不明不收）' }
  return { ok: true, params: { ...base, source, fileName: form.fileName || 'note.md', contentBase64: textToBase64(text) } }
}

/* ---------- 文档行视图模型 ---------- */

export interface DocRow extends Record<string, unknown> {
  doc: KbDocument
  id: string
  title: string
  category: string
  format: string
  size: string
  status: string
  embed: string
  chunks: string
  version: number | null
  created: string
  /** 空串占位，配合 NtTable 的操作列插槽 */
  action: string
}

export function toDocRow(d: KbDocument): DocRow {
  return {
    doc: d,
    id: d.id,
    title: d.title || d.fileName || '（无标题）',
    category: categoryLabel(d.category),
    format: d.format || '—',
    size: formatSize(d.sizeBytes),
    status: statusLabel(d.status),
    embed: embedLabel(d.embedStatus),
    chunks: d.chunkCount == null ? '—' : String(d.chunkCount),
    version: d.version,
    created: d.createdAt,
    action: '',
  }
}
