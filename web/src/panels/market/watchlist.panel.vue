<script setup lang="ts">
/**
 * 自选/扫描面板：登记市场候选池（10 分钟租约，需心跳续期）+ 触发策略扫描。
 * 写操作仅 owner；扫描结果跳到 pool-manager 面板查看。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { scan, type ScanReply } from '@/api/strategy'
import { listSignals } from '@/api/signals'
import { getQuote } from '@/api/market'
import NtButton from '@/ui/NtButton.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import { useAuthStore } from '@/stores/auth'
import { useSymbolStore } from '@/stores/symbol'
import { getWorkbench } from '@/workspace/inject'
import { clock, compactAmount, messageOf, missLabel, pnlClass, price, signed } from '@/utils/format'
import { request } from '@/api/http'

const auth = useAuthStore()
const symbolStore = useSymbolStore()

const draft = ref('')
const saving = ref(false)
const scanning = ref(false)
const error = ref('')
const expiresAt = ref('')
const lastScan = ref<ScanReply | null>(null)

/** dropped: 规则号 → 只数，按数量倒序。 */
const droppedRows = computed(() =>
  Object.entries(lastScan.value?.dropped ?? {})
    .map(([rule, count]) => ({ rule, count }))
    .sort((a, b) => b.count - a.count),
)
/** misses: 原因代码 → 只数，按数量倒序（与 dropped 同一后端 map 形状）。 */
const missRows = computed(() =>
  Object.entries(lastScan.value?.misses ?? {})
    .map(([rule, count]) => ({ rule, count }))
    .sort((a, b) => b.count - a.count),
)

/**
 * 命中票列表（方案 §10-17 R1 盯盘行）：本次扫描产生买卖信号的标的行情行。
 * 数据链：scan 只回 signalIds/sellSignalIds → listSignals 反查 symbol → GetQuote 补现价/涨跌额·幅/成交额；
 * 量比 GetQuote 未携带盘中口径（附录 B-2 未上）——列显「—」不冒充（数据纪律，同 quote-sidebar 量额区先例）。
 */
interface HitRow {
  symbol: string
  last: number | null
  chg: number | null
  pctChg: number | null
  amount: number | null
  /** GetQuote 无比量字段，恒 null → 列显「—」。 */
  volRatio: number | null
  [key: string]: unknown
}
const hits = ref<HitRow[]>([])
/** 命中列表三态：ok=有行/列取数异常逐格「—」；empty=扫描无信号；failed=有信号但未能从信号列表核到标的。 */
const hitsState = ref<'ok' | 'empty' | 'failed'>('empty')
/** §7.3 首批挂载：命中行情行逐一来自 GetQuote，新鲜度取各行 asOf 最大者；全无证据显「时点未知」。 */
const hitsFreshAt = ref<number | null>(null)

const hitColumns: NtTableColumn<HitRow>[] = [
  { key: 'symbol', title: '证券', kind: 'symbol', symbolMode: 'inline', width: '150px' },
  { key: 'last', title: '现价', align: 'right' },
  { key: 'pctChg', title: '涨跌额/幅', align: 'right' },
  { key: 'amount', title: '成交额', align: 'right' },
  { key: 'volRatio', title: '量比', align: 'right' },
]

async function loadHits(reply: ScanReply) {
  hits.value = []
  hitsFreshAt.value = null
  hitsState.value = 'empty'
  const ids = new Set([...reply.signalIds, ...reply.sellSignalIds].map((id) => String(id)))
  if (!ids.size) return
  const symbols: string[] = []
  try {
    const signals = await listSignals()
    const seen = new Set<string>()
    for (const item of signals) {
      if (!ids.has(String(item.id)) || !item.symbol || seen.has(item.symbol)) continue
      seen.add(item.symbol)
      symbols.push(item.symbol)
    }
  } catch {
    hitsState.value = 'failed'
    return
  }
  if (!symbols.length) {
    hitsState.value = 'failed'
    return
  }
  const hitTimes: number[] = []
  const settled = await Promise.allSettled(
    symbols.map(async (symbol): Promise<HitRow> => {
      const q = await getQuote(symbol)
      const at = Date.parse(q.asOf || '')
      if (Number.isFinite(at)) hitTimes.push(at)
      return {
        symbol,
        last: q.last > 0 ? q.last : null,
        // 昨收缺失时涨跌无口径（P3-10c 契约：prevClose=0 是没取到）——显「—」不显假 0。
        chg: q.prevClose > 0 ? q.chg : null,
        pctChg: q.prevClose > 0 ? q.pctChg : null,
        amount: q.amount > 0 ? q.amount : null,
        volRatio: null,
      }
    }),
  )
  hits.value = settled.map((outcome, index) =>
    outcome.status === 'fulfilled'
      ? outcome.value
      : { symbol: symbols[index] ?? '', last: null, chg: null, pctChg: null, amount: null, volRatio: null },
  )
  hitsFreshAt.value = hitTimes.length ? Math.max(...hitTimes) : null
  hitsState.value = hits.value.length ? 'ok' : 'empty'
}

function openHit(row: HitRow) {
  if (!row.symbol) return
  // 与 SymbolName 自身点击合同一致：行点击同样 setSymbol + 打开行情侧栏。
  symbolStore.setSymbol(row.symbol, 'WL')
  getWorkbench()?.openPanel('quote-sidebar', { symbol: row.symbol })
}

const SYMBOL_RE = /^\d{6}\.(SZ|SH|BJ)$/i
let heartbeat: ReturnType<typeof setInterval> | undefined

async function save() {
  const symbols = draft.value
    .split(/[\s,;，；]+/)
    .map((s) => s.trim().toUpperCase())
    .filter(Boolean)
  const bad = symbols.filter((s) => !SYMBOL_RE.test(s))
  if (bad.length) {
    error.value = `代码格式不对：${bad.join('、')}（应为 000001.SZ）`
    return
  }
  saving.value = true
  error.value = ''
  try {
    const data = (await request('/api/market/watchlist', {
      method: 'POST',
      json: { owner: auth.operatorName || 'web', symbols },
    })) as Record<string, unknown>
    expiresAt.value = String(data?.expiresAt ?? data?.expires_at ?? '')
    startHeartbeat(symbols)
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    saving.value = false
  }
}

function startHeartbeat(symbols: string[]) {
  stopHeartbeat()
  if (!symbols.length) return
  // 租约 10 分钟，8 分钟续一次。
  heartbeat = setInterval(
    () => {
      void request('/api/market/watchlist', {
        method: 'POST',
        json: { owner: auth.operatorName || 'web', symbols },
      }).then((d) => {
        expiresAt.value = String((d as Record<string, unknown>)?.expiresAt ?? '')
      })
    },
    8 * 60 * 1000,
  )
}

function stopHeartbeat() {
  if (heartbeat) clearInterval(heartbeat)
  heartbeat = undefined
}

async function runScan() {
  scanning.value = true
  error.value = ''
  try {
    lastScan.value = await scan({})
    void loadHits(lastScan.value)
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    scanning.value = false
  }
}

function openPool() {
  getWorkbench()?.openPanel('pool-manager')
}

onMounted(() => {})
onBeforeUnmount(stopHeartbeat)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">自选 / 扫描</h1>
        <p class="nt-note">登记候选池租约 10 分钟，面板开着会自动续期。</p>
      </div>
    </header>

    <section class="nt-panel">
      <h2 class="nt-panel__title">候选池</h2>
      <textarea v-model="draft" class="draft" rows="3" placeholder="000001.SZ 600519.SH …（空格或逗号分隔）" />
      <div class="row">
        <NtButton v-if="!auth.isViewer()" size="sm" :loading="saving" :disabled="!draft.trim()" @click="save">登记</NtButton>
        <NtButton v-if="!auth.isViewer()" size="sm" variant="primary" :loading="scanning" @click="runScan">
          立即扫描
        </NtButton>
        <NtButton size="sm" @click="openPool">打开股池面板</NtButton>
      </div>
      <p v-if="expiresAt" class="nt-note">租约有效至 {{ clock(expiresAt) }}</p>
      <p v-if="error" class="nt-err">{{ error }}</p>
    </section>

    <section v-if="lastScan" class="nt-panel">
      <h2 class="nt-panel__title">命中票列表</h2>
      <NtFreshness label="命中行情" :at="hitsFreshAt" />
      <!-- TODO(P2 §8.1)：0 命中需落 L2 告警属 P2 范围，本批只留标注不实现。 -->
      <p v-if="!hits.length" class="nt-empty">
        {{
          hitsState === 'failed'
            ? '有信号产出但没能从信号列表核对到标的——凭下方 trace 到信号中枢核对。'
            : '本次扫描没有产生买卖信号的票——被规则剔除与漏选的原因见下方计数。'
        }}
      </p>
      <NtTable v-else :columns="hitColumns" :rows="hits" row-key="symbol" dense clickable @row-click="openHit">
        <template #cell-last="{ row }">{{ row.last != null ? price(row.last) : '—' }}</template>
        <template #cell-pctChg="{ row }">
          <!-- R1 涨跌额/幅同格，方向色走 toneOf 唯一出口（红涨绿跌在令牌，class 只有 up/down） -->
          <span :class="pnlClass(row.pctChg)">{{
            row.pctChg != null ? `${signed(row.chg)} · ${row.pctChg >= 0 ? '+' : ''}${row.pctChg.toFixed(2)}%` : '—'
          }}</span>
        </template>
        <template #cell-amount="{ row }">{{ row.amount != null ? compactAmount(row.amount) : '—' }}</template>
      </NtTable>
    </section>

    <section v-if="lastScan" class="nt-panel">
      <h2 class="nt-panel__title">本次扫描</h2>
      <p class="nt-note">
        范围 {{ lastScan.universe ?? '—' }} 只 → 命中 {{ lastScan.matched ?? '—' }} → 进模型排序
        {{ lastScan.ranked ?? '—' }}（调用 {{ lastScan.aiCalls ?? '—' }} 次
        <template v-if="lastScan.degraded">，模型降级</template>） → 买入信号 {{ lastScan.signalIds.length }} 条 / 卖出信号
        {{ lastScan.sellSignalIds.length }} 条。耗时 {{ lastScan.elapsedMs ?? '—' }} ms。
        <span v-if="lastScan.killSwitch" class="kill">扫描时急停开着，买入信号不会执行。</span>
      </p>

      <template v-if="droppedRows.length">
        <h3 class="sub">被规则剔除</h3>
        <ul class="kv">
          <li v-for="row in droppedRows" :key="row.rule">
            <span>{{ missLabel(row.rule) }}</span>
            <b>{{ row.count }} 只</b>
          </li>
        </ul>
      </template>

      <template v-if="missRows.length">
        <h3 class="sub">通过规则但没进信号（按原因）</h3>
        <ul class="kv">
          <li v-for="row in missRows" :key="row.rule">
            <span>{{ missLabel(row.rule) }}</span>
            <b>{{ row.count }} 只</b>
          </li>
        </ul>
      </template>

      <p class="nt-note">trace {{ lastScan.traceId || '—' }}，凭它可以在股池面板核对候选。</p>
    </section>
  </div>
</template>

<style scoped>
.row {
  display: flex;
  gap: 8px;
  margin-top: 8px;
  flex-wrap: wrap;
}

.draft {
  width: 100%;
  box-sizing: border-box;
  padding: 8px 10px;
  font-family: var(--nt-font-mono);
  font-size: 12.5px;
  color: var(--nt-text-1);
  background: var(--nt-bg-2);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  resize: vertical;
}

.draft:focus {
  outline: none;
  border-color: var(--nt-accent);
}

.sub {
  margin: 12px 0 0;
  font-size: 12.5px;
  font-weight: 600;
}

.kv {
  list-style: none;
  margin: 6px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
}

.kv li {
  display: flex;
  gap: 8px;
  align-items: baseline;
}

.kv b {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.sym {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-accent);
  font: inherit;
  font-family: var(--nt-font-mono);
  cursor: pointer;
}

.sym:hover {
  text-decoration: underline;
}

.kill {
  color: var(--nt-danger);
  font-weight: 600;
}
</style>
