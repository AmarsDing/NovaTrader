import { reactive } from 'vue'
import { cancelOrder, confirmOrder } from '@/api/trade'
import { messageOf, pick } from '@/utils/format'

export const LIVE_CONFIRM_SEC = 30

export const confirmState = reactive({
  open: false,
  order: null,
  local: false,
  seconds: LIVE_CONFIRM_SEC,
  error: '',
  busy: false,
})

const queue = []
let current = null
let timer = null

export function askConfirm(order, local) {
  return new Promise((resolve) => {
    queue.push({ order, local, resolve })
    pump()
  })
}

function pump() {
  if (current || !queue.length) return
  current = queue.shift()
  confirmState.open = true
  confirmState.order = current.order
  confirmState.local = current.local
  confirmState.seconds = LIVE_CONFIRM_SEC
  confirmState.error = ''
  confirmState.busy = false
  clearInterval(timer)
  timer = setInterval(() => {
    if (!confirmState.open) return
    confirmState.seconds -= 1
    if (confirmState.seconds <= 0) {
      clearInterval(timer)
      timer = null
      void expireConfirm()
    }
  }, 1000)
}

function finish(result) {
  clearInterval(timer)
  timer = null
  const job = current
  current = null
  confirmState.open = false
  confirmState.order = null
  confirmState.busy = false
  job?.resolve(result)
  pump()
}

function orderId(order) {
  return pick(order, 'clientOrderId', 'client_order_id', 'id') || ''
}

export async function acceptConfirm() {
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

export async function dismissConfirm() {
  await voidCurrent('cancel')
}

async function expireConfirm() {
  await voidCurrent('void')
}

async function voidCurrent(result) {
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
