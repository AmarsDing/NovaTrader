<script setup lang="ts">
/**
 * 下单条（视频设计指引 §8 底条恒随）：下单 / 当前委托 / 委托记录 / 持仓成交。
 * 纪律（与 TradeView 同源、轻量版，全量订单中心仍是「交易 JY」面板）：
 * - 表单 symbol 默认随全局 symbol store，布局不动；
 * - 提交前强制两段试算：停笔预览（防抖）+ 落单前一次全新试算；试算拦截/不可用都不落单；
 * - LIVE 账本永不放开（执行权限未验收，与 TradeView liveAllowed 同口径）；
 * - 授权徽标常显；下单回报 toast 必带原因，网络层异常显「待核」并指引去交易面板核对。
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { askConfirm } from '@/store/confirm'
import { previewOrder, type PreviewBody, type PreviewReply } from '@/api/risk'
import type { OrderDraft } from '@/api/trade'
import { useAccountStore } from '@/stores/account'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import { useSymbolStore } from '@/stores/symbol'
import { useTradeStore } from '@/stores/trade'
import SymbolName from '@/display/SymbolName.vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSegmented from '@/ui/NtSegmented.vue'
import NtTabs from '@/ui/NtTabs.vue'
import { amount, clock, compactAmount, messageOf, orderStatusLabel, pnlClass, price, sideLabel, canCancelOrder } from '@/utils/format'
import { deskNotify } from '@/utils/notify'
import { useToast } from '@/ui/useToast'
import { isLive } from '@/ws/hub'
import { getWorkbench } from '@/workspace/inject'

const auth = useAuthStore()
const accountStore = useAccountStore()
const risk = useRiskStore()
const trade = useTradeStore()
const symbolStore = useSymbolStore()
const toast = useToast()

type TabId = 'order' | 'open' | 'all' | 'hold'
const tab = ref<TabId>('order')

/** 实盘执行权限：与 TradeView 同源——连续运行证据只供核验，不授予权限；通道验收前恒 false。 */
const liveAllowed = computed(() => false)
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
    if (draft.account === 'LIVE') {
      const reply = await askConfirm(draft, true)
      if (reply !== 'ok') {
        submitHint.value = '实盘确认已作废，委托未提交'
        return
      }
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

const badge = computed(() => {
  if (book.value === 'LIVE') return { text: 'LIVE · 未授权', tone: 'danger' as const, note: '实盘执行权限未验收，提交保持禁用' }
  return { text: 'SIM 模拟', tone: 'accent' as const, note: '模拟账本撮合，不等同实盘成交' }
})

const REFRESH_MS = 10_000
let refreshTimer: ReturnType<typeof setInterval> | undefined

function refreshBook() {
  void accountStore.refresh()
  void trade.refresh(book.value)
}

function openTradePanel() {
  getWorkbench()?.openPanel('trade')
}

onMounted(() => {
  refreshBook()
  refreshTimer = setInterval(() => {
    if (disposed || document.hidden || !isLive()) return
    void trade.refresh(book.value)
  }, REFRESH_MS)
})

onBeforeUnmount(() => {
  disposed = true
  if (previewTimer) clearTimeout(previewTimer)
  if (refreshTimer) clearInterval(refreshTimer)
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
      <form class="ticket" @submit.prevent="submit">
        <NtSegmented
          :model-value="form.side"
          size="sm"
          :options="[{ value: 'buy', label: '买入' }, { value: 'sell', label: '卖出' }]"
          @update:model-value="form.side = $event as 'buy' | 'sell'"
        />
        <label class="ticket__field">
          <span>代码</span>
          <NtInput v-model="form.symbol" placeholder="600519.SH" mono :invalid="touched && !symbolOk" />
        </label>
        <label class="ticket__field">
          <span>限价</span>
          <NtInput v-model="form.price" type="number" mono :invalid="touched && !priceOk" placeholder="0.00" />
        </label>
        <label class="ticket__field">
          <span>数量</span>
          <NtInput v-model="form.volume" type="number" mono :invalid="touched && !volumeOk" placeholder="100 的整数倍" />
        </label>
        <NtButton type="submit" variant="primary" :loading="submitting" :disabled="!canSubmit" :title="submitBlock || '提交前会先跑一次风控试算'">
          提交委托
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
      <p v-if="submitError" class="nt-err">{{ submitError }}</p>
      <p v-else-if="submitHint" class="nt-note">{{ submitHint }}</p>
      <p v-else-if="submitBlock && touched" class="nt-note">{{ submitBlock }}</p>
    </section>

    <section v-else-if="tab === 'open'">
      <!-- R4（§6.3）：委托行无时间字段（接口 Delivery 未携带），不拿当前时刻冒充；
           状态文案为订单状态机散值（orderStatusLabel），status-map 册无 order.* 键——维持原文案待评审入册（§7.1 禁自封）。 -->
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
            <td>{{ orderStatusLabel(o.status) }}<template v-if="o.reason"> · {{ o.reason }}</template></td>
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
      <!-- R4（§6.3）：同「当前委托」——时间位缺（接口无时间字段）；状态章维持 orderStatusLabel 原文案待入册评审。 -->
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
            <td>{{ orderStatusLabel(o.status) }}</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-else>
      <div class="hold">
        <div>
          <h2 class="nt-panel__title">持仓</h2>
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
                <td class="mono dim">{{ clock(f.time) }}</td>
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
