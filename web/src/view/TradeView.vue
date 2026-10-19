<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { askConfirm } from '@/store/confirm'
import { useAccountStore } from '@/stores/account'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import { getAccount } from '@/api/trade'
import { getRiskState, previewOrder, type PreviewBody, type PreviewReply, type RiskStateView } from '@/api/risk'
import { getSignal } from '@/api/signals'
import { onPush, onResync } from '@/ws/hub'
import { createPageRefresh } from './page-refresh'
import {
  cancelOrder,
  getChannelStatus,
  getOrder,
  listFills,
  listOrders,
  listPositions,
  placeOrder,
  type ChannelStatus,
  type Fill,
  type Order,
  type Position,
  type AccountSnapshot,
  type OrderDraft,
} from '@/api/trade'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import {
  amount,
  canCancelOrder,
  messageOf,
  orderStatusLabel,
  pnlClass,
  pnlSign,
  price,
  ratioPct,
  sideLabel,
} from '@/utils/format'
import { router } from '@/router'
import { consumePanelParams, onPanelParams } from '@/workspace/panel-bus'
import { getWorkbench } from '@/workspace/inject'
import { PAPER_DAYS_REQUIRED } from './decision-desk'
import PaperAdmission from './PaperAdmission.vue'
import { draftPrice, normalizeBook, pendingTradeKey, readPendingTrade, signalContextOf, signalDraftBlock, type SignalDraftContext } from './trade-draft'

const auth = useAuthStore()
const accountStore = useAccountStore()
const riskStore = useRiskStore()

const account = ref('SIM')
const positions = ref<Position[]>([])
const orders = ref<Order[]>([])
const fills = ref<Fill[]>([])
const error = ref('')
const hint = ref('')
const loading = ref(false)
const submitting = ref(false)
const accountSnapshot = ref<AccountSnapshot | null>(null)
const riskSnapshot = ref<RiskStateView | null>(null)
// G7 执行通道状态（sim）：读取失败或清单为空一律按「状态未知」呈现，绝不沿用上一次的值虚报。
const channelStatus = ref<ChannelStatus | null>(null)
const positionsOk = ref(false)
const ordersOk = ref(false)
const fillsOk = ref(false)
const readAt = ref(0)
const clockMs = ref(Date.now())
const signalContext = ref<SignalDraftContext | null>(null)
/** 已核实的本会话信号委托；列表短暂延迟不能使同一计划再次提交。 */
const acknowledgedSignals = reactive(new Set<string>())
/** 请求结果不明时先用原ID查回报，不能点第二次生成另一张单。 */
const uncertainDraft = ref<OrderDraft | null>(null)
const journalError = ref('')
const journalKey = pendingTradeKey(auth.operatorName)
try {
  uncertainDraft.value = readPendingTrade(localStorage, journalKey)
} catch {
  journalError.value = '待核对委托记录无法读取，暂不能提交新委托'
}
let readSequence = 0
let disposed = false
let contextVersion = 0
const refreshKey = () => `${account.value}:${contextVersion}`
const refreshes = createPageRefresh({ key: refreshKey, read: readPage, pushIntervalMs: 0 })
let pollTimer: ReturnType<typeof setInterval> | null = null
let clockTimer: ReturnType<typeof setInterval> | null = null

/** 只清除自己已核实的记录，另一面板写入的待核对委托继续保留。 */
function settleJournal(clientOrderId: string) {
  const pending = readPendingTrade(localStorage, journalKey)
  if (pending?.clientOrderId === clientOrderId) localStorage.removeItem(journalKey)
  uncertainDraft.value = readPendingTrade(localStorage, journalKey)
}

function rememberSignalOrder(draft: OrderDraft, order: Order) {
  if (draft.signalId && ((order.filled ?? 0) > 0 || !['rejected', 'cancelled', 'canceled', 'expired'].includes(order.status.toLowerCase()))) {
    acknowledgedSignals.add(`${normalizeBook(draft.account)}:${draft.signalId}`)
  }
}
const orderFilter = ref<'open' | 'all'>('open')
const touched = ref(false)
const cancelAllBusy = ref(false)
/** 一键全撤第一步：再点一次才执行（8 秒内有效）。 */
const cancelAllArmed = ref(false)
let cancelAllTimer: ReturnType<typeof setTimeout> | null = null
const form = reactive<{ symbol: string; side: 'buy' | 'sell'; price: string; volume: string }>({
  symbol: '',
  side: 'buy',
  price: '',
  volume: '',
})

/** G7 干跑试算：表单合法后自动预演摘要；提交时仍以一次全新试算为准，不沿用这里的结果。 */
const preview = ref<PreviewReply | null>(null)
const previewIssue = ref('')
let previewTimer: ReturnType<typeof setTimeout> | null = null
let previewSequence = 0

// 连续运行报告只提供证据，不授予权限；当前真实文件通道尚未验收。
const liveAllowed = computed(() => false)
const accountOptions = computed(() => [
  { value: 'SIM', label: 'SIM 模拟' },
  {
    value: 'LIVE',
    label: `LIVE 实盘（连续 ${PAPER_DAYS_REQUIRED} 日证据及通道另行验收）`,
    disabled: !liveAllowed.value,
  },
])
const sideOptions = [
  { value: 'buy', label: '买入' },
  { value: 'sell', label: '卖出' },
]

interface PosRow extends Record<string, unknown> {
  symbol: string
  quantity: number | null
  available: number | null
  avgCost: number | null
  lastPrice: number | null
  marketValue: number | null
  pnl: number | null
  pnlPct: number | null
}
interface OrderRow extends Record<string, unknown> {
  id: string
  symbol: string
  side: string
  status: string
  price: number | null
  volume: number | null
  filled: number | null
}
interface FillRow extends Record<string, unknown> {
  id: string
  time: string
  symbol: string
  side: string
  price: number | null
  qty: number | null
  amount: number | null
}

const posColumns: NtTableColumn<PosRow>[] = [
  { key: 'symbol', title: '代码' },
  { key: 'quantity', title: '数量', align: 'right' },
  { key: 'available', title: '可卖', align: 'right' },
  { key: 'avgCost', title: '成本', align: 'right' },
  { key: 'lastPrice', title: '现价', align: 'right' },
  { key: 'marketValue', title: '市值', align: 'right' },
  { key: 'pnl', title: '浮动盈亏', align: 'right' },
  { key: 'pnlPct', title: '盈亏%', align: 'right' },
]
const orderColumns: NtTableColumn<OrderRow>[] = [
  { key: 'id', title: '编号' },
  { key: 'symbol', title: '代码' },
  { key: 'side', title: '方向' },
  { key: 'status', title: '状态' },
  { key: 'price', title: '价格', align: 'right' },
  { key: 'volume', title: '数量', align: 'right' },
  { key: 'filled', title: '已成', align: 'right' },
  { key: 'actions', title: '', width: '70px' },
]
const fillColumns: NtTableColumn<FillRow>[] = [
  { key: 'time', title: '时间' },
  { key: 'symbol', title: '代码' },
  { key: 'side', title: '方向' },
  { key: 'price', title: '价格', align: 'right' },
  { key: 'qty', title: '数量', align: 'right' },
  { key: 'amount', title: '金额', align: 'right' },
]

const posRows = computed<PosRow[]>(() =>
  positions.value.map((p) => {
    const marketValue = p.lastPrice != null && p.quantity != null ? p.lastPrice * p.quantity : null
    const cost = p.avgCost != null && p.quantity != null ? p.avgCost * p.quantity : null
    const pnl = marketValue != null && cost != null ? marketValue - cost : null
    const pnlPct = pnl != null && cost ? pnl / cost : null
    return { ...p, marketValue, pnl, pnlPct }
  }),
)
const orderRows = computed<OrderRow[]>(() => {
  const rows = orders.value.map((o) => ({ ...o, actions: '' }) as OrderRow & { actions: string })
  if (orderFilter.value === 'open') return rows.filter((row) => canCancelOrder(row.status))
  return rows
})
const hiddenTerminal = computed(
  () => orderFilter.value === 'open' && orders.value.some((row) => !canCancelOrder(row.status)),
)
const openOrders = computed(() => orders.value.filter((row) => canCancelOrder(row.status)))
const fillRows = computed<FillRow[]>(() => fills.value.map((f) => ({ ...f })))
/** 当日成交合计（费用字段后端暂无，合计只算成交额）。 */
const fillSummary = computed(() => {
  if (!fills.value.length) return null
  let buy = 0
  let sell = 0
  for (const f of fills.value) {
    const amt = f.amount ?? (f.price != null && f.qty != null ? f.price * f.qty : 0)
    if (f.side === 'buy') buy += amt
    else if (f.side === 'sell') sell += amt
  }
  return { count: fills.value.length, buy, sell, net: sell - buy }
})

const priceNum = computed(() => Number(form.price))
const volumeNum = computed(() => Number(form.volume))
const symbolOk = computed(() => /^\d{6}\.(SZ|SH|BJ)$/i.test(form.symbol.trim()))
const priceOk = computed(() => Number.isFinite(priceNum.value) && priceNum.value > 0)
const volumeOk = computed(() => Number.isInteger(volumeNum.value) && volumeNum.value >= 100 && volumeNum.value % 100 === 0)
const signalBlock = computed(() => signalDraftBlock(signalContext.value, { account: account.value, symbol: form.symbol, side: form.side }, clockMs.value))
const signalOrderBlock = computed(() => {
  const context = signalContext.value
  if (!context) return ''
  const key = `${context.book}:${context.signalId}`
  if (acknowledgedSignals.has(key)) return '此信号已有已核实委托，请查看原委托与持仓，不能重复执行'
  const existing = orders.value.find((order) => order.signalId === context.signalId &&
    (!normalizeBook(order.account) || normalizeBook(order.account) === context.book) &&
    ((order.filled ?? 0) > 0 || !['rejected', 'cancelled', 'canceled', 'expired'].includes(order.status.toLowerCase())))
  return existing ? `此信号已有关联委托 ${existing.id}（${orderStatusLabel(existing.status)}），请先核对原委托` : ''
})
const submissionBlock = computed(() => {
  if (auth.isViewer()) return '当前是查看角色，不能下单'
  if (journalError.value) return journalError.value
  if (signalBlock.value) return signalBlock.value
  if (uncertainDraft.value) return '上一笔委托结果待核对，暂不重复提交'
  if (signalOrderBlock.value) return signalOrderBlock.value
  if (!accountSnapshot.value || accountSnapshot.value.cash == null || accountSnapshot.value.equity == null || !positionsOk.value || !ordersOk.value || !riskSnapshot.value || !readAt.value || clockMs.value - readAt.value > 30_000) return '账户、持仓、委托或风控状态未读到最新结果，暂不能提交'
  if (!['L0', 'L1', 'L2', 'L3'].includes(riskSnapshot.value.mode)) return '运行授权未知，暂不能提交'
  if (riskSnapshot.value.engineReady === false || riskSnapshot.value.configReady === false) return '风控引擎或配置尚未就绪，暂不能提交'
  if (normalizeBook(accountSnapshot.value.account) !== normalizeBook(account.value)) return '账户响应与当前账本不一致，暂不能提交'
  if (form.side === 'buy' && accountSnapshot.value.openHalted) return accountSnapshot.value.haltReason || '账户已停止新开仓'
  if (form.side === 'buy' && (riskSnapshot.value.killActive || riskSnapshot.value.mode === 'L0')) return '当前只允许处理持仓，不能新买入'
  if (account.value === 'LIVE' && !liveAllowed.value) return '实盘准入状态未通过'
  if (account.value === 'LIVE') return '实盘文件单通道尚未验收，本轮仅支持模拟委托'
  // G7：表单对应的试算拦截时下按钮一并禁提交（提交时还会以全新试算再拦截一次）。
  if (preview.value && !preview.value.approved) return `风控试算未放行${preview.value.blockRule ? `（${preview.value.blockRule}）` : ''}：${preview.value.reason || '请调整委托后重试'}`
  return ''
})
const canSubmit = computed(() => !submissionBlock.value && !submitting.value && !loading.value && symbolOk.value && priceOk.value && volumeOk.value)
const estimate = computed(() => (priceOk.value && volumeNum.value > 0 ? priceNum.value * volumeNum.value : null))
const buyable = computed(() => {
  const cash = accountSnapshot.value?.cash
  if (!priceOk.value || cash == null) return null
  return Math.floor(cash / priceNum.value / 100) * 100
})
/** 持仓里这只票的现价/成本，下单时作参照。 */
const positionRef = computed(() => {
  const code = form.symbol.trim().toUpperCase()
  if (!code) return null
  return positions.value.find((p) => p.symbol.toUpperCase() === code) ?? null
})
/** 试算结果里未放行的规则（展示明细）。 */
const previewFailed = computed(() => (preview.value?.hits ?? []).filter((hit) => !hit.pass))

// 模拟通道徽标（G7）：信息性提示，不接管提交闸门（闸门是风控试算）。
const channelPill = computed(() => {
  const c = channelStatus.value
  if (!c) return { kind: 'unknown', text: '模拟通道：状态未知', title: '通道状态读取失败或服务端未跟踪' }
  const since = c.since ? ` · 自 ${c.since}` : ''
  switch (c.status) {
    case 'connected': return { kind: 'ok', text: '模拟通道：正常', title: `撮合账本心跳正常${since}` }
    case 'degraded': return { kind: 'warn', text: '模拟通道：降级', title: `快照回路连续失败，恢复中${since}` }
    case 'down': return { kind: 'danger', text: '模拟通道：断开', title: `快照回路持续失败${since}` }
    default: return { kind: 'dim', text: '模拟通道：未连接', title: `引擎尚未完成首次心跳${since}` }
  }
})

function previewBodyFor(clientOrderId: string, symbol: string, side: 'buy' | 'sell', px: number, vol: number): PreviewBody {
  return { client_order_id: clientOrderId, account_type: account.value, symbol, side, price: px, volume: vol, source: 'manual', operator: auth.operatorName }
}

/** 表单停笔 250ms 后预演一次（预览不落单、不占预约）。序列号丢弃过期响应。 */
async function runPreview(sequence: number) {
  const body = previewBodyFor(`preview-${crypto.randomUUID()}`, form.symbol.trim(), form.side, priceNum.value, volumeNum.value)
  try {
    const result = await previewOrder(body)
    if (disposed || sequence !== previewSequence) return
    previewIssue.value = ''
    preview.value = result
  } catch (err) {
    if (disposed || sequence !== previewSequence) return
    preview.value = null
    previewIssue.value = `风控试算暂不可用：${messageOf(err)}；提交时会重新试算`
  }
}

watch([() => form.symbol, () => form.side, () => form.price, () => form.volume, account], () => {
  preview.value = null
  previewIssue.value = ''
  previewSequence += 1
  if (previewTimer) clearTimeout(previewTimer)
  previewTimer = null
  if (!symbolOk.value || !priceOk.value || !volumeOk.value) return
  const sequence = previewSequence
  previewTimer = setTimeout(() => { void runPreview(sequence) }, 250)
})

watch(
  account,
  (value) => {
    contextVersion += 1
    // 实盘未达准入时不允许账本停在 LIVE：下拉 option 的 disabled 挡不住 v-model 赋值，
    // 持久化的 book='LIVE' 会在挂载时写回来，这里统一纠回 SIM。
    if (value === 'LIVE' && !liveAllowed.value) {
      hint.value = '实盘执行权限尚未开放，已切回 SIM；连续运行证据见下方核验'
      account.value = 'SIM'
      return
    }
    accountStore.book = value
    accountSnapshot.value = null
    riskSnapshot.value = null
    readAt.value = 0
    positions.value = []
    orders.value = []
    fills.value = []
    positionsOk.value = ordersOk.value = fillsOk.value = false
    void accountStore.refresh()
    void load()
  },
  { immediate: true, flush: 'sync' },
)

function load() { return refreshes.refresh() }

async function readPage(key: string): Promise<boolean> {
  const book = account.value
  const sequence = ++readSequence
  loading.value = true
  error.value = ''
  const currentAccount = getAccount(book)
  const [pos, ord, fill, acc, riskRes, chan] = await Promise.allSettled([
    listPositions(book),
    listOrders(book),
    listFills(book),
    currentAccount,
    getRiskState(),
    getChannelStatus(),
  ])
  if (disposed || key !== refreshKey() || sequence !== readSequence || account.value !== book) return false
  positionsOk.value = pos.status === 'fulfilled'
  ordersOk.value = ord.status === 'fulfilled'
  fillsOk.value = fill.status === 'fulfilled'
  const channels = chan.status === 'fulfilled' ? chan.value : []
  channelStatus.value = channels.find((c) => c.channel === 'sim') ?? null
  positions.value = pos.status === 'fulfilled' ? pos.value : []
  orders.value = ord.status === 'fulfilled' ? ord.value : []
  fills.value = fill.status === 'fulfilled' ? fill.value : []
  accountSnapshot.value = acc.status === 'fulfilled' && normalizeBook(acc.value.account) === normalizeBook(book) &&
    acc.value.equity != null && acc.value.cash != null ? acc.value : null
  riskSnapshot.value = riskRes.status === 'fulfilled' && ['L0', 'L1', 'L2', 'L3'].includes(riskRes.value.mode) ? riskRes.value : null
  readAt.value = Date.now()
  clockMs.value = Date.now()
  if (uncertainDraft.value && orders.value.some((order) => order.id === uncertainDraft.value?.clientOrderId)) {
    try {
      const foundId = uncertainDraft.value.clientOrderId
      rememberSignalOrder(uncertainDraft.value, orders.value.find((order) => order.id === foundId)!)
      settleJournal(foundId)
      hint.value = `已查到委托 ${foundId}，请查看实际状态`
    } catch {
      journalError.value = '已查到委托，但本地核对记录未能清除'
    }
  }
  const failed = [pos, ord, fill, acc, riskRes].find((item) => item.status === 'rejected')
  if (failed) error.value = messageOf((failed as PromiseRejectedResult).reason)
  const ready = positionsOk.value && ordersOk.value && fillsOk.value && accountSnapshot.value != null && riskSnapshot.value != null
  if (!ready && !failed) error.value = '账户或风控核心字段未完成核对'
  loading.value = false
  return ready
}

async function submit() {
  if (submitting.value || uncertainDraft.value) return
  hint.value = ''
  error.value = ''
  if (auth.isViewer()) {
    error.value = '当前是查看角色，不能下单'
    return
  }
  touched.value = true
  if (!symbolOk.value || !priceOk.value || !volumeOk.value) {
    error.value = '请填写 6 位代码（如 600519.SH）、正限价，以及 100 股整数倍'
    return
  }
  const draft: OrderDraft = {
    clientOrderId: `manual-${crypto.randomUUID()}`,
    account: account.value,
    symbol: form.symbol.trim(),
    side: form.side,
    price: priceNum.value,
    volume: volumeNum.value,
    source: 'manual',
    operator: auth.operatorName,
    signalId: signalContext.value?.signalId,
    strategyVersion: signalContext.value?.strategyVersionId,
  }
  const originalContext = signalContext.value
  let postStarted = false
  submitting.value = true
  try {
    await load()
    if (disposed) return
    if (submissionBlock.value) throw new Error(submissionBlock.value)
    if (draft.account === 'LIVE') {
      const result = await askConfirm(draft, true)
      if (result !== 'ok') {
        hint.value = '实盘确认已作废，委托未提交'
        return
      }
      await load()
      if (disposed) return
    }
    if (draft.account !== account.value || draft.symbol !== form.symbol.trim() || draft.side !== form.side || draft.price !== priceNum.value || draft.volume !== volumeNum.value || originalContext !== signalContext.value) {
      throw new Error('确认期间表单或信号已变化，请重新核对')
    }
    clockMs.value = Date.now()
    if (submissionBlock.value) throw new Error(submissionBlock.value)
    if (originalContext) {
      const signal = await getSignal(originalContext.signalId)
      if (disposed) return
      if (signal.status.toLowerCase() !== 'approved') throw new Error('信号不再处于可确认状态，请查看最新计划')
      if (normalizeBook(signal.book) !== originalContext.book || signal.symbol.toUpperCase() !== originalContext.symbol || signal.side !== originalContext.side || signal.strategyVersionId !== originalContext.strategyVersionId) throw new Error('信号身份或策略版本已变化，请重新选择')
      const blocked = signalDraftBlock({ ...originalContext, validUntil: signal.validUntil }, draft, Date.now())
      if (blocked) throw new Error(blocked)
      if (signal.side === 'buy' && (signal.entryLow == null || signal.entryHigh == null || draft.price < signal.entryLow || draft.price > signal.entryHigh)) throw new Error('限价不在当前信号的入场区间，请重新核对计划')
    }
    if (draft.account !== account.value || draft.symbol !== form.symbol.trim() || draft.side !== form.side || draft.price !== priceNum.value || draft.volume !== volumeNum.value || originalContext !== signalContext.value) throw new Error('检查期间表单已变化，请重新核对')
    if (disposed) return
    clockMs.value = Date.now()
    if (submissionBlock.value) throw new Error(submissionBlock.value)
    // G7：落单前必跑一次全新的干跑试算（同草稿编号，只预演不留痕）。拦截或不可用都不生成委托。
    let pv: PreviewReply
    try {
      pv = await previewOrder(previewBodyFor(draft.clientOrderId, draft.symbol, draft.side, draft.price, draft.volume))
    } catch (err) {
      throw new Error(`风控试算未返回，暂不能提交：${messageOf(err)}`, { cause: err })
    }
    if (disposed) return
    preview.value = pv
    previewIssue.value = ''
    if (!pv.approved) throw new Error(`风控试算未放行${pv.blockRule ? `（${pv.blockRule}）` : ''}：${pv.reason || '未给出原因'}`)
    if (draft.account !== account.value || draft.symbol !== form.symbol.trim() || draft.side !== form.side || draft.price !== priceNum.value || draft.volume !== volumeNum.value || originalContext !== signalContext.value) throw new Error('试算期间表单已变化，请重新核对')
    if (disposed) return
    const pending = readPendingTrade(localStorage, journalKey)
    if (pending) {
      uncertainDraft.value = pending
      throw new Error('已有委托结果待核对，未创建新委托')
    }
    localStorage.setItem(journalKey, JSON.stringify(draft))
    uncertainDraft.value = draft
    postStarted = true
    const placed = await placeOrder(draft)
    if (placed.id !== draft.clientOrderId || !placed.status) throw new Error('委托响应缺少对应编号或状态')
    rememberSignalOrder(draft, placed)
    settleJournal(draft.clientOrderId)
    hint.value = `委托：${orderStatusLabel(placed.status)}${placed.reason ? ` · ${placed.reason}` : ''}`
    await load()
    await accountStore.refresh()
  } catch (err) {
    const status = (err as { status?: number } | null)?.status
    if (postStarted && status != null && [400, 401, 403, 404, 422].includes(status)) {
      try {
        settleJournal(draft.clientOrderId)
      } catch {
        journalError.value = '本地核对记录未能清除，请先核对原委托'
      }
    }
    error.value = uncertainDraft.value ? `${messageOf(err)} · 委托结果待核对，请先查原编号` : messageOf(err)
  } finally {
    submitting.value = false
  }
}

async function reconcileSubmission() {
  const draft = uncertainDraft.value
  if (!draft || submitting.value) return
  submitting.value = true
  try {
    const order = await getOrder(draft.clientOrderId)
    if (order.id !== draft.clientOrderId || !order.status) throw new Error('返回的委托编号或状态无法核实')
    rememberSignalOrder(draft, order)
    settleJournal(draft.clientOrderId)
    hint.value = `已查到原委托：${orderStatusLabel(order.status)}`
    await load()
  } catch (err) {
    error.value = `${messageOf(err)} · 原委托结果仍未核实，未生成新委托`
  } finally {
    submitting.value = false
  }
}

async function cancel(id: string) {
  error.value = ''
  try {
    await cancelOrder(id)
    await load()
  } catch (err) {
    error.value = messageOf(err)
  }
}

/** 一键全撤：第一次点进入待确认态，8 秒内再点才执行（不用原生 confirm）。 */
async function cancelAll() {
  const list = openOrders.value
  if (!list.length) return
  if (!cancelAllArmed.value) {
    cancelAllArmed.value = true
    if (cancelAllTimer) clearTimeout(cancelAllTimer)
    cancelAllTimer = setTimeout(() => {
      cancelAllArmed.value = false
    }, 8000)
    return
  }
  cancelAllArmed.value = false
  if (cancelAllTimer) clearTimeout(cancelAllTimer)
  cancelAllBusy.value = true
  error.value = ''
  hint.value = ''
  let ok = 0
  let failed = 0
  for (const order of list) {
    try {
      await cancelOrder(order.id)
      ok += 1
    } catch {
      failed += 1
    }
  }
  cancelAllBusy.value = false
  hint.value = `已提交撤单 ${ok} 笔${failed ? `，${failed} 笔失败` : ''}`
  await load()
}

function openMarket(symbol: string) {
  getWorkbench()?.openPanel('market', { symbol })
}

/** 面板参数落表：挂载预填与已挂载时的实时推送共用。只应用 params 里带了的字段。 */
function applyOrderParams(params: Record<string, unknown>) {
  const symbol = params.symbol
  const side = params.side
  const px = params.price
  const nextContext = signalContextOf(params)
  if (typeof symbol === 'string' && symbol) {
    const changed = form.symbol.toUpperCase() !== symbol.trim().toUpperCase()
    form.symbol = symbol.trim().toUpperCase()
    if (changed || nextContext) {
      form.price = ''
      form.volume = ''
    }
    signalContext.value = nextContext
    if (nextContext?.book && nextContext.book !== account.value) account.value = nextContext.book
  }
  if (side === 'buy' || side === 'sell') form.side = side
  if (Object.prototype.hasOwnProperty.call(params, 'price')) form.price = draftPrice(px)
}

watch([() => form.symbol, () => form.side], () => {
  const context = signalContext.value
  if (context && (context.symbol !== form.symbol.trim().toUpperCase() || context.side !== form.side)) {
    signalContext.value = null
    form.price = ''
    form.volume = ''
    hint.value = '证券或方向已修改，已解除原信号关联并清空旧价格；当前为手动委托'
  }
})

const offPanelParams = onPanelParams('trade', applyOrderParams)

onMounted(() => {
  // 预填：面板参数优先；路由 query 兜底（面板不在 RouterView 下，勿用 useRoute）。
  const panelParams = consumePanelParams('trade') ?? {}
  const q = router.currentRoute.value.query
  applyOrderParams({
    ...panelParams,
    symbol: panelParams.symbol ?? q.symbol,
    side: panelParams.side ?? q.side,
    price: panelParams.price ?? q.price,
  })
  if (!signalContext.value?.book && (accountStore.book || 'SIM') !== account.value) account.value = accountStore.book
  pollTimer = setInterval(() => {
    if (!document.hidden) void load()
  }, 10_000)
  clockTimer = setInterval(() => { clockMs.value = Date.now() }, 1000)
  document.addEventListener('visibilitychange', onVisible)
})

function onVisible() {
  if (!document.hidden) { clockMs.value = Date.now(); void load() }
}

const offPush = onPush(async (message) => {
  if (!['position', 'alert', 'resume'].includes(message.topic)) return
  const key = refreshKey()
  const ready = await load()
  if (!disposed && key === refreshKey() && !ready) throw new Error('交易面板账户或风控状态尚未恢复')
})
const offResync = onResync(['position', 'alert'], async () => {
  const key = refreshKey()
  const ready = await load()
  if (!disposed && key === refreshKey() && !ready) throw new Error('交易面板账户或风控状态尚未恢复')
})

onBeforeUnmount(() => {
  disposed = true
  contextVersion += 1
  previewSequence += 1
  refreshes.dispose()
  readSequence += 1
  offPanelParams()
  offPush()
  offResync()
  document.removeEventListener('visibilitychange', onVisible)
  if (pollTimer) clearInterval(pollTimer)
  if (clockTimer) clearInterval(clockTimer)
  if (cancelAllTimer) clearTimeout(cancelAllTimer)
  if (previewTimer) clearTimeout(previewTimer)
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">交易</h1>
        <p class="nt-note">
          B 买入 · S 卖出 · Shift+K 急停。实盘单先确认。股数按 100 股一手。
          <span v-if="riskStore.buysToday != null" class="head-meta">今日买入 {{ riskStore.buysToday }}/5</span>
          <span class="channel-pill" :class="channelPill.kind" :title="channelPill.title">{{ channelPill.text }}</span>
        </p>
      </div>
      <NtSelect v-model="account" label="账户" :options="accountOptions" />
    </header>

    <form class="nt-form nt-panel" @submit.prevent="submit">
      <label>
        <span>代码</span>
        <NtInput v-model="form.symbol" placeholder="600519.SH" mono :invalid="touched && !symbolOk" />
      </label>
      <NtSelect v-model="form.side" label="方向" :options="sideOptions" />
      <label>
        <span>限价</span>
        <NtInput v-model="form.price" type="number" mono :invalid="touched && !priceOk" />
      </label>
      <label>
        <span>股数</span>
        <NtInput v-model="form.volume" type="number" mono :invalid="touched && !volumeOk" />
      </label>
      <NtButton type="submit" variant="success" :disabled="!canSubmit" :loading="submitting">提交委托</NtButton>
    </form>
    <p v-if="signalContext" class="nt-note">信号 {{ signalContext.signalId || '编号未返回' }} · 策略版本 {{ signalContext.strategyVersionId || '未返回' }} · {{ signalContext.book || '账户未返回' }} · 有效至 {{ signalContext.validUntil || '未返回' }}。提交前重新核验，仍须正式风控。</p>
    <p v-if="submissionBlock" class="nt-note">{{ submissionBlock }}</p>
    <NtButton v-if="uncertainDraft" size="sm" :loading="submitting" @click="reconcileSubmission">核对原委托结果</NtButton>
    <p class="quote">
      可用 {{ amount(accountSnapshot?.cash) }}
      · 预估 {{ estimate == null ? '—' : amount(estimate) }}
      · 可买 {{ buyable == null ? '—' : `${buyable} 股` }}
      <template v-if="positionRef">
        · 现价 {{ price(positionRef.lastPrice) }} / 成本 {{ price(positionRef.avgCost) }}
      </template>
    </p>
    <section v-if="preview" class="preview" :class="{ blocked: !preview.approved }">
      <p class="preview-line">
        <template v-if="preview.approved">风控试算可下</template>
        <template v-else>风控试算拦截{{ preview.blockRule ? ` ${preview.blockRule}` : '' }}</template>
        · 金额 {{ amount(preview.amount) }} · 费用 {{ amount(preview.fee) }} · 现金影响 {{ amount(preview.cashDelta) }}
        <span v-if="preview.approved && preview.adjusted && preview.volume != null" class="shrink">· 缩量放行 {{ preview.volume }} 股</span>
      </p>
      <p v-if="!preview.approved && preview.reason" class="preview-reason">{{ preview.reason }}</p>
      <p v-if="previewFailed.length" class="preview-fails">
        未过规则：<span v-for="hit in previewFailed" :key="hit.code" class="fail">{{ hit.code }} {{ hit.name }}</span>
      </p>
    </section>
    <p v-else-if="previewIssue" class="nt-note">{{ previewIssue }}</p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="hint" class="nt-note">{{ hint }}</p>
    <PaperAdmission />

    <section>
      <h2 class="nt-panel__title">持仓</h2>
      <NtTable :columns="posColumns" :rows="posRows" row-key="symbol" :empty="loading ? '读取中…' : positionsOk ? '暂无持仓' : '持仓未返回，不能当作空仓'">
        <template #cell-symbol="{ value }">
          <button type="button" class="sym" @click="openMarket(String(value))">{{ value }}</button>
        </template>
        <template #cell-avgCost="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-lastPrice="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-marketValue="{ value }">{{ amount(value as number | null) }}</template>
        <template #cell-pnl="{ value }">
          <span :class="pnlClass(value)">{{ pnlSign(value) }}{{ amount(value) }}</span>
        </template>
        <template #cell-pnlPct="{ value }">
          <span :class="pnlClass(value)">{{ ratioPct(value) }}</span>
        </template>
      </NtTable>
    </section>

    <section>
      <h2 class="nt-panel__title">委托</h2>
      <div class="filters">
        <button type="button" :class="{ on: orderFilter === 'open' }" @click="orderFilter = 'open'">可撤</button>
        <button type="button" :class="{ on: orderFilter === 'all' }" @click="orderFilter = 'all'">全部</button>
        <span v-if="hiddenTerminal" class="nt-note">终态已收起</span>
        <NtButton
          v-if="openOrders.length && !auth.isViewer()"
          size="sm"
          variant="danger"
          :loading="cancelAllBusy"
          class="cancel-all"
          @click="cancelAll"
        >
          {{ cancelAllArmed ? `确认撤销全部 ${openOrders.length} 笔？` : `一键全撤（${openOrders.length}）` }}
        </NtButton>
      </div>
      <NtTable
        :columns="orderColumns"
        :rows="orderRows"
        row-key="id"
        :empty="loading ? '读取中…' : !ordersOk ? '委托未返回，不能当作无委托' : orderFilter === 'open' ? '没有可撤委托' : '暂无委托'"
      >
        <template #cell-side="{ value }">
          <span :class="value === 'buy' ? 'up' : 'down'">{{ sideLabel(value) }}</span>
        </template>
        <template #cell-status="{ value }">{{ orderStatusLabel(value) }}</template>
        <template #cell-price="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-actions="{ row }">
          <NtButton v-if="canCancelOrder(row.status)" size="sm" @click="cancel(String(row.id))">撤单</NtButton>
        </template>
      </NtTable>
    </section>

    <section>
      <h2 class="nt-panel__title">成交</h2>
      <p v-if="fillSummary" class="nt-note fills-sum">
        当日成交 {{ fillSummary.count }} 笔 · 买入 {{ amount(fillSummary.buy) }} · 卖出
        {{ amount(fillSummary.sell) }} · 净额
        <span :class="pnlClass(fillSummary.net)">{{ pnlSign(fillSummary.net) }}{{ amount(fillSummary.net) }}</span>
      </p>
      <NtTable :columns="fillColumns" :rows="fillRows" :empty="loading ? '读取中…' : fillsOk ? '暂无成交明细' : '成交明细未返回'">
        <template #cell-side="{ value }">
          <span :class="value === 'buy' ? 'up' : 'down'">{{ sideLabel(value) }}</span>
        </template>
        <template #cell-price="{ value }">{{ price(value as number | null) }}</template>
        <template #cell-amount="{ value }">{{ amount(value as number | null) }}</template>
      </NtTable>
    </section>
  </div>
</template>

<style scoped>
h2 {
  margin: 4px 0 8px;
}

.head-meta {
  margin-left: 6px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-2);
}

.channel-pill {
  margin-left: 8px;
  padding: 0 8px;
  border-radius: 999px;
  border: 1px solid var(--nt-border-1);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
}

.channel-pill.ok { color: var(--nt-success); border-color: var(--nt-success); }
.channel-pill.warn { color: var(--nt-warn); border-color: var(--nt-warn); }
.channel-pill.danger { color: var(--nt-danger); border-color: var(--nt-danger); }
.channel-pill.dim, .channel-pill.unknown { color: var(--nt-text-3); border-color: var(--nt-border-1); }

.sym {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-accent);
  font: inherit;
  cursor: pointer;
}

.sym:hover {
  text-decoration: underline;
}

.cancel-all {
  margin-left: auto;
}

.fills-sum {
  margin: 0 0 6px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.quote {
  margin: 0;
  font-size: 12px;
  color: var(--nt-text-2);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.preview {
  margin: 0;
  padding: 6px 8px;
  font-size: 12px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
}

.preview.blocked {
  border-color: var(--nt-danger);
}

.preview-line {
  margin: 0;
  color: var(--nt-text-2);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.preview.blocked .preview-line,
.preview-reason {
  color: var(--nt-danger);
}

.preview-reason {
  margin: 4px 0 0;
}

.preview-fails {
  margin: 4px 0 0;
  color: var(--nt-text-2);
}

.preview-fails .fail {
  margin-right: 8px;
  color: var(--nt-danger);
  font-family: var(--nt-font-mono);
}

.preview .shrink {
  color: var(--nt-warn);
}

.filters {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 8px;
}

.filters button {
  min-height: 26px;
  padding: 0 10px;
  font-size: 12px;
  color: var(--nt-text-2);
  background: transparent;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.filters button.on {
  color: var(--nt-bg-0);
  background: var(--nt-accent);
  border-color: transparent;
}
</style>
