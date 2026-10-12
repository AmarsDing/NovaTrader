import { request } from '@/api/http'

export async function getConfigValues(): Promise<Record<string, string>> {
  const raw = await request<{ values?: Record<string, string> }>('/api/config')
  const values = raw && typeof raw === 'object' ? raw.values : undefined
  if (!values || typeof values !== 'object') return {}
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(values)) {
    if (typeof value === 'string') out[key] = value
  }
  return out
}

export async function saveConfigValues(values: Record<string, string>): Promise<Record<string, string>> {
  const raw = await request<{ values?: Record<string, string> }>('/api/config', { method: 'PUT', json: { values } })
  const stored = raw && typeof raw === 'object' ? raw.values : undefined
  if (!stored || typeof stored !== 'object') return values
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(stored)) {
    if (typeof value === 'string') out[key] = value
  }
  return out
}
