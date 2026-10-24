import { reactive } from 'vue'
import { cancelOrder, confirmOrder } from '@/api/trade'
import { messageOf, pick } from '@/utils/format'

export const LIVE_CONFIRM_SEC = 30

/** 确认结果：ok=确认放行，cancel=用户放弃，void=超时作废（撤单失败后仍按 void 收口）。 */
export type ConfirmResult = 'ok' | 'cancel' | 'void'

export const confirmState = reactive({
  open: false,
  order: null as object | null,
  local: false,
  seconds: LIVE_CONFIRM_SEC,
  error: '',
  busy: false,
})

interface ConfirmJob {
  order: object
  local: boolean
  resolve: (result: ConfirmResult) => void
}

const queue: ConfirmJob[] = []
let current: ConfirmJob | null = null
let timer: ReturnType<typeof setInterval> | null = null

export function askConfirm(order: object, local: boolean): Promise<ConfirmResult> {
  return new Promise((resolve) => {
    queue.push({ order, local, resolve })
    pump()
  })
}

function stopTimer(): void {
  if (timer !== null) clearInterval(timer)
  timer = null
}

function pump(): void {
  if (current || !queue.length) return
  const job = queue.shift() as ConfirmJob
  current = job
  confirmState.open = true
  confirmState.order = job.order
  confirmState.local = job.local
  confirmState.seconds = LIVE_CONFIRM_SEC
  confirmState.error = ''
  confirmState.busy = false
  stopTimer()
  timer = setInterval(() => {
    if (!confirmState.open) return
    confirmState.seconds -= 1
    if (confirmState.seconds <= 0) {
      stopTimer()
      void expireConfirm()
    }
  }, 1000)
}

function finish(result: ConfirmResult): void {
  stopTimer()
  const job = current
  current = null
  confirmState.open = false
  confirmState.order = null
  confirmState.busy = false
  job?.resolve(result)
  pump()
}

function orderId(order: object): string {
  return pick<string>(order, 'clientOrderId', 'client_order_id', 'id') || ''
}

export async function acceptConfirm(): Promise<void> {
  if (!current || confirmState.busy) return
  const job = current
  confirmState.busy = true
  confirmState.error = ''
  try {
    if (!job.local) {
      const id = orderId(job.order)
      if (!id) throw new Error('缺少委托编号')
      await confirmOrder(id)
    }
    if (current === job) finish('ok')
  } catch (error) {
    if (current !== job) return
    confirmState.error = messageOf(error)
    confirmState.busy = false
  }
}

export async function dismissConfirm(): Promise<void> {
  await voidCurrent('cancel')
}

async function expireConfirm(): Promise<void> {
  await voidCurrent('void')
}

async function voidCurrent(result: ConfirmResult): Promise<void> {
  if (!current) return
  if (confirmState.error && result === 'cancel') {
    finish(result)
    return
  }
  if (confirmState.busy) return
  const job = current
  confirmState.busy = true
  confirmState.error = ''
  try {
    if (!job.local) {
      const id = orderId(job.order)
      if (id) await cancelOrder(id)
    }
    if (current === job) finish(result)
  } catch (error) {
    if (current !== job) return
    confirmState.error = `${messageOf(error)}。再点一次放弃可关闭。`
    confirmState.busy = false
    if (result === 'void') finish('void')
  }
}
