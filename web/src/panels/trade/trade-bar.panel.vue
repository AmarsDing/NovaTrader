<script setup lang="ts">
/**
 * 下单条（视频设计指引 §8 底条恒随）：下单 / 当前委托 / 委托记录 / 持仓成交。
 * 纪律（与 TradeView 同源、轻量版，全量订单中心仍是「交易 JY」面板）：
 * - 表单 symbol 默认随全局 symbol store，布局不动；
 * - XD buy/sell 助记符直达：经 panel-bus 预填方向与代码（P0-2 修复，与 TradeView.applyOrderParams 同款）；
 * - 提交前强制两段试算：停笔预览（防抖）+ 落单前一次全新试算；试算拦截/不可用都不落单；
 * - 确认顺序对齐 FE-T1：试算 → 确认（带试算摘要与幂等键）→ 提交；
 * - LIVE 可选性只认 admission 信封（FE-T4，与 TradeView 同店单源；总闸未开 fail-closed）；
 * - 闸门对齐 TradeView 完整口径（FE-T3）：风控就绪+账本读到/新鲜/同源，缺闸不提交；
 * - 授权徽标常显；下单回报 toast 必带原因，网络层异常显「待核」并指引去交易面板核对。
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { askConfirm } from '@/stores/confirm'
import { previewOrder, type PreviewBody, type PreviewReply } from '@/api/risk'
import { createAlertRule, type AlertRuleDraft } from '@/api/alert-rules'
import { getOpsReadiness, type OpsReadiness } from '@/api/ops'
import type { OrderDraft } from '@/api/trade'
import { useAccountStore } from '@/stores/account'
import { useAdmissionStore } from '@/stores/admission'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import { useSymbolStore } from '@/stores/symbol'
import { useTradeStore } from '@/stores/trade'
import { createShortcutRegistry } from '@/workspace/shortcut-registry'
import NtBlockerCard from '@/display/NtBlockerCard.vue'
import SymbolName from '@/display/SymbolName.vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTabs from '@/ui/NtTabs.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import { statusOf } from '@/ui/status-map'
import { amount, clock, compactAmount, messageOf, pnlClass, price, sideLabel, canCancelOrder } from '@/utils/format'
import { normalizeCode } from '@/utils/symbol'
import { deskNotify } from '@/utils/notify'
import { openDeeplink } from '@/utils/deeplink'
import { serverNow } from '@/utils/server-clock'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'
import { normalizeBook } from '@/view/trade-draft'
import { useToast } from '@/ui/useToast'
import { isLive } from '@/ws/hub'
import { getWorkbench } from '@/workspace/inject'
import { consumePanelParams, onPanelParams } from '@/workspace/panel-bus'

const auth = useAuthStore()
const accountStore = useAccountStore()
const risk = useRiskStore()
const trade = useTradeStore()
const symbolStore = useSymbolStore()
const toast = useToast()

type TabId = 'order' | 'open' | 'all' | 'hold'
// W3-2 面板偏好持久化：键版勿改，旧档自然失效。
const PREFS_KEY = 'nt.prefs.trade-bar.v1'
const PREFS_ALLOW = { tab: ['order', 'open', 'all', 'hold'] } as const
// 读档回填页签初值（默认仍是下单页）；提交成功程序化跳到「当前委托」同样会被收档，
// 属预期行为——用户最后看到的就是 open 页。
const tab = ref<TabId>((loadPanelPrefs(PREFS_KEY, PREFS_ALLOW).tab as TabId | undefined) ?? 'order')
watch([tab], () => savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { tab: tab.value }))

/** FE-T4：LIVE 可选性只认 admission 信封总闸（与 TradeView 同店单源）；信封取数失败即 fail-closed 不可选，不再前端写死。 */
const admission = useAdmissionStore()
const liveAllowed = computed(() => admission.liveEnabled)
const book = computed(() => (accountStore.book === 'LIVE' ? 'LIVE' : 'SIM'))

const form = reactive({ symbol: symbolStore.code, side: 'buy' as 'buy' | 'sell', price: '', volume: '' })
const touched = ref(false)

/**
 * 随 store 换代码：仅当用户没沿旧代码改表时才跟随（看完旧票再操作不丢输入）。
 * lastSyncedCode 锚住「程序化跟随写入」——用户手输与 store 跟随共用一个 form.symbol，
 * 靠这个锚才能把 watcher 反馈环拆开：store 赋的值不算用户编辑，手输的才算。
 */
let formEditedSinceSymbol = false
let lastSyncedCode = form.symbol
watch(
  () => symbolStore.code,
  (code) => {
    if (!code || formEditedSinceSymbol) return
    lastSyncedCode = code
    form.symbol = code
  },
)
watch(
  () => form.symbol,
  (next) => {
    if (next === lastSyncedCode) formEditedSinceSymbol = false
    else formEditedSinceSymbol = true
  },
)
watch(book, () => {
  formEditedSinceSymbol = false
  void accountStore.refresh()
  void trade.refresh(book.value)
})

/**
 * XD buy/sell 助记符直达（P0-2）：消费 panel-bus 预填方向与代码，与 TradeView.applyOrderParams 同款。
 * 代码经 normalizeCode 落定（总线进站已归一，再兜一道），写入时同步 lastSyncedCode 锚——
 * 程序化预填不算用户编辑，之后 store 切票仍可跟随；方向之外不动价格/数量，交给用户核对。
 */
function applyOrderParams(params: Record<string, unknown>) {
  const pSymbol = params.symbol
  if (typeof pSymbol === 'string' && pSymbol.trim()) {
    const next = normalizeCode(pSymbol)
    if (next && SYMBOL_RE.test(next)) {
      lastSyncedCode = next
      formEditedSinceSymbol = false
      form.symbol = next
      symbolStore.setSymbol(next, 'trade-bar')
    }
  }
  const pSide = params.side
  if (pSide === 'buy' || pSide === 'sell') form.side = pSide
}
/** 已挂载时 XD 直达立即推送；卸载时退订。 */
const offPanelParams = onPanelParams('trade-bar', applyOrderParams)

const SYMBOL_RE = /^\d{6}\.(SZ|SH|BJ)$/i
const symbolOk = computed(() => SYMBOL_RE.test(form.symbol.trim()))
const priceNum = computed(() => Number(form.price))
const volumeNum = computed(() => Number(form.volume))
const priceOk = computed(() => Number.isFinite(priceNum.value) && priceNum.value > 0)
const volumeOk = computed(() => Number.isInteger(volumeNum.value) && volumeNum.value >= 100 && volumeNum.value % 100 === 0)
const formOk = computed(() => symbolOk.value && priceOk.value && volumeOk.value)

/* ---------- G7 试算：停笔预览 + 提交前重算 ---------- */

const preview = ref<PreviewReply | null>(null)
const previewError = ref('')
const previewing = ref(false)
let previewSeq = 0
let previewTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false

function previewBodyFor(clientOrderId: string): PreviewBody {
  return {
    client_order_id: clientOrderId,
    account_type: book.value,
    symbol: form.symbol.trim(),
    side: form.side,
    price: priceNum.value,
    volume: volumeNum.value,
    source: 'manual',
    operator: auth.operatorName,
  }
}

async function runPreview(seq: number) {
  const body = previewBodyFor(`preview-${crypto.randomUUID()}`)
  try {
    const result = await previewOrder(body)
    if (disposed || seq !== previewSeq) return
    preview.value = result
    previewError.value = ''
  } catch (err) {
    if (disposed || seq !== previewSeq) return
    preview.value = null
    previewError.value = messageOf(err)
  } finally {
    if (seq === previewSeq) previewing.value = false
  }
}

watch(
  () => [form.symbol, form.side, form.price, form.volume, book.value] as const,
  () => {
    preview.value = null
    previewError.value = ''
    if (previewTimer) clearTimeout(previewTimer)
    if (!formOk.value) return
    previewTimer = setTimeout(() => {
      previewTimer = undefined
      if (disposed) return
      previewing.value = true
      void runPreview(++previewSeq)
    }, 250)
  },
)

const previewBlocked = computed(() => (preview.value && !preview.value.approved ? preview.value : null))

/* ---------- W3b.b 拦截位：执行链闸门就绪引导卡（R-UI-30/36） ---------- */

/** 只列下单执行链闸门（风控三闸+准入+文件单通道）；数据源/模型闸属扫描链，归股池面板呈现。 */
const TRADE_GATES = ['risk_engine', 'risk_config', 'risk_killswitch', 'sim_admission', 'broker_gw']
const readiness = ref<OpsReadiness | null>(null)
const readinessBlockers = computed(() => (readiness.value?.blockers ?? []).filter((b) => TRADE_GATES.includes(b.gate)))
/** 就绪口取不到不盖面板：下单拦截另有风控试算 fail-closed 兜底，卡只在「拿到回执且未就绪」时现身。 */
async function refreshReadiness() {
  try {
    readiness.value = await getOpsReadiness()
  } catch {
    /* fail-silent：理由见上，面板主体不被就绪口拖死 */
  }
}

/* ---------- QD 攻心版 v1：双色买卖整块 + 仓位四键 + 持仓报价锚 ---------- */

/** 持仓行（仓位卖出档与报价锚的唯一取数口）；非持仓票锚/卖档给明确原因，不凭空造。 */
const positionOf = computed(() => accountStore.positions.find((p) => p.symbol === form.symbol.trim().toUpperCase()) ?? null)
/** 报价锚：仅持仓票有最现价证据时给锚（五档盘口锚随 viz 死件批，缺证据不虚构）。点按显式写入限价，不静默改价。 */
const anchorPrice = computed(() => (positionOf.value?.lastPrice != null ? positionOf.value.lastPrice : null))
function applyAnchor() {
  if (anchorPrice.value == null) return
  form.price = anchorPrice.value.toFixed(2)
}

const FRACTIONS = [0.25, 0.5, 0.75, 1] as const
/** 仓位档折算手数：买=可用现金与限价折算（整百取整）；卖=该票今日可卖（满仓档给精确可卖，含尾零）。
 *  缺腿或不足一手返回 0 + 中文原因，禁用档键——不拿 0 股伪装可点（数据纪律）。 */
function fractionShares(pct: number): { shares: number; reason: string } {
  if (form.side === 'buy') {
    if (accountStore.cash == null) return { shares: 0, reason: '可用现金未读到——点「刷新」重试' }
    if (!priceOk.value) return { shares: 0, reason: '先填正限价，才能按现金折算' }
    const shares = Math.floor((accountStore.cash * pct) / priceNum.value / 100) * 100
    return shares > 0 ? { shares, reason: '' } : { shares: 0, reason: '按此档折算不足一手（100 股）' }
  }
  const available = positionOf.value?.available
  if (available == null) return { shares: 0, reason: '该票不在持仓或无可卖股数' }
  const shares = pct === 1 ? available : Math.floor((available * pct) / 100) * 100
  return shares > 0 ? { shares, reason: '' } : { shares: 0, reason: '按此档折算不足一手（100 股）' }
}
function fractionTitle(pct: number): string {
  const r = fractionShares(pct)
  if (r.reason) return r.reason
  return form.side === 'buy' ? `按可用现金 ${pct * 100}% 折算 → ${r.shares} 股` : `按该票可卖 ${pct * 100}% 折算 → ${r.shares} 股`
}
function applyFraction(pct: number) {
  const { shares } = fractionShares(pct)
  if (shares > 0) form.volume = String(shares)
}

/* ---------- W5c：QD 攻心版补齐——F9/F10 热键、R 倍数联动、反手 ---------- */

/** R 倍数（QD 图3 设计原图 §5.13B）：止盈/止损勾选后按百分比与限价几何联动；
 *  仅买单给 R（卖单止盈止损语义不适用，反手键接管）；R<1 警告（赔率倒挂）。 */
const tpEnabled = ref(false)
const slEnabled = ref(false)
const tpPct = ref('')
const slPct = ref('')

const riskReward = computed(() => {
  if (!tpEnabled.value || !slEnabled.value || !priceOk.value) return null
  const tp = Number(tpPct.value)
  const sl = Number(slPct.value)
  /* 负值/空白不出 R：Number('')=0 会显出「R 0.0」误导，负数经 abs 掩盖符号更是假赔率。 */
  if (!Number.isFinite(tp) || !Number.isFinite(sl) || tp <= 0 || sl <= 0) return null
  return tp / sl
})

/** 提交后联动：止盈/止损勾选即建告警规则（M08 引擎评估，通知经 M10 出站）。
 *  买单：止盈=上破 price*(1+tp%)，止损=下破 price*(1-sl%)；卖单不建（反手接管）。
 *  入参为提交时的快照（勾选态受理即复位，异步段不得回读 ref）；
 *  止损 ≥100% 目标价非正，服务端校验必拒——前置跳过不造废单并明示未布防；
 *  联动失败不阻塞主单（告警是提示位不是下单闸），但必须 toast 告知「你以为有保护其实没有」。 */
async function linkAlertRules(snap: { symbol: string; price: number; tp: number; sl: number }) {
  const { symbol, price, tp, sl } = snap
  const operator = auth.operatorName
  if (Number.isFinite(tp) && tp > 0) {
    const draft: AlertRuleDraft = { symbol, condition: 'above', targetPrice: (price * (1 + tp / 100)).toFixed(3), note: `止盈 ${tp}%（W5c 联动）` }
    await createAlertRule(draft, operator).catch(() => {
      void toast.warn('止盈告警未建成', `${symbol} 上破 ${tp}% 规则未受理——请到风控链面板核对`)
    })
  }
  if (Number.isFinite(sl) && sl > 0 && sl < 100) {
    const draft: AlertRuleDraft = { symbol, condition: 'below', targetPrice: (price * (1 - sl / 100)).toFixed(3), note: `止损 ${sl}%（W5c 联动）` }
    await createAlertRule(draft, operator).catch(() => {
      void toast.warn('止损告警未建成', `${symbol} 下破 ${sl}% 规则未受理——请到风控链面板核对`)
    })
  } else if (Number.isFinite(sl) && sl >= 100) {
    /* 止损 ≥100% 目标价非正，服务端校验必拒——不造废单，明示未布防（勾选留空= visibly 未填，不扰） */
    void toast.warn('止损告警未建成', '止损幅度须小于 100%，本次未布防')
  }
}

/** F9/F10 热键（W5b 登记表 scope=trade-bar）：买/卖方向直填 + 表单合法即提交。
 *  不劫持打字（shortcut-registry.isEditableTarget 门闸）；LIVE 账本恒禁。 */
const shortcutRegistry = createShortcutRegistry()
shortcutRegistry.register({ id: 'trade-bar.buy', combo: 'F9', desc: '买入（表单合法即提交）', scope: 'trade-bar' })
shortcutRegistry.register({ id: 'trade-bar.sell', combo: 'F10', desc: '卖出（表单合法即提交）', scope: 'trade-bar' })
let offHotkeys: (() => void) | undefined

function hotkeySubmit(side: 'buy' | 'sell') {
  /* 热键与提交钮同闸（submitBlock 含 viewer/LIVE/急停/停止开仓/试算拦截），
     且只在下单页生效——委托/持仓页表单不可见，后台静默落单违背「所见即所提」。 */
  if (tab.value !== 'order') return
  if (submitBlock.value || submitting.value) return
  form.side = side
  void submit()
}

function onHotkey(e: KeyboardEvent) {
  /* 命中才 preventDefault（F10 在宿主内核有菜单栏默认语义）；未命中不吞键。 */
  if (shortcutRegistry.run(e, 'trade-bar')) e.preventDefault()
}

onMounted(() => {
  shortcutRegistry.bind('trade-bar.buy', () => hotkeySubmit('buy'))
  shortcutRegistry.bind('trade-bar.sell', () => hotkeySubmit('sell'))
  window.addEventListener('keydown', onHotkey)
  offHotkeys = () => window.removeEventListener('keydown', onHotkey)
})

onBeforeUnmount(() => {
  offHotkeys?.()
  /* unregisterScope 一并释放登记与 handler（单口清理，不做双写范式）。 */
  shortcutRegistry.unregisterScope('trade-bar')
})

/** 反手（QD 图3 设计原图 §5.13B）：持仓票一键卖全可卖 → 买回原量。
 *  显式两步（先卖后买），卖单同样先过风控试算门闩（与 submit() 同口径——拦截/不可用都不落单），
 *  买单在卖单受理后预填表单，由用户核对后走正常提交流程再发。 */
const reversing = ref(false)

async function reverse() {
  if (auth.isViewer() || reversing.value) return
  const pos = positionOf.value
  if (!pos || pos.available == null || pos.available <= 0) return
  if (book.value === 'LIVE' && !liveAllowed.value) return
  hintClear()
  reversing.value = true
  try {
    /* 第一步：卖全可卖（现价锚优先，缺锚用表单限价；两头都没有给原因，不静默空操作）。 */
    const sellPrice = anchorPrice.value ?? (priceOk.value ? priceNum.value : null)
    if (sellPrice == null) {
      submitError.value = '反手缺价格证据：该票暂无现价锚，请先填正限价再点反手'
      return
    }
    const sellDraft: OrderDraft = {
      clientOrderId: `reverse-sell-${crypto.randomUUID()}`,
      account: book.value,
      symbol: form.symbol.trim(),
      side: 'sell',
      price: sellPrice,
      volume: pos.available,
      source: 'manual',
      operator: auth.operatorName,
    }
    /* 试算门闩（与 submit() 同口径）：拦截/不可用都不落单。 */
    let pv: PreviewReply
    try {
      pv = await previewOrder(previewBodyFor(sellDraft.clientOrderId))
    } catch (err) {
      submitError.value = `反手风控试算未返回，未落单：${messageOf(err)}`
      return
    }
    if (!pv.approved) {
      submitError.value = `反手风控试算未放行${pv.blockRule ? `（${pv.blockRule}）` : ''}：${pv.reason || '未给出原因'}`
      return
    }
    await trade.place(sellDraft)
    void toast.success('反手卖单已受理', `卖出 ${pos.symbol} ${pos.available} 股`)
    /* 第二步：买回原量（价格同卖单锚，用户确认后走正常提交流程）。 */
    form.side = 'buy'
    form.price = sellPrice.toFixed(2)
    form.volume = String(pos.available)
    submitHint.value = '反手卖单已受理；买单已预填（同价同量），请核对后提交'
  } catch (err) {
    submitError.value = `反手卖单未受理：${messageOf(err)}`
  } finally {
    reversing.value = false
  }
}

/* ---------- 提交 ---------- */

const submitting = ref(false)
const submitError = ref('')
const submitHint = ref('')

const submitBlock = computed(() => {
  if (auth.isViewer()) return '当前是查看角色，不能下单'
  if (!formOk.value) return '请填写 600519.SH 形态代码、正限价、100 股整数倍'
  if (form.side === 'buy' && (risk.ready && risk.killActive)) return '急停已触发，不能新买入'
  if (form.side === 'buy' && accountStore.openHalted) return accountStore.haltReason || '账户已停止新开仓'
  if (book.value === 'LIVE' && !liveAllowed.value) return '实盘执行权限尚未开放，仅支持 SIM 委托'
  /* FE-T3 对齐 TradeView 完整闸门口径：风控就绪 + 账本读到/新鲜/同源（此前只查急停与开仓锁，缺这三闸——
     下单条与全量订单中心两条提交链路闸门不齐，本会话收编同一口径）。 */
  if (!risk.ready || !risk.checkedAt || serverNow() - risk.checkedAt > 30_000) return '风控状态未读到最新结果，暂不能提交'
  if (!accountStore.accountOk || !accountStore.positionsOk) return '账户或持仓未读到最新结果，暂不能提交'
  if (!accountStore.checkedAt || serverNow() - accountStore.checkedAt > 30_000) return '账本读数超过 30 秒未刷新，暂不能提交'
  if (accountStore.snapshot && normalizeBook(accountStore.snapshot.account) !== normalizeBook(book.value)) return '账户响应与当前账本不一致，暂不能提交'
  if (previewError.value) return `试算不可用，暂不放开提交：${previewError.value}`
  if (previewBlocked.value) {
    return `风控试算未放行${previewBlocked.value.blockRule ? `（${previewBlocked.value.blockRule}）` : ''}：${previewBlocked.value.reason || '请调整委托后重试'}`
  }
  return ''
})
const canSubmit = computed(() => !submitBlock.value && !submitting.value && !previewing.value)

async function submit() {
  if (submitting.value) return
  hintClear()
  touched.value = true
  if (!formOk.value) {
    submitError.value = '请填写 600519.SH 形态代码、正限价，以及 100 股整数倍'
    return
  }
  const draft: OrderDraft = {
    clientOrderId: `manual-${crypto.randomUUID()}`,
    account: book.value,
    symbol: form.symbol.trim(),
    side: form.side,
    price: priceNum.value,
    volume: volumeNum.value,
    source: 'manual',
    operator: auth.operatorName,
  }
  submitting.value = true
  try {
    // 先刷新账本与风控的最新读数，再按同一草稿编号重跑试算（不落单不留痕）。
    await Promise.all([accountStore.refresh(), risk.refresh()])
    // FE-T3：落单前复核账本/风控新鲜度与一致性（对齐 TradeView submissionBlock 的同一组闸——
    // 按钮禁用只是 UI，回车/热键路径也得被同一口径拦下；试算闸由下方全新试算兜底，不重复判）。
    if (!risk.ready || !risk.checkedAt || serverNow() - risk.checkedAt > 30_000) {
      submitError.value = '风控状态未读到最新结果，暂不能提交'
      return
    }
    if (!accountStore.accountOk || !accountStore.positionsOk) {
      submitError.value = '账户或持仓未读到最新结果，暂不能提交'
      return
    }
    if (!accountStore.checkedAt || serverNow() - accountStore.checkedAt > 30_000) {
      submitError.value = '账本读数超过 30 秒未刷新，暂不能提交'
      return
    }
    if (accountStore.snapshot && normalizeBook(accountStore.snapshot.account) !== normalizeBook(book.value)) {
      submitError.value = '账户响应与当前账本不一致，暂不能提交'
      return
    }
    let pv: PreviewReply
    try {
      pv = await previewOrder({ ...previewBodyFor(draft.clientOrderId) })
    } catch (err) {
      submitError.value = `风控试算未返回，暂不能提交：${messageOf(err)}`
      return
    }
    if (disposed) return
    preview.value = pv
    if (!pv.approved) {
      submitError.value = `风控试算未放行${pv.blockRule ? `（${pv.blockRule}）` : ''}：${pv.reason || '未给出原因'}`
      return
    }
    // FE-T1：确认顺序=试算→确认（弹层带试算摘要与幂等键）→提交——用户确认时必须已看到金额/费用/缩量。
    if (draft.account === 'LIVE') {
      const reply = await askConfirm({ ...draft, previewAmount: pv.amount, previewFee: pv.fee, previewCashDelta: pv.cashDelta, previewAdjusted: pv.adjusted, previewVolume: pv.volume }, true)
      if (reply !== 'ok') {
        submitHint.value = '实盘确认已作废，委托未提交'
        return
      }
    }
    // 提交的门闩：此时表单若已改（停笔输入），按新输入不算数，交给用户重新核对。
    if (draft.symbol !== form.symbol.trim() || draft.side !== form.side || draft.price !== priceNum.value || draft.volume !== volumeNum.value || draft.account !== book.value) {
      submitHint.value = '检查期间表单已变化，请重新核对后再提交'
      return
    }
    try {
      await trade.place(draft)
    } catch (err) {
      const message = messageOf(err)
      if ((err as { status?: number })?.status === 0) {
        submitError.value = `委托结果未知（网络异常）：编号 ${draft.clientOrderId}，请到交易（JY）面板核对后再决定重试`
        void deskNotify('委托结果待核', draft.clientOrderId, 'warn')
      } else {
        submitError.value = `委托未受理：${message}`
        void toast.warn('委托未受理', message)
      }
      return
    }
    if (disposed) return
    const label = `${sideLabel(draft.side)} ${draft.symbol} ${draft.volume} 股`
    submitHint.value = `已受理 ${label}；编号 ${draft.clientOrderId}`
    void toast.success('委托已受理', label)
    void deskNotify('模拟委托已受理', label, 'success')
    /* W5c：止盈/止损勾选联动——买单受理后建告警规则（M08 引擎评估，M10 通知出站），
       失败不阻塞主单（告警是提示位不是下单闸）；快照取值（勾选态随即复位，异步段不回读 ref）。 */
    if (form.side === 'buy') {
      void linkAlertRules({
        symbol: form.symbol.trim().toUpperCase(),
        price: priceNum.value,
        tp: tpEnabled.value ? Number(tpPct.value) : 0,
        sl: slEnabled.value ? Number(slPct.value) : 0,
      })
      /* 联动勾选单次生效：受理即复位，防改价重提时同码规则无声堆积。 */
      tpEnabled.value = false
      slEnabled.value = false
      tpPct.value = ''
      slPct.value = ''
    }
    formEditedSinceSymbol = false
    void accountStore.refresh()
    tab.value = 'open'
  } finally {
    submitting.value = false
  }
}

function hintClear() {
  submitError.value = ''
  submitHint.value = ''
}

/* ---------- 列表 ---------- */

const openOrders = computed(() => trade.orders.filter((o) => canCancelOrder(o.status)))

/** R3 浮盈额：缺一输入显「—」，不拿 0 冒充（数据纪律）。 */
function pnlAmount(p: { quantity: number | null; avgCost: number | null; lastPrice: number | null }): number | null {
  if (p.quantity == null || p.avgCost == null || p.lastPrice == null) return null
  return (p.lastPrice - p.avgCost) * p.quantity
}
/** R3 浮盈%：成本为 0/缺同样显「—」。 */
function pnlRatioText(p: { avgCost: number | null; lastPrice: number | null }): { text: string; value: number | null } {
  if (p.avgCost == null || p.avgCost === 0 || p.lastPrice == null) return { text: '—', value: null }
  const r = (p.lastPrice - p.avgCost) / p.avgCost
  return { text: `${r > 0 ? '+' : ''}${(r * 100).toFixed(1)}%`, value: r }
}
/** R3 今日可卖：后端 available 即 T+1 可卖口径（接口无独立 sellableToday 字段，注明）；0 显「T+1 不可卖」text-3。 */
function sellable(p: { available: number | null }): { text: string; hold: boolean } {
  if (p.available == null) return { text: '—', hold: false }
  if (p.available === 0) return { text: 'T+1 不可卖', hold: true }
  return { text: String(p.available), hold: false }
}

/** 委托状态章文案/tone 出自 order 册（§7.1，一字锁定）：后端别名（new/accepted/canceled）归并同义项；
 *  册外值返回 null，模板原样回显 status（不自封文案、不用 0/空串冒充）。 */
function orderStatusEntry(status: string | null | undefined) {
  const key = String(status || '').toLowerCase()
  if (key === 'new' || key === 'pending') return statusOf('order.pending')
  if (key === 'accepted' || key === 'submitted') return statusOf('order.submitted')
  if (key === 'pending_confirm') return statusOf('order.pending_confirm')
  if (key === 'partial') return statusOf('order.partial')
  if (key === 'filled') return statusOf('order.filled')
  if (key === 'cancelled' || key === 'canceled') return statusOf('order.cancelled')
  if (key === 'rejected') return statusOf('order.rejected')
  if (key === 'expired') return statusOf('order.expired')
  return null
}

/** FE-T4：授权徽标状态词走 status-map 册（auth.sim/auth.live/auth.unauthorized），不自封文案。 */
const badge = computed(() => {
  if (book.value === 'LIVE') {
    return liveAllowed.value
      ? { text: statusOf('auth.live').text, tone: statusOf('auth.live').tone, note: '实盘总闸已开（admission 信封）；新委托仍走确认与风控试算' }
      : { text: `LIVE · ${statusOf('auth.unauthorized').text}`, tone: statusOf('auth.unauthorized').tone, note: '实盘总闸未开：经交易面板的准入证据与授权开关核验' }
  }
  return { text: statusOf('auth.sim').text, tone: statusOf('auth.sim').tone, note: '模拟账本撮合，不等同实盘成交' }
})

const REFRESH_MS = 10_000
/** 就绪轮询 30s 静候档（与 datasource-health/OpsView 同谱）：document.hidden 跳拍；心智是提示位不是秒表。 */
const READINESS_MS = 30_000
let refreshTimer: ReturnType<typeof setInterval> | undefined
let readinessTimer: ReturnType<typeof setInterval> | undefined

/** §7.3 首批挂载：自助拉取模型的真实新鲜度证据 = 最近一次账本刷新成功落笔的时刻（服务端钟；全失败保留上一刻，由年龄自然变旧，不拿旧数据冒充新读数）。 */
const bookFreshAt = ref<number | null>(null)

function refreshBook() {
  /* FE-T3：账本/风控同一轮新鲜度——submitBlock 三闸的输入同源刷新（缺风险拍，风控闸只剩「急停」半边）。 */
  void Promise.allSettled([accountStore.refresh(), trade.refresh(book.value), risk.refresh()]).then((settled) => {
    if (disposed) return
    if (settled.some((s) => s.status === 'fulfilled')) bookFreshAt.value = serverNow()
  })
}

function openTradePanel() {
  getWorkbench()?.openPanel('trade')
}

onMounted(() => {
  // 冷启动（未挂载时按过 XD）：pending 参数一次性消费。
  const staged = consumePanelParams('trade-bar')
  if (staged) applyOrderParams(staged)
  refreshBook()
  void refreshReadiness()
  /* FE-T4：LIVE 可选性走 admission 信封——挂载即开 30s 静候档轮询（幂等，TradeView/PaperAdmission 同店）。 */
  admission.startPolling()
  refreshTimer = setInterval(() => {
    if (disposed || document.hidden || !isLive()) return
    void trade.refresh(book.value)
  }, REFRESH_MS)
  readinessTimer = setInterval(() => {
    if (disposed || document.hidden) return
    void refreshReadiness()
  }, READINESS_MS)
})

onBeforeUnmount(() => {
  disposed = true
  offPanelParams()
  if (previewTimer) clearTimeout(previewTimer)
  if (refreshTimer) clearInterval(refreshTimer)
  if (readinessTimer) clearInterval(readinessTimer)
})
</script>

<template>
  <div class="nt-page tb">
    <header class="bar">
      <div class="bar__left">
        <NtTabs
          v-model="tab"
          :items="[
            { value: 'order', label: '下单' },
            { value: 'open', label: `当前委托${openOrders.length ? ` ${openOrders.length}` : ''}` },
            { value: 'all', label: '委托记录' },
            { value: 'hold', label: '持仓成交' },
          ]"
        />
      </div>
      <div class="bar__right">
        <span class="auth" :title="badge.note">
          授权
          <NtBadge :tone="badge.tone">{{ badge.text }}</NtBadge>
        </span>
        <span class="readout">{{ book }} · 可用 {{ accountStore.cash != null ? compactAmount(accountStore.cash) : '未知' }} · 净值 {{ accountStore.equity != null ? compactAmount(accountStore.equity) : '未知' }}</span>
        <NtButton size="sm" variant="ghost" :loading="trade.loading" @click="refreshBook">刷新</NtButton>
        <NtButton size="sm" variant="ghost" title="全量订单中心（信号草稿/核对流水）" @click="openTradePanel">交易面板 ↗</NtButton>
      </div>
    </header>

    <section v-if="tab === 'order'" class="order">
      <!-- W3b.b：执行链闸门未就绪先亮卡指路（就绪口不可达不盖面板——拦截兜底另有试算 fail-closed）。 -->
      <NtBlockerCard
        v-if="readiness && !readiness.ready && readinessBlockers.length"
        :ready="false"
        :blockers="readinessBlockers"
        :as-of="clock(readiness.asOf)"
        title="下单前置检查"
        @navigate="openDeeplink"
      />
      <form class="ticket" @submit.prevent="submit">
        <!-- QD 攻心版（P0-7）：买整块红/卖整块绿双目锚（§0.3 红涨绿跌）。快捷键徽注只标真实绑定：
             B/S=shortcuts.ts orderTicket 全局热键唤起本票并预填买卖方向（B3 走查勘正）；
             F9/F10=W5c 本面板登记表热键（仅下单页，与提交钮同 submitBlock 闸，LIVE 恒禁）。 -->
        <div class="ticket__side" role="group" aria-label="买卖方向">
          <button
            type="button"
            class="tb-side tb-side--buy"
            :class="{ active: form.side === 'buy' }"
            :aria-pressed="form.side === 'buy'"
            title="B 唤起本票预填买；F9 在下单页表单合法即提交买单（与提交钮同闸）"
            @click="form.side = 'buy'"
          >
            <b>买入</b><i class="kbd">B · F9</i>
          </button>
          <button
            type="button"
            class="tb-side tb-side--sell"
            :class="{ active: form.side === 'sell' }"
            :aria-pressed="form.side === 'sell'"
            title="S 唤起本票预填卖；F10 在下单页表单合法即提交卖单（与提交钮同闸）"
            @click="form.side = 'sell'"
          >
            <b>卖出</b><i class="kbd">S · F10</i>
          </button>
        </div>
        <label class="ticket__field">
          <span>代码</span>
          <NtInput v-model="form.symbol" placeholder="600519.SH" mono :invalid="touched && !symbolOk" />
        </label>
        <label class="ticket__field">
          <span>限价</span>
          <NtInput v-model="form.price" type="number" mono :invalid="touched && !priceOk" placeholder="0.00" />
        </label>
        <!-- 报价锚：仅持仓票显锚，缺报价证据的票不凭空给 -->
        <NtButton v-if="anchorPrice != null" type="button" size="sm" variant="quiet" class="ticket__anchor" @click="applyAnchor">
          锚现价 {{ price(anchorPrice) }}
        </NtButton>
        <label class="ticket__field">
          <span>数量</span>
          <NtInput v-model="form.volume" type="number" mono :invalid="touched && !volumeOk" placeholder="100 的整数倍" />
        </label>
        <div class="ticket__frac" role="group" aria-label="仓位档位">
          <!-- 禁键原因可达性（B3 走查 P2 销号，直觉5）：disabled 按钮不派发指针事件也不可聚焦，
               title 挂外层 span——禁态经 tabindex=0 可聚焦，悬浮/读屏都能拿到缺腿原因。 -->
          <span
            v-for="p in FRACTIONS"
            :key="p"
            class="frac-cell"
            :title="fractionTitle(p)"
            :aria-label="fractionTitle(p)"
            :tabindex="!fractionShares(p).shares ? 0 : undefined"
          >
            <button
              type="button"
              class="frac"
              :disabled="!fractionShares(p).shares"
              @click="applyFraction(p)"
            >
              {{ p === 1 ? 'Max' : `${p * 100}%` }}
            </button>
          </span>
        </div>
        <!-- W5c R 倍数联动（QD 图3 设计原图 §5.13B）：止盈/止损勾选后按百分比与限价几何联动；
             R<1 警告（赔率倒挂）；仅买单给 R（卖单止盈止损语义不适用，反手键接管）。 -->
        <div v-if="form.side === 'buy'" class="ticket__rr" role="group" aria-label="止盈止损与 R 倍数">
          <label class="rr-cell">
            <input v-model="tpEnabled" type="checkbox" />
            <span>止盈</span>
            <NtInput v-if="tpEnabled" v-model="tpPct" type="number" mono placeholder="%" class="rr-input" />
          </label>
          <label class="rr-cell">
            <input v-model="slEnabled" type="checkbox" />
            <span>止损</span>
            <NtInput v-if="slEnabled" v-model="slPct" type="number" mono placeholder="%" class="rr-input" />
          </label>
          <span v-if="riskReward != null" class="rr-value" :class="{ 'rr-value--warn': riskReward < 1 }">
            R {{ riskReward.toFixed(1) }}
          </span>
        </div>
        <NtButton type="submit" variant="primary" size="lg" :loading="submitting" :disabled="!canSubmit" :disabled-reason="submitBlock">
          提交委托
        </NtButton>
        <!-- W5c 反手（QD 图3 设计原图 §5.13B）：持仓票一键卖全可卖 → 买回原量（两步显式，卖单受理后买单预填待确认）。 -->
        <NtButton
          v-if="positionOf && positionOf.available != null && positionOf.available > 0"
          type="button"
          variant="quiet"
          size="sm"
          :loading="reversing"
          :disabled="reversing || auth.isViewer() || (book === 'LIVE' && !liveAllowed)"
          title="反手：卖全可卖 → 买回原量（两步显式，卖单受理后买单预填待确认）"
          @click="reverse"
        >
          反手
        </NtButton>
      </form>
      <div class="preview" :class="{ blocked: previewBlocked }">
        <template v-if="previewing && !preview">试算中……</template>
        <template v-else-if="previewError">试算不可用：{{ previewError }}</template>
        <template v-else-if="preview">
          <span :class="preview.approved ? 'verdict-ok' : 'verdict-bad'">
            {{ preview.approved ? `试算放行${preview.adjusted ? `（缩量至 ${preview.volume ?? '?'} 股）` : ''}` : `试算拦截${preview.blockRule ? ` ${preview.blockRule}` : ''}` }}
          </span>
          <span v-if="preview.reason" class="pv">{{ preview.reason }}</span>
          <span v-if="preview.amount != null" class="pv">金额 {{ amount(preview.amount) }}</span>
          <span v-if="preview.fee != null" class="pv">费用 {{ amount(preview.fee) }}</span>
          <span v-if="preview.cashDelta != null" class="pv" :class="pnlClass(preview.cashDelta)">现金影响 {{ amount(preview.cashDelta) }}</span>
        </template>
        <template v-else>填好代码/限价/数量后自动试算（不落单）。</template>
      </div>
      <NtErrorGuide v-if="submitError" :error="submitError" />
      <p v-else-if="submitHint" class="nt-note">{{ submitHint }}</p>
      <p v-else-if="submitBlock && touched" class="nt-note">{{ submitBlock }}</p>
    </section>

    <section v-else-if="tab === 'open'">
      <!-- R4（§6.3）：委托行无时间字段（接口 Delivery 未携带），不拿当前时刻冒充；
           状态章文案/tone 出自 order 册（§7.1，一字锁定），册外状态码原样回显（禁自封）。 -->
      <p v-if="!trade.ready && !trade.orders.length" class="nt-empty">委托未读到，点「刷新」重试。</p>
      <p v-else-if="!openOrders.length" class="nt-empty">当前没有未终态委托。</p>
      <table v-else class="sheet">
        <thead><tr><th>编号</th><th>证券</th><th>方向</th><th>限价</th><th>数量</th><th>已成</th><th>状态</th><th></th></tr></thead>
        <tbody>
          <tr v-for="o in openOrders" :key="o.id">
            <td class="mono">{{ o.id.slice(0, 13) }}…</td>
            <td><SymbolName :code="o.symbol" mode="inline" /></td>
            <td :class="pnlClass(o.side === 'buy' ? 1 : o.side === 'sell' ? -1 : 0)">{{ sideLabel(o.side) }}</td>
            <td class="mono">{{ price(o.price) }}</td>
            <td class="mono">{{ o.volume ?? '—' }}</td>
            <td class="mono">{{ o.filled ?? '—' }}</td>
            <td><NtBadge :tone="orderStatusEntry(o.status)?.tone ?? 'neutral'">{{ orderStatusEntry(o.status)?.text ?? (o.status || '—') }}</NtBadge><template v-if="o.reason"> · {{ o.reason }}</template></td>
            <td>
              <NtButton
                v-if="o.status.toLowerCase() === 'pending_confirm'"
                size="sm"
                @click="void trade.confirm(book, o.id)"
              >确认</NtButton>
              <NtButton size="sm" variant="ghost" @click="void trade.cancel(book, o.id)">撤销</NtButton>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-else-if="tab === 'all'">
      <!-- R4（§6.3）：同「当前委托」——时间位缺（接口无时间字段）；状态章同走 order 册（§7.1，一字锁定）。 -->
      <p class="nt-note">后端口径：最近 100 条委托（暂无日期字段，今日/历史暂不拆分）。</p>
      <p v-if="!trade.orders.length" class="nt-empty">{{ trade.ready ? '还没有委托。' : '委托未读到，点「刷新」重试。' }}</p>
      <table v-else class="sheet">
        <thead><tr><th>编号</th><th>证券</th><th>方向</th><th>限价</th><th>数量</th><th>已成</th><th>状态</th></tr></thead>
        <tbody>
          <tr v-for="o in trade.orders" :key="o.id">
            <td class="mono">{{ o.id.slice(0, 13) }}…</td>
            <td><SymbolName :code="o.symbol" mode="inline" /></td>
            <td :class="pnlClass(o.side === 'buy' ? 1 : o.side === 'sell' ? -1 : 0)">{{ sideLabel(o.side) }}</td>
            <td class="mono">{{ price(o.price) }}</td>
            <td class="mono">{{ o.volume ?? '—' }}</td>
            <td class="mono">{{ o.filled ?? '—' }}</td>
            <td><NtBadge :tone="orderStatusEntry(o.status)?.tone ?? 'neutral'">{{ orderStatusEntry(o.status)?.text ?? (o.status || '—') }}</NtBadge></td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-else>
      <div class="hold">
        <div>
          <h2 class="nt-panel__title">持仓</h2>
          <NtFreshness label="账本" :at="bookFreshAt" />
          <p v-if="!accountStore.positionsOk" class="nt-empty">持仓未读到，不能按账面为空处理；点「刷新」重试。</p>
          <p v-else-if="!trade.ready && !accountStore.positions.length" class="nt-empty">读取中……</p>
          <table v-else-if="accountStore.positions.length" class="sheet">
            <!-- R3（§6.3）：数量 | 可用 | 成本(mono) | 现价(mono) | 浮盈额/浮盈%(up/down) | 今日可卖 -->
            <thead><tr><th>证券</th><th>数量</th><th>可用</th><th>成本</th><th>现价</th><th>浮盈额</th><th>浮盈%</th><th>今日可卖</th></tr></thead>
            <tbody>
              <tr v-for="p in accountStore.positions" :key="p.symbol">
                <td><SymbolName :code="p.symbol" mode="inline" /></td>
                <td class="mono">{{ p.quantity ?? '—' }}</td>
                <td class="mono">{{ p.available ?? '—' }}</td>
                <td class="mono">{{ price(p.avgCost) }}</td>
                <td class="mono" :class="pnlClass(p.lastPrice != null && p.avgCost != null ? p.lastPrice - p.avgCost : 0)">{{ price(p.lastPrice) }}</td>
                <td class="mono" :class="pnlClass(pnlAmount(p))">{{ pnlAmount(p) != null ? amount(pnlAmount(p)) : '—' }}</td>
                <td class="mono" :class="pnlClass(pnlRatioText(p).value)">{{ pnlRatioText(p).text }}</td>
                <td class="sellable" :class="{ 'sellable--hold': sellable(p).hold }">{{ sellable(p).text }}</td>
              </tr>
            </tbody>
          </table>
          <p v-else class="nt-empty">当前账本没有持仓。</p>
        </div>
        <div>
          <h2 class="nt-panel__title">最近成交</h2>
          <p v-if="!trade.fills.length" class="nt-empty">{{ trade.ready ? '还没有成交。' : '成交未读到。' }}</p>
          <table v-else class="sheet">
            <!-- R4（§6.3）：时间(mono text-3)置首 | SymbolName(inline) | 方向字色双编码 | 量 | 价(mono) | 额(mono)；成交终态无状态章位 -->
            <thead><tr><th>时间</th><th>证券</th><th>方向</th><th>量</th><th>价</th><th>额</th></tr></thead>
            <tbody>
              <tr v-for="(f, i) in trade.fills.slice(0, 8)" :key="f.id + i">
                <td class="mono dim">{{ clock(f.time) || '—' }}</td>
                <td><SymbolName :code="f.symbol" mode="inline" /></td>
                <td :class="pnlClass(f.side === 'buy' ? 1 : -1)">{{ sideLabel(f.side) }}</td>
                <td class="mono">{{ f.qty ?? '—' }}</td>
                <td class="mono">{{ price(f.price) }}</td>
                <td class="mono">{{ compactAmount(f.amount) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.tb {
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 12.5px;
}

.bar {
  display: flex;
  align-items: center;
  gap: 12px;
  justify-content: space-between;
  flex-wrap: wrap;
}

.bar__right {
  display: flex;
  align-items: center;
  gap: 10px;
  white-space: nowrap;
}

.auth {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.readout {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11.5px;
  color: var(--nt-text-2);
}

.order {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.ticket {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}

.ticket__field {
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: 11px;
  color: var(--nt-text-3);
}

.ticket__field span {
  padding-left: 2px;
}

/* QD 攻心版：买卖双色整块（§0.3 红涨绿跌——买=--nt-up 红、卖=--nt-down 绿；
   选中性底字色复用同族 on-danger/on-success 保证 colorvision=safe 下仍成立）。 */
.ticket__side {
  display: flex;
  gap: 6px;
  align-self: flex-end;
}

.tb-side {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 40px;
  padding: 0 18px;
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--nt-radius-s);
  font-size: var(--nt-fs-title); /* B3 走查销号：裸 14px 绕 compact 缩放，归阶梯随密度联动 */
  font-weight: 700;
  cursor: pointer;
  transition:
    background var(--nt-transition),
    border-color var(--nt-transition);
}

.tb-side .kbd {
  font-style: normal;
  font-family: var(--nt-font-mono);
  font-size: var(--nt-fs-micro); /* B3 走查销号：归阶梯随密度联动 */
  line-height: 1;
  padding: 2px 5px;
  border: 1px solid currentColor;
  border-radius: 4px;
  opacity: 0.7;
}

.tb-side--buy {
  color: color-mix(in srgb, var(--nt-up) 80%, var(--nt-text-1));
  border-color: color-mix(in srgb, var(--nt-up) 45%, transparent);
}

.tb-side--buy:not(.active):hover {
  background: color-mix(in srgb, var(--nt-up) 10%, transparent);
}

.tb-side--buy.active {
  background: var(--nt-up);
  color: var(--nt-on-danger);
}

.tb-side--sell {
  color: color-mix(in srgb, var(--nt-down) 80%, var(--nt-text-1));
  border-color: color-mix(in srgb, var(--nt-down) 45%, transparent);
}

.tb-side--sell:not(.active):hover {
  background: color-mix(in srgb, var(--nt-down) 10%, transparent);
}

.tb-side--sell.active {
  background: var(--nt-down);
  color: var(--nt-on-success);
}

/* 报价锚（持仓现价）与仓位四键：与表单基线对齐的辅助行，视觉降一档（quiet/mono text-2）。 */
.ticket__anchor {
  align-self: flex-end;
}

.ticket__frac {
  display: inline-flex;
  gap: 4px;
  align-self: flex-end;
}

.ticket__frac .frac {
  min-width: 38px;
  height: var(--nt-row-h);
  padding: 0 6px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-caption); /* B3 走查销号：归阶梯随密度联动 */
  color: var(--nt-text-2);
  background: transparent;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.ticket__frac .frac:hover:not(:disabled) {
  color: var(--nt-text-1);
  border-color: var(--nt-accent-2);
}

.ticket__frac .frac:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

/* W5c R 倍数联动区：与仓位四键同档（quiet/mono text-2），R<1 走 warn 语义色。 */
.ticket__rr {
  display: inline-flex;
  gap: 8px;
  align-self: flex-end;
  align-items: center;
}

.rr-cell {
  display: inline-flex;
  gap: 4px;
  align-items: center;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
  cursor: pointer;
}

.rr-input {
  width: 48px;
}

.rr-value {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.rr-value--warn {
  color: var(--nt-warn);
}

.preview {
  padding: 6px 10px;
  border: 1px dashed var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.preview.blocked {
  border-color: var(--nt-warn);
  background: color-mix(in srgb, var(--nt-warn) 8%, transparent);
}

.pv {
  margin-left: 10px;
  color: var(--nt-text-2);
}

/* 试算结论走语义色：放行=成功，拦截=风险（§7.1 风险色不按涨跌判断）。 */
.verdict-ok {
  color: var(--nt-success);
}

.verdict-bad {
  color: var(--nt-danger);
}

.sheet {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

.sheet th {
  text-align: left;
  font-weight: 500;
  color: var(--nt-text-3);
  padding: var(--nt-cell-pad);
  border-bottom: 1px solid var(--nt-border-1);
}

.sheet td {
  padding: var(--nt-cell-pad);
  border-bottom: 1px solid var(--nt-bg-2);
  white-space: nowrap;
}

/* R4 时间位降档（mono text-3）；R3 今日可卖 text-2，0 时显「T+1 不可卖」text-3。 */
.sheet td.dim {
  color: var(--nt-text-3);
}

.sheet td.sellable {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-2);
}

.sheet td.sellable--hold {
  color: var(--nt-text-3);
}

.mono {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.hold {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}

@media (max-width: 900px) {
  .hold {
    grid-template-columns: 1fr;
  }
}
</style>
