import type { WsTopic } from '@/ws/hub'
import type { MonitorInput } from './types'

/** Cursor acknowledgement must follow usable domain reads, including when the canvas is hidden. */
export function assertMonitorRecovery(input: MonitorInput, topics: readonly WsTopic[], applied: boolean, active: boolean, signal?: AbortSignal) {
  if (!active || signal?.aborted) throw new Error('监控恢复已取消')
  if (!applied ||
    (topics.includes('position') && (!input.account?.ok || !input.account.positionsOk || !input.orders?.ok || !input.fills?.ok || !input.risk?.ok)) ||
    (topics.includes('signal') && !input.signals?.ok) ||
    (topics.includes('alert') && (!input.risk?.ok || !input.notices?.ok)) ||
    (topics.includes('market') && (!input.sentiment?.ok || !input.sentiment.asOf || !input.sectors?.ok)) ||
    (topics.includes('brain') && !input.brainStats?.ok)) throw new Error('监控关键域尚未恢复')
}
