import { request } from '@/api/http'

export interface ConfigReceipt { service: string; instance: string; scope: string; revision: number; status: string; detail: string; observedAt: string }
export interface ConfigVersion { revision: number; createdAt: string; active: boolean }
export interface ConfigSnapshot { revision: number; values: Record<string, string>; secretSet: Record<string, boolean>; receipts: ConfigReceipt[]; history: ConfigVersion[]; createdAt: string; note: string; receiptError: string; targetServices?: string[] }
export interface ConfigChange { key: string; before: string; after: string; secret: boolean; service: string; effective: string }
export interface ConfigPreview { baseRevision: number; changes: ConfigChange[]; services: string[]; knownEffects: string[]; unknownEffects: string[]; invalidatesPlans: boolean }
function record(value: unknown): Record<string, unknown> { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {} }
function text(value: unknown): string { return typeof value === 'string' ? value : '' }
function revision(value: unknown): number { const n = typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : value; if (typeof n !== 'number' || !Number.isSafeInteger(n) || n < 0) throw new Error('配置版本缺失或无效，请刷新后端'); return n }
function stringMap(value: unknown): Record<string, string> { return Object.fromEntries(Object.entries(record(value)).filter((entry): entry is [string, string] => typeof entry[1] === 'string')) }
function list(value: unknown): unknown[] { return Array.isArray(value) ? value : [] }
export function normalizeConfigSnapshot(value: unknown): ConfigSnapshot {
  const r = record(value)
  if (r.schema_version !== 1 && r.schemaVersion !== 1) throw new Error('后端未提供版本化配置合同，不能安全保存')
  return { targetServices: list(r.target_services ?? r.targetServices).map(text), revision: revision(r.revision), values: stringMap(r.values), secretSet: Object.fromEntries(Object.entries(record(r.secret_set ?? r.secretSet)).map(([key, val]) => [key, val === true])), receipts: list(r.receipts).map(item => { const row = record(item); return { service: text(row.service), instance: text(row.instance), scope: text(row.scope), revision: revision(row.revision), status: text(row.status), detail: text(row.detail), observedAt: text(row.observed_at ?? row.observedAt) } }), history: list(r.history).map(item => { const row = record(item); return { revision: revision(row.revision), createdAt: text(row.created_at ?? row.createdAt), active: row.active === true } }), createdAt: text(r.created_at ?? r.createdAt), note: text(r.note), receiptError: text(r.receipt_error ?? r.receiptError) }
}
export async function getConfigSnapshot(): Promise<ConfigSnapshot> { return normalizeConfigSnapshot(await request('/api/config')) }
// Legacy read facade is kept for read-only decision evidence; writes need a snapshot.
export async function getConfigValues(): Promise<Record<string, string>> { return stringMap(record(await request('/api/config')).values) }
export async function saveConfig(values: Record<string, string>, baseRevision: number): Promise<ConfigSnapshot> { return normalizeConfigSnapshot(await request('/api/config', { method: 'PUT', json: { values, base_revision: revision(baseRevision) } })) }
export async function saveConfigValues(values: Record<string, string>, baseRevision: number): Promise<Record<string, string>> { return (await saveConfig(values, baseRevision)).values }
export async function previewConfig(values: Record<string, string>, baseRevision: number, targetRevision?: number): Promise<ConfigPreview> {
  const r = record(await request('/api/config/preview', { method: 'POST', json: { values, base_revision: revision(baseRevision), ...(targetRevision == null ? {} : { target_revision: revision(targetRevision) }) } }))
  return { baseRevision: revision(r.base_revision ?? r.baseRevision), changes: list(r.changes).map(item => { const c = record(item); return { key: text(c.key), before: text(c.before), after: text(c.after), secret: c.secret === true, service: text(c.service), effective: text(c.effective) } }), services: list(r.services).map(text), knownEffects: list(r.known_effects ?? r.knownEffects).map(text), unknownEffects: list(r.unknown_effects ?? r.unknownEffects).map(text), invalidatesPlans: r.invalidates_plans === true || r.invalidatesPlans === true }
}
export async function rollbackConfig(baseRevision: number, targetRevision: number): Promise<ConfigSnapshot> { return normalizeConfigSnapshot(await request('/api/config/rollback', { method: 'POST', json: { base_revision: revision(baseRevision), target_revision: revision(targetRevision) } })) }

// ---------- W7b 配置 schema 下发（admin gateway 唯一事实源） ----------
// 字段元数据由后端 binary 发放，前端表单按 schema 渲染——新增字段不再需要前端发版。
// 数据纪律：secret fallback 恒为 ""（服务端永远不写真值）；schema_version!==1 抛错（R-UI-38）。
export type ConfigSchemaFieldKind = 'number' | 'int' | 'text' | 'bool' | 'json' | 'secret' | 'select'
export type ConfigSchemaFieldStore = 'config' | 'risk'
export interface ConfigSchemaOption { value: string; label: string }
export interface ConfigSchemaField {
  key: string
  label: string
  hint: string
  kind: ConfigSchemaFieldKind
  fallback: string
  store: ConfigSchemaFieldStore
  section: string
  /** blob 字段的外层配置键（目前仅 market.sentiment.rules / market.sector.weights），非 blob 字段为空。 */
  blobKey: string
  /** blob 字段的内层键 = key；非 blob 字段为空。 */
  jsonKey: string
  options: ConfigSchemaOption[]
  wide: boolean
  clearable: boolean
}
export interface ConfigSchemaCategory {
  id: string
  title: string
  note: string
  blobKey: string
  fields: ConfigSchemaField[]
}
export interface ConfigSchema { schemaVersion: 1; categories: ConfigSchemaCategory[] }
const SCHEMA_KINDS = new Set<string>(['number', 'int', 'text', 'bool', 'json', 'secret', 'select'])
const SCHEMA_STORES = new Set<string>(['config', 'risk'])
function schemaError(detail: string): Error {
  return new Error(`配置结构已变更（${detail}，R-UI-38：请刷新页面或升级前端）`)
}
function normalizeOption(value: unknown, fieldKey: string): ConfigSchemaOption {
  const row = record(value)
  if (typeof row.value !== 'string' || !row.value || typeof row.label !== 'string' || !row.label) {
    throw schemaError(`字段 ${fieldKey} 的选项不完整`)
  }
  return { value: row.value, label: row.label }
}
function normalizeField(value: unknown, categoryBlobKey: string): ConfigSchemaField {
  const row = record(value)
  const key = text(row.key)
  const label = text(row.label)
  const kind = text(row.kind)
  if (!key || !label || !SCHEMA_KINDS.has(kind)) throw schemaError(`字段 ${key || '缺 key'} / ${kind || '缺 kind'} 无法识别`)
  const store = text(row.store)
  if (!SCHEMA_STORES.has(store)) throw schemaError(`字段 ${key} 的存储位置不受支持`)
  const options = list(row.options).map((item) => normalizeOption(item, key))
  if (kind === 'select' && options.length === 0) throw schemaError(`字段 ${key} 缺少选项`)
  const jsonKey = text(row.json_key ?? row.jsonKey)
  const blobKey = text(row.blob_key ?? row.blobKey) || (jsonKey ? categoryBlobKey : '')
  if ((categoryBlobKey || jsonKey) && !jsonKey) throw schemaError(`字段 ${key} 缺少内层键`)
  if (jsonKey && !blobKey) throw schemaError(`字段 ${key} 缺少外层键`)
  return {
    key,
    label,
    hint: text(row.hint),
    kind: kind as ConfigSchemaFieldKind,
    fallback: kind === 'secret' ? '' : text(row.fallback),
    store: store as ConfigSchemaFieldStore,
    section: text(row.section),
    blobKey,
    jsonKey,
    options,
    wide: row.wide === true,
    clearable: row.clearable === true,
  }
}
function normalizeCategory(value: unknown): ConfigSchemaCategory {
  const row = record(value)
  const id = text(row.id)
  const title = text(row.title)
  if (!id || !title) throw schemaError('分类缺少标识或标题')
  const blobKey = text(row.blob_key ?? row.blobKey)
  const rawFields = list(row.fields)
  if (!rawFields.length) throw schemaError(`分类 ${id} 没有字段`)
  const fields = rawFields.map((item) => normalizeField(item, blobKey))
  const seen = new Set<string>()
  for (const field of fields) {
    if (seen.has(field.key)) throw schemaError(`分类 ${id} 字段 ${field.key} 重复`)
    seen.add(field.key)
  }
  return { id, title, note: text(row.note), blobKey, fields }
}
export function normalizeConfigSchema(value: unknown): ConfigSchema {
  const r = record(value)
  const version = r.schema_version ?? r.schemaVersion
  if (version !== 1) throw new Error('配置 schema 版本不受支持（R-UI-38：请刷新页面或升级前端）')
  if (!Array.isArray(r.categories) || r.categories.length === 0) throw schemaError('字段目录为空')
  const categories = r.categories.map(normalizeCategory)
  const seen = new Set<string>()
  for (const category of categories) {
    if (seen.has(category.id)) throw schemaError(`分类 ${category.id} 重复`)
    seen.add(category.id)
  }
  return { schemaVersion: 1, categories }
}
export async function getConfigSchema(): Promise<ConfigSchema> { return normalizeConfigSchema(await request('/api/config/schema')) }
