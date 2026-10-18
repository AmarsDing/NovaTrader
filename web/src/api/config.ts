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
