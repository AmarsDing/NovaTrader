import { request } from './http'
import { pick } from '@/utils/format'

export interface ResearchJob {
  id: string
  kind: string
  manifestId: string
  manifestHash: string
  inputHash: string
  status: string
  reason: string
  revision: string
  asOf: string
  createdAt: string
  startedAt: string
  finishedAt: string
  result: Record<string, unknown> | null
  artifactHash: string
  cancelRequested: boolean
}
export interface ResearchDraft {
  kind: string
  manifest_id: string
  as_of: string
  input_json: string
  options_json: string
  timeout_seconds: number
  seed: number
}
export function parseResearchJob(raw: unknown): ResearchJob {
  const id = pick<string>(raw, 'runId', 'run_id')
  const status = pick<string>(raw, 'status')
  if (!id || !status) throw new Error('研究任务身份或状态缺失')
  const resultJSON = pick<string>(raw, 'resultJson', 'result_json')
  let result: Record<string, unknown> | null = null
  if (resultJSON) {
    const parsed: unknown = JSON.parse(resultJSON)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('研究产物合同无效')
    result = parsed as Record<string, unknown>
  }
  return {
    id,
    status,
    kind: pick<string>(raw, 'kind') || '',
    reason: pick<string>(raw, 'reason') || '',
    manifestId: pick<string>(raw, 'manifestId', 'manifest_id') || '',
    manifestHash: pick<string>(raw, 'manifestHash', 'manifest_hash') || '',
    inputHash: pick<string>(raw, 'inputHash', 'input_hash') || '',
    revision: String(pick(raw, 'revision') ?? ''),
    asOf: pick<string>(raw, 'asOf', 'as_of') || '',
    createdAt: pick<string>(raw, 'createdAt', 'created_at') || '',
    startedAt: pick<string>(raw, 'startedAt', 'started_at') || '',
    finishedAt: pick<string>(raw, 'finishedAt', 'finished_at') || '',
    artifactHash: pick<string>(raw, 'artifactHash', 'artifact_hash') || '',
    cancelRequested: pick(raw, 'cancelRequested', 'cancel_requested') === true,
    result,
  }
}
export async function listResearchJobs(): Promise<ResearchJob[]> {
  const raw = await request('/api/backtest/research/jobs', { query: { limit: 100 } })
  const rows = pick<unknown[]>(raw, 'jobs')
  if (!Array.isArray(rows)) throw new Error('研究任务列表未返回')
  return rows.map(parseResearchJob)
}
export async function createResearchJob(draft: ResearchDraft) {
  return parseResearchJob(await request('/api/backtest/research/jobs', { method: 'POST', json: draft }))
}
export async function cancelResearchJob(id: string) {
  return parseResearchJob(
    await request(`/api/backtest/research/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST', json: {} }),
  )
}
