<script setup lang="ts">
/**
 * 自选/扫描面板：登记市场候选池（10 分钟租约，需心跳续期）+ 触发策略扫描。
 * 写操作仅 owner；扫描结果跳到 pool-manager 面板查看。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { scan, type ScanReply } from '@/api/strategy'
import { listSignals } from '@/api/signals'
import { getQuote, getWatchlist, setWatchlist, type WatchLeaseView } from '@/api/market'
import NtButton from '@/ui/NtButton.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import SymbolName from '@/display/SymbolName.vue'
import { useAuthStore } from '@/stores/auth'
import { useSymbolStore } from '@/stores/symbol'
import { getWorkbench } from '@/workspace/inject'
import { compactAmount, messageOf, missLabel, pnlClass, price, signed } from '@/utils/format'
import { serverNow } from '@/utils/server-clock'
import { raiseAlert } from '@/utils/notify'

const auth = useAuthStore()
const symbolStore = useSymbolStore()

/** 本机登记 owner：与后端 SetWatchlist 同一口径（operatorName 缺省 'web'）。 */
const ownerName = computed(() => auth.operatorName || 'web')

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
  if (!ids.size) {
    notifyZeroHit()
    return
  }
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

/**
 * §8.1 扫描 0 命中落 L2 提醒（走 §8 统一漏斗：warn toast + 通知中心常显，静默/冷却都认账）。
 * 只在本函数被调用时发——调用方保证扫描本体已成功（runScan 的 try 分支）；
 * 扫描本身失败 / hitsState='failed'（取数落空）一律不响，「没取到≠0」。
 */
function notifyZeroHit() {
  void raiseAlert({
    kind: 'scan_zero_hit',
    level: 'warn',
    title: '扫描 0 命中',
    body: '本次扫描 0 命中，剔除原因见计数',
    at: Date.now(),
  })
}

function openHit(row: HitRow) {
  if (!row.symbol) return
  // 与 SymbolName 自身点击合同一致：行点击同样 setSymbol + 打开行情侧栏。
  symbolStore.setSymbol(row.symbol, 'WL')
  getWorkbench()?.openPanel('quote-sidebar', { symbol: row.symbol })
}

const SYMBOL_RE = /^\d{6}\.(SZ|SH|BJ)$/i
let heartbeat: ReturnType<typeof setInterval> | undefined

/** 租约读口回显（W3-B2，评审 §4.9.2「登记后刷新即失忆」根修）：面板挂载 GET 全 owner 租约，逐行倒计时。 */
const leases = ref<WatchLeaseView[]>([])
/** 读口失败态：true 时租约区显「状态未知」，不得以空表冒充「没有活租约」（没取到≠0）。 */
const leasesFailed = ref(false)
const nowTick = ref(serverNow())
let tickTimer: ReturnType<typeof setInterval> | undefined

/** 租约行视图：剩余毫秒按 1s tick 递减；<=0 视为已失效（后端惰性剔除，读口下次返回即缺席）。 */
const leaseRows = computed(() =>
  leases.value.map((row) => ({
    owner: row.owner,
    count: row.symbols.length,
    remain: Date.parse(row.expiresAt) - nowTick.value,
    mine: row.owner === ownerName.value,
  })),
)

async function loadLeases() {
  try {
    leases.value = await getWatchlist()
    leasesFailed.value = false
  } catch {
    // 读口失败不挡登记/扫描主流程；置失败态让列表显「状态未知」，不冒充空（也防误导用户整池替换）。
    leasesFailed.value = true
  }
}

function fmtRemain(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
}

/** 当前心跳闭包里的名单（保存失败恢复用）与代际号（迟到回包丢弃判据）。 */
let beatSymbols: string[] = []
let beatSeq = 0
/** 登记代际号：save() 每次发起即 +1；挂载恢复拿它判「GET 在飞期间用户是否已登记」。 */
let saveGen = 0
/** 卸载死旗：onMounted 的 await 之后判死，防迟到回包重启出永不清理的幽灵心跳。 */
let dead = false

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
  saveGen++ // 挂载恢复的迟到回包看到代际前进即放弃，不得回写旧名单
  saving.value = true
  error.value = ''
  // 写序防竞：保存全程先停心跳——已在飞的旧闭包心跳无法取消，若其 POST 晚于本次登记到达后端
  // 会把候选池整体改回旧名单（后端 Set 是 owner 级整池替换）。停心跳把窗口缩到本次 await 期间
  // 残存的一拍；界面侧迟到回包由 beat() 的代际号丢弃，不把旧 expires/lease 行写回。失败即恢复原心跳。
  stopHeartbeat()
  try {
    expiresAt.value = await setWatchlist(ownerName.value, symbols)
    startHeartbeat(symbols)
    void loadLeases()
  } catch (err) {
    error.value = messageOf(err)
    startHeartbeat(beatSymbols)
  } finally {
    saving.value = false
  }
}

/** 续期一拍：失败静默（与 loadLeases 同纪律）——不写 error 不扰用户，下一拍自动重试；
 *  晚到的回包按代际号丢弃（名单已换/心跳已停时不得回写旧 expires 与租约行）。 */
async function beat(symbols: string[], seq: number) {
  try {
    const expires = await setWatchlist(ownerName.value, symbols)
    if (seq !== beatSeq) return
    expiresAt.value = expires
    void loadLeases()
  } catch {
    /* 见上注释 */
  }
}

function startHeartbeat(symbols: string[]) {
  stopHeartbeat()
  beatSymbols = symbols
  if (!symbols.length) return
  const seq = beatSeq
  // 租约 10 分钟，8 分钟续一次，不按 document.hidden 跳拍：桌面壳最小化（含托盘）即 hidden，
  // 跳拍超 10 分钟租约即死、池推送静默中断——违背页注「开着本面板即续期」与 data-frame 隐藏态纪律；
  // 8 分钟一拍成本为零。系统睡眠造成的迟到空窗由 onVisible 补一拍兜底。
  heartbeat = setInterval(
    () => {
      void beat(symbols, seq)
    },
    8 * 60 * 1000,
  )
}

function stopHeartbeat() {
  if (heartbeat) clearInterval(heartbeat)
  heartbeat = undefined
  beatSeq++ // 代际+1：在飞回包全部作废
}

/** 回到前台立即补一拍（隐藏期不续、租约 10 分钟可能已死）——死态行不必等下个 8 分钟整拍。 */
function onVisible() {
  if (document.hidden || !heartbeat || !beatSymbols.length) return
  void beat(beatSymbols, beatSeq)
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

onMounted(async () => {
  // 倒计时 tick（1s，P2-10 纪律：有效期判定一律 serverNow，禁本地钟）；隐藏标签页不空转，回前台下一秒自对齐。
  tickTimer = setInterval(() => {
    if (document.hidden) return
    nowTick.value = serverNow()
  }, 1000)
  document.addEventListener('visibilitychange', onVisible)
  const genAtMount = saveGen
  await loadLeases()
  // 卸载/登记竞态护栏：GET 在飞期间面板被关（dead）或用户已完成一次登记（saveGen 前进），
  // 迟到回包一律丢弃——否则会以旧名单重启心跳，下一拍把刚登记的池整体改回（整池替换语义）。
  if (dead || genAtMount !== saveGen) return
  // 本机活租约自动回显并重启续期——面板重开不失忆；「任一桌面开着本面板即续期」口径见页注。
  const mine = leases.value.find((row) => row.owner === ownerName.value)
  if (mine && Date.parse(mine.expiresAt) > serverNow()) {
    if (!draft.value.trim()) draft.value = mine.symbols.join(' ')
    expiresAt.value = mine.expiresAt
    startHeartbeat(mine.symbols)
    // 恢复即补一拍：剩余 TTL<8 分钟的租约等不到首个整拍就到期，立即续满 10 分钟消除推送空窗。
    void beat(mine.symbols, beatSeq)
  }
})
onBeforeUnmount(() => {
  dead = true
  document.removeEventListener('visibilitychange', onVisible)
  stopHeartbeat()
  if (tickTimer) clearInterval(tickTimer)
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">自选 / 扫描</h1>
        <p class="nt-note">登记候选池租约 10 分钟；任一桌面开着本面板即每 8 分钟续期，全部关闭后约 10 分钟停止池中标的的分钟行情推送（扫描不受影响）。</p>
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
      <ul v-if="leaseRows.length" class="leases">
        <li v-for="row in leaseRows" :key="row.owner" class="lease" :class="{ 'lease--dead': row.remain <= 0 }">
          <span class="lease__owner">{{ row.owner }}<template v-if="row.mine">（本机）</template></span>
          <span class="lease__count">{{ row.count }} 只</span>
          <span v-if="row.remain > 0" class="lease__remain">剩余 {{ fmtRemain(row.remain) }}</span>
          <span v-else class="lease__dead">已失效，重新登记恢复推送</span>
        </li>
      </ul>
      <p v-else-if="leasesFailed" class="nt-note">租约读口失败，当前池状态未知——稍后重开面板复查，勿凭缺表提示把池整池改小。</p>
      <p v-else class="nt-note">当前没有活租约——登记后池中标的才有分钟行情推送。</p>
      <p v-if="error" class="nt-err">{{ error }}</p>
    </section>

    <section v-if="lastScan" class="nt-panel">
      <h2 class="nt-panel__title">命中票列表</h2>
      <NtFreshness label="命中行情" :at="hitsFreshAt" />
      <p v-if="!hits.length" class="nt-empty">
        {{
          hitsState === 'failed'
            ? '有信号产出但没能从信号列表核对到标的——凭下方 trace 到信号中枢核对。'
            : '本次扫描没有产生买卖信号的票——被规则剔除与漏选的原因见下方计数。'
        }}
      </p>
      <NtTable v-else :columns="hitColumns" :rows="hits" row-key="symbol" dense clickable @row-click="openHit">
        <!-- F10 试点（§6.5）：命中列表证券格 hover 600ms 出速览、点击进 F10 抽屉；
             拦下冒泡，防触发行点击的 setSymbol + 行情侧栏联动（行其余格仍走 openHit）。 -->
        <template #cell-symbol="{ row }">
          <span @click.stop><SymbolName :code="row.symbol" mode="inline" :f10="true" /></span>
        </template>
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

.leases {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.lease {
  display: flex;
  gap: 10px;
  align-items: baseline;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.lease--dead {
  opacity: 0.55;
}

.lease__owner {
  font-family: var(--nt-font-mono);
  color: var(--nt-text-1);
}

.lease__dead {
  color: var(--nt-warn);
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
