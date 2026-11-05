<script setup lang="ts">
/**
 * 涨停梯队面板（M02 §5 涨停生态 + §6 情绪阶段）。
 * 全市场涨跌停列表 → 按连板高度分列；炸板单列；头部情绪细条（阶段/切换时点/仓位系数）+ 关键计数。
 * FE-L3：情绪轨道下沉为头部细条并删独立大组件（原 SentimentStages.vue）；
 * 切换时点接 GetEmotionPath（复盘面板外首次引用），全口径明细仍归复盘。
 * §2.4 目标形态收口：主体三页签——高度梯队（行内封板时间+封单额，FE-L2 排序选项
 * 按首封时间/按封单额，梯队行 SymbolName 默认点击已聚焦 quote-sidebar）/
 * 封板时间线（按首封时间排序的横道，横道长=封单额相对值）/ 溢价与晋级（昨板溢价双口径+晋级率）；
 * 炸板/跌停是跨页签的今日板面状态，常驻页签区外。页签与梯队排序入面板偏好档（浏览偏好）。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  getEmotionPath,
  getLadder,
  getLimitPremium,
  getSentimentDetail,
  listLimits,
  type EmotionPath,
  type LadderSummary,
  type LimitBoard,
  type PremiumSummary,
  type Sentiment,
} from '@/api/market'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtLadderTiers, { type LadderTier } from '@/display/NtLadderTiers.vue'
import NtSegmented from '@/ui/NtSegmented.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import NtTabs from '@/ui/NtTabs.vue'
import SymbolName from '@/display/SymbolName.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { SENTIMENT_STAGES, compactAmount, intText, messageOf, phaseName, pnlClass, ppText, ratioPct } from '@/utils/format'
import { formatDate, formatMinute } from '@/utils/time'
import { serverNow } from '@/utils/server-clock'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'
import { dataFrame } from '@/ws/data-frame'
import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'

/* §2.4 三页签 + FE-L2 排序选项：页签与梯队排序都是浏览偏好，入面板偏好档（键版勿改）。 */
type TabKey = 'tiers' | 'timeline' | 'premium'
type TierSort = 'time' | 'seal'
const PREFS_KEY = 'nt.prefs.panel.limit-ladder.v1'
const PREFS_ALLOW = {
  tab: ['tiers', 'timeline', 'premium'],
  sort: ['time', 'seal'],
} as const
const savedPrefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)
const tab = ref<TabKey>((savedPrefs.tab as TabKey | undefined) ?? 'tiers')
const tierSort = ref<TierSort>((savedPrefs.sort as TierSort | undefined) ?? 'time')
const tierSortOptions = [
  { value: 'time', label: '按首封时间' },
  { value: 'seal', label: '按封单额' },
]
watch([tab, tierSort], () => {
  savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { tab: tab.value, sort: tierSort.value })
})

const detail = ref<Sentiment | null>(null)
const upBoards = ref<LimitBoard[]>([])
const downBoards = ref<LimitBoard[]>([])
const upReady = ref(false)
const downReady = ref(false)
/** 批次 A1：晋级率/昨日涨停今溢价（M02 跨日对比，接口与归一早已就绪，本批接线）。
 *  null=接口未返回（显缺口文案），不拿空对象冒充「无样本」（数据纪律）。 */
const ladder = ref<LadderSummary | null>(null)
const ladderReady = ref(false)
const premium = ref<PremiumSummary | null>(null)
const premiumReady = ref(false)
/** FE-L3 情绪细条的切换时点来源（GetEmotionPath 今日截面）；null=未取到，切换段显「—」不冒充。 */
const path = ref<EmotionPath | null>(null)
const error = ref('')
const loading = ref(false)
/** 行情快照推送触发的整表重拉走数据帧统一调度（G1）：合帧、隐藏页降频、负载自适应。 */
const PUSH_RELOAD_MIN_INTERVAL_MS = 15000

const sealed = computed(() => upBoards.value.filter((row) => row.status === 'SEALED'))
const broken = computed(() => upBoards.value.filter((row) => row.status === 'BROKEN'))

/* FE-L3 情绪细条（取代独立大组件 SentimentStages）：阶段+切换时点+仓位系数一行收编。 */
const phaseCode = computed(() => (tapeFlat.value ? '' : detail.value?.phase ?? ''))
const stageScale = computed(() => {
  if (tapeFlat.value) return null
  return detail.value?.positionScale ?? SENTIMENT_STAGES.find((s) => s.code === phaseCode.value)?.scale ?? null
})
/** 当日最近一次阶段切换（已落库截面推得）；路径未取到/只有一个截面如实显「—」，全天同阶段明说。 */
const switchText = computed(() => {
  const phased = (path.value?.points ?? []).filter((p) => p.phase)
  if (phased.length < 2) return '—'
  for (let i = phased.length - 1; i > 0; i--) {
    if (phased[i]!.phase !== phased[i - 1]!.phase) {
      const t = formatMinute(phased[i]!.time) || phased[i]!.time
      return `${t} 转 ${phaseName(phased[i]!.phase)}`
    }
  }
  return '全天未切换'
})

const tiers = computed<LadderTier[]>(() => {
  const groups = new Map<number, LimitBoard[]>()
  sealed.value.forEach((row) => {
    const height = row.consecutive && row.consecutive > 0 ? row.consecutive : 1
    const list = groups.get(height) ?? []
    list.push(row)
    groups.set(height, list)
  })
  return [...groups.entries()]
    .sort((a, b) => b[0] - a[0])
    .map(([height, rows]) => ({
      height,
      // FE-L2 排序选项：默认按首封时间（打板看带队先后），缺时间的按封单额兜底；
      // 切「按封单额」时金额优先（null 折叠 0 只用于排序，显示仍是「—」），同额回退时间序。
      stocks: [...rows]
        .sort((a, b) => {
          if (tierSort.value === 'seal') {
            const diff = (b.sealAmount ?? 0) - (a.sealAmount ?? 0)
            if (diff !== 0) return diff
          }
          const at = a.firstSealAt || ''
          const bt = b.firstSealAt || ''
          if (at && bt && at !== bt) return at.localeCompare(bt)
          if (at && !bt) return -1
          if (!at && bt) return 1
          return (b.sealAmount ?? 0) - (a.sealAmount ?? 0)
        })
        .map((row) => ({
          // 名称由梯队内的 SymbolName 从 security 名称字典解析，缺名显「名称未返回」（数据纪律）；
          // 行点击聚焦 quote-sidebar 由 SymbolName(stacked) 默认点击承接（FE-L2 行聚焦）。
          symbol: row.symbol,
          sealAmount: row.sealAmount,
          openCount: row.openCount,
          status: row.status,
          firstSealAt: row.firstSealAt,
        })),
    }))
})

/* 封板时间线（三页签之二）：全部涨停名单（封住+炸板）按首封时间升序横道；
   缺首封时间的排末段、时点显「—」（没取到≠0）；横道长=封单额相对最大值（缺额空轨不画条）。 */
const timelineRows = computed(() => {
  const maxSeal = Math.max(0, ...upBoards.value.map((row) => row.sealAmount ?? 0))
  return [...upBoards.value]
    .sort((a, b) => {
      const at = a.firstSealAt || ''
      const bt = b.firstSealAt || ''
      if (at && bt && at !== bt) return at.localeCompare(bt)
      if (at && !bt) return -1
      if (!at && bt) return 1
      return (b.sealAmount ?? 0) - (a.sealAmount ?? 0)
    })
    .map((row) => ({
      symbol: row.symbol,
      consecutive: row.consecutive ?? 0,
      status: row.status,
      openCount: row.openCount,
      sealAmount: row.sealAmount,
      timeText: formatMinute(row.firstSealAt) || '—',
      barWidth: row.sealAmount != null && maxSeal > 0 ? `${Math.max(2, Math.round((row.sealAmount / maxSeal) * 100))}%` : '0%',
    }))
})

/* 页签计数跟随取数状态：名单未返回时不带计数（不拿 0 冒充）。 */
const tabItems = computed(() => [
  { value: 'tiers', label: `高度梯队${upReady.value ? ` ${sealed.value.length}` : ''}` },
  { value: 'timeline', label: `封板时间线${upReady.value ? ` ${upBoards.value.length}` : ''}` },
  { value: 'premium', label: '溢价与晋级' },
])

const sealedCount = computed(() => upReady.value ? sealed.value.length : (detail.value?.upCount ?? null))
const downCount = computed(() => downReady.value ? downBoards.value.filter((row) => row.status === 'SEALED').length : (detail.value?.downCount ?? null))
const brokenCount = computed(() => upReady.value ? broken.value.length : (detail.value?.brokenCount ?? null))
const tapeFlat = computed(() => !detail.value || detail.value.advance == null || detail.value.decline == null || (detail.value.advance === 0 && detail.value.decline === 0))
const noTape = computed(() => !detail.value?.asOf || tapeFlat.value)
const brokenRate = computed(() => {
  const listed = upBoards.value.length
  if (upReady.value && listed > 0) return broken.value.length / listed
  return detail.value?.brokenRate ?? null
})
const maxHeight = computed(() => {
  if (upReady.value) return tiers.value.reduce((max, tier) => Math.max(max, tier.height), 0)
  return detail.value?.maxHeight ?? null
})
/** 全市场封单总额（已封板股票 sealAmount 之和）——情绪指标口径。
 * 「没取到≠0」（P3-10c 复核）：seal_amount 全链条非指针（东财缺键折叠 0 → DB Default(0) → proto 非 optional），
 * 后端恒发数值，api 层 num() 无 ?? 0，row.sealAmount == null 是契约漂移防线（当前死分支）；
 * 缺键股折叠为 0 参与求和不改变总额读数（求和加 0 无影响，仅该股封单未计入）。 */
const totalSealAmount = computed(() => {
  if (!upReady.value || sealed.value.some((row) => row.sealAmount == null)) return null
  return sealed.value.reduce((acc, row) => acc + row.sealAmount!, 0)
})
/** 最高连板标（梯队里第一梯队的带队股）。 */
const topLeader = computed(() => tiers.value[0]?.stocks[0] ?? null)
const asOf = computed(() => detail.value?.asOf || upBoards.value[0]?.asOf || '')
/** NtFreshness 收 epoch 毫秒；asOf 是 ISO 串，解析失败按缺失（null）交给册件自述。 */
const asOfMs = computed(() => {
  const t = asOf.value ? Date.parse(asOf.value) : NaN
  return Number.isFinite(t) ? t : null
})

const brokenColumns: NtTableColumn<LimitBoard & Record<string, unknown>>[] = [
  { key: 'symbol', title: '证券', kind: 'symbol', symbolMode: 'inline', width: '150px' },
  { key: 'consecutive', title: '连板', align: 'right', mono: true },
  { key: 'openCount', title: '开板次数', align: 'right', mono: true },
  { key: 'lastSealAt', title: '最后封板', mono: true },
]
const brokenRows = computed(() => broken.value as unknown as (LimitBoard & Record<string, unknown>)[])

/* ---------- 批次 A1：晋级率 / 昨日涨停今溢价（近 5 日跨日对比） ---------- */

interface LadderRow {
  step: string
  sample: string
  succeeded: string
  rate: string
  [key: string]: unknown
}
const ladderColumns: NtTableColumn<LadderRow>[] = [
  { key: 'step', title: '晋级' },
  { key: 'sample', title: '样本', align: 'right', mono: true },
  { key: 'succeeded', title: '成功', align: 'right', mono: true },
  { key: 'rate', title: '晋级率', align: 'right', mono: true },
]
/** rate 是比值口径（0.5=50%）；sample=0 显「无样本」不冒充 0%（与 monitor/detail.ts 同口径）。 */
const ladderRows = computed<LadderRow[]>(() =>
  (ladder.value?.total ?? []).map((step) => ({
    step: `${step.fromHeight} 进 ${step.fromHeight >= 3 ? '4+' : step.fromHeight + 1}`,
    sample: step.sample === 0 ? '无样本' : `${step.sample} 只`,
    succeeded: step.sample === 0 ? '—' : `${step.succeeded} 只`,
    rate: step.sample === 0 ? '—' : ratioPct(step.rate, 0),
  })),
)

/** 昨日涨停今日溢价摘要（分数口径：0.0323=3.23%）；再板率由真计数现算。 */
const premiumSummary = computed(() => premium.value?.summary ?? null)
const premiumAgainRate = computed(() => {
  const s = premiumSummary.value
  if (!s || s.sample === 0) return null
  return s.limitAgain / s.sample
})
interface PremiumTableRow {
  symbol: string
  consecutive: number
  openPremium: number
  closePremium: number
  highPremium: number
  limitAgain: string
  [key: string]: unknown
}
const premiumColumns: NtTableColumn<PremiumTableRow>[] = [
  { key: 'symbol', title: '证券', kind: 'symbol', symbolMode: 'inline', width: '150px' },
  { key: 'consecutive', title: '连板', align: 'right', mono: true },
  { key: 'openPremium', title: '开盘溢价', align: 'right', mono: true },
  { key: 'closePremium', title: '收盘溢价', align: 'right', mono: true },
  { key: 'highPremium', title: '最高溢价', align: 'right', mono: true },
  { key: 'limitAgain', title: '再板' },
]
/** 明细按收盘溢价倒序取前 8（面板是梯队速览，全量明细走监控大屏/复盘）。 */
const premiumRows = computed<PremiumTableRow[]>(() =>
  [...(premium.value?.rows ?? [])]
    .sort((a, b) => b.closePremium - a.closePremium)
    .slice(0, 8)
    .map((row) => ({
      symbol: row.symbol,
      consecutive: row.consecutive,
      openPremium: row.openPremium,
      closePremium: row.closePremium,
      highPremium: row.highPremium,
      limitAgain: row.limitAgain ? '再板' : '—',
    })),
)

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined
let loadSequence = 0
let disposed = false

async function load() {
  const sequence = ++loadSequence
  loading.value = true
  error.value = ''
  const [sentimentRes, upRes, downRes, ladderRes, premiumRes, pathRes] = await Promise.allSettled([
    getSentimentDetail(),
    listLimits('UP'),
    listLimits('DOWN'),
    getLadder(5),
    getLimitPremium(5, 50),
    getEmotionPath(formatDate(new Date(serverNow()))),
  ])
  if (disposed || sequence !== loadSequence) return
  detail.value = sentimentRes.status === 'fulfilled' ? sentimentRes.value : null
  upReady.value = upRes.status === 'fulfilled'
  upBoards.value = upRes.status === 'fulfilled' ? upRes.value : []
  downReady.value = downRes.status === 'fulfilled'
  downBoards.value = downRes.status === 'fulfilled' ? downRes.value : []
  ladderReady.value = ladderRes.status === 'fulfilled'
  ladder.value = ladderRes.status === 'fulfilled' ? ladderRes.value : null
  premiumReady.value = premiumRes.status === 'fulfilled'
  premium.value = premiumRes.status === 'fulfilled' ? premiumRes.value : null
  // 情绪路径独立成败：失败只让切换时点显「—」，不拖垮涨跌停主表的错误语义。
  path.value = pathRes.status === 'fulfilled' ? pathRes.value : null
  const failed = [sentimentRes, upRes, downRes].find((result) => result.status === 'rejected')
  if (failed?.status === 'rejected') error.value = messageOf(failed.reason)
  loading.value = false
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'market') return
  dataFrame.scheduleThrottled('limit-ladder', PUSH_RELOAD_MIN_INTERVAL_MS, () => {
    if (!disposed) void load()
  })
}

onMounted(() => {
  void load()
  // subscribe 负责让 2003 通道把 market 主题推上来（引用计数），onPush 才拿得到帧；
  // 二者生命周期一致，卸载时成对释放。
  unsubscribe = subscribe('market')
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
  disposed = true
  loadSequence++
  offPush?.()
  unsubscribe?.()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">涨停梯队</h1>
        <p class="nt-note">三页签看梯队、封板先后与昨板溢价；炸板与跌停常驻单列。</p>
      </div>
      <div class="nt-head__actions">
        <!-- 手搓「截至」换 NtFreshness 册件（直觉5 新鲜度锚点统一单源，stale 徽注自动带） -->
        <NtFreshness label="梯队" :at="asOfMs" :stale-after-ms="PUSH_RELOAD_MIN_INTERVAL_MS * 3" />
        <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
      </div>
    </header>

    <!-- FE-L3 情绪细条：五段轨道（当前阶段高亮）+ 切换时点（GetEmotionPath）+ 仓位系数；
         评分系数/风险等级全口径归复盘面板，此处不重复（L1 三处重复收编）。 -->
    <p class="tape-strip" :aria-label="`情绪阶段 ${phaseName(phaseCode)}`">
      <span class="tape-strip__label">情绪</span>
      <span class="tape-strip__track">
        <i
          v-for="s in SENTIMENT_STAGES"
          :key="s.code"
          class="tape-strip__seg"
          :class="{ on: s.code === phaseCode }"
          :title="s.label"
        />
      </span>
      <b>{{ phaseCode ? `${phaseName(phaseCode)} ${phaseCode}` : '—' }}</b>
      <span class="tape-strip__meta">切换 {{ switchText }}</span>
      <span class="tape-strip__meta">仓位系数 {{ stageScale == null ? '—' : `×${stageScale.toFixed(2)}` }}</span>
      <span v-if="noTape || detail?.stale" class="tape-strip__stale">快照过期</span>
    </p>

    <div class="nt-cards">
      <NtStatCard label="涨停" :value="intText(sealedCount)" tone="up" />
      <NtStatCard label="跌停" :value="intText(downCount)" tone="down" />
      <NtStatCard label="炸板率" :value="ratioPct(brokenRate)" :sub="brokenCount == null ? '炸板数量未返回' : `${brokenCount} 只炸板`" tone="warn" />
      <NtStatCard label="连板高度" :value="maxHeight == null ? '—' : `${maxHeight} 板`" tone="accent" />
      <NtStatCard
        label="赚钱效应"
        :value="ppText(detail?.profitEffect)"
        :tone="detail?.profitEffect == null ? 'neutral' : detail.profitEffect >= 0 ? 'up' : 'down'"
      />
      <NtStatCard
        label="封单总额"
        :value="compactAmount(totalSealAmount)"
        :sub="
          topLeader
            ? `最高标 ${topLeader.symbol} · ${tiers[0].height} 板 · 封 ${compactAmount(topLeader.sealAmount)}`
            : upReady ? '无封板' : '封板名单未返回'
        "
      />
    </div>

    <NtErrorGuide :error="error" />

    <!-- §2.4 目标形态：主体三页签——高度梯队 / 封板时间线 / 溢价与晋级（页签与梯队排序入偏好档） -->
    <NtTabs v-model="tab" :items="tabItems" class="tabs" />

    <section v-if="tab === 'tiers'" class="nt-panel">
      <div class="panel-head">
        <h2 class="nt-panel__title">涨停梯队 · 按连板高度</h2>
        <!-- FE-L2：梯队内排序选项（默认按首封时间=打板带队先后；偏好入档）。行点击聚焦侧栏由 SymbolName 默认点击承接。 -->
        <NtSegmented v-model="tierSort" size="sm" :options="tierSortOptions" />
      </div>
      <NtLadderTiers :tiers="tiers" :empty="upReady ? '今日无涨停梯队' : '涨停名单未返回，梯队未知'" />
    </section>

    <section v-else-if="tab === 'timeline'" class="nt-panel">
      <h2 class="nt-panel__title">封板时间线 · 按首封时间</h2>
      <p v-if="!upReady" class="nt-empty">涨停名单未返回，时间线未知。</p>
      <p v-else-if="!timelineRows.length" class="nt-empty">今日无涨停，时间线为空。</p>
      <template v-else>
        <ul class="timeline">
          <li v-for="row in timelineRows" :key="row.symbol" :class="{ broken: row.status === 'BROKEN' }">
            <span class="timeline__time">{{ row.timeText }}</span>
            <span class="timeline__stock"><SymbolName :code="row.symbol" mode="inline" /></span>
            <span class="timeline__height">{{ row.consecutive > 1 ? `${row.consecutive} 板` : '首板' }}</span>
            <span class="timeline__track"><i class="timeline__bar" :style="{ width: row.barWidth }" /></span>
            <span class="timeline__amount">{{ compactAmount(row.sealAmount) }}</span>
            <span v-if="row.status === 'BROKEN'" class="timeline__flag warn">炸板</span>
            <span v-else-if="(row.openCount ?? 0) > 0" class="timeline__flag">再封 ×{{ row.openCount }}</span>
          </li>
        </ul>
        <p class="nt-note foot">横道长度=封单额相对值；缺首封时间的排末段、时点显「—」。</p>
      </template>
    </section>

    <template v-else>
      <!-- 批次 A1：昨日涨停今溢价 + 连板晋级率（超短情绪复盘核心两问；接口早已就绪，本批接线） -->
      <section class="nt-panel">
        <h2 class="nt-panel__title">昨日涨停 · 今日溢价</h2>
        <p v-if="!premiumReady" class="nt-empty">溢价快照未返回。</p>
        <template v-else-if="premium?.found && premiumSummary">
          <div class="premium-stats">
            <span>样本 <b>{{ premiumSummary.sample }}</b> 只（{{ premiumSummary.baseDate || '—' }} 涨停）</span>
            <span>开盘溢价 <b :class="pnlClass(premiumSummary.openAvg)">{{ premiumSummary.openAvg == null ? '—' : ratioPct(premiumSummary.openAvg, 2) }}</b></span>
            <span>收盘溢价 <b :class="pnlClass(premiumSummary.hasClose ? premiumSummary.closeAvg : null)">{{ premiumSummary.hasClose ? ratioPct(premiumSummary.closeAvg) : '收盘未补齐' }}</b></span>
            <span>最高溢价 <b :class="pnlClass(premiumSummary.highAvg)">{{ premiumSummary.highAvg == null ? '—' : ratioPct(premiumSummary.highAvg) }}</b></span>
            <span>再板 <b>{{ premiumSummary.limitAgain }}/{{ premiumSummary.sample }}</b><template v-if="premiumAgainRate != null">（{{ ratioPct(premiumAgainRate, 0) }}）</template></span>
            <span v-if="premium.live" class="nt-note">快照未落库，实时口径</span>
          </div>
          <NtTable
            :columns="premiumColumns"
            :rows="premiumRows"
            row-key="symbol"
            dense
            :empty="premiumSummary.sample === 0 ? '昨日无涨停样本' : '明细未返回'"
          >
            <template #cell-openPremium="{ row }">
              <span :class="pnlClass(row.openPremium)">{{ ratioPct(row.openPremium, 2) }}</span>
            </template>
            <template #cell-closePremium="{ row }">
              <span :class="pnlClass(row.closePremium)">{{ ratioPct(row.closePremium, 2) }}</span>
            </template>
            <template #cell-highPremium="{ row }">
              <span :class="pnlClass(row.highPremium)">{{ ratioPct(row.highPremium, 2) }}</span>
            </template>
          </NtTable>
          <p class="nt-note foot">明细按收盘溢价倒序取前 8；开盘/收盘/最高为双口径均值（M02 §5）。</p>
        </template>
        <p v-else class="nt-empty">昨日涨停快照未生成（盘前或快照任务未跑）。</p>
      </section>

      <section class="nt-panel">
        <h2 class="nt-panel__title">连板晋级率 · 近 5 日</h2>
        <p v-if="!ladderReady" class="nt-empty">晋级率未返回。</p>
        <template v-else>
          <NtTable :columns="ladderColumns" :rows="ladderRows" dense empty="近 5 日没有连板样本" />
          <p v-if="ladder?.days[0]" class="nt-note foot">最近考察日 {{ ladder.days[0].tradeDate }}；基准日无连板数据的日期不纳入统计。</p>
        </template>
      </section>
    </template>

    <!-- 炸板/跌停：跨页签的今日板面状态，常驻页签区外 -->
    <section class="nt-panel">
      <h2 class="nt-panel__title">炸板 · {{ upReady ? broken.length : '—' }} 只</h2>
      <NtTable :columns="brokenColumns" :rows="brokenRows" row-key="symbol" dense :empty="upReady ? '今日没有炸板' : '涨停名单未返回，炸板明细未知'" />
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">跌停 · {{ downReady ? downBoards.length : '—' }} 只</h2>
      <p v-if="!downReady" class="nt-empty">跌停名单未返回。</p>
      <p v-else-if="!downBoards.length" class="nt-empty">今日没有跌停。</p>
      <p v-else class="nt-note break">
        <template v-for="(row, index) in downBoards" :key="row.symbol">
          <SymbolName :code="row.symbol" mode="compact" /><template v-if="index + 1 < downBoards.length">，</template>
        </template>
      </p>
    </section>
  </div>
</template>

<style scoped>
.nt-head__actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

/* FE-L3 情绪细条：一行收编五段轨道+切换时点+仓位系数（取代独立大卡）。 */
.tape-strip {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  margin: 0;
  padding: 7px 10px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-1);
  font-size: 12px;
  color: var(--nt-text-2);
}

.tape-strip__label {
  color: var(--nt-text-3);
  font-size: 11.5px;
}

.tape-strip__track {
  display: inline-flex;
  gap: 3px;
}

.tape-strip__seg {
  width: 14px;
  height: 6px;
  border-radius: 2px;
  background: var(--nt-bg-3);
}

.tape-strip__seg.on {
  background: var(--nt-accent);
}

.tape-strip b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-1);
}

.tape-strip__meta {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-3);
}

.tape-strip__stale {
  color: var(--nt-warn);
}

/* 跌停名称串：中文名走在 sans 上，仅保留长串折行。 */
.break {
  word-break: break-all;
}

/* 批次 A1：溢价摘要行——读数等宽右语义，红涨绿跌走 pnlClass。 */
.premium-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--nt-text-2);
}

.premium-stats b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-1);
}

.premium-stats b.up {
  color: var(--nt-up);
}

.premium-stats b.down {
  color: var(--nt-down);
}

.foot {
  margin-top: 8px;
}

.tabs {
  margin-bottom: 2px;
}

/* 梯队页签头：标题与排序选项两端对齐（FE-L2 排序选项）。 */
.panel-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

/* 封板时间线：首封时间升序横道——横道长=封单额相对值，炸板灰显弱一档。 */
.timeline {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.timeline li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 3px 6px;
  border-radius: var(--nt-radius-s);
  font-size: 12px;
}

.timeline li:hover {
  background: var(--nt-bg-2);
}

.timeline__time {
  flex: none;
  width: 44px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-3);
}

.timeline__stock {
  flex: none;
  width: 150px;
  min-width: 0;
}

.timeline__height {
  flex: none;
  width: 44px;
  font-size: 11px;
  color: var(--nt-up);
}

.timeline__track {
  flex: 1;
  height: 8px;
  background: var(--nt-bg-2);
  border-radius: 2px;
  overflow: hidden;
}

.timeline__bar {
  display: block;
  height: 100%;
  background: var(--nt-up);
  border-radius: 2px;
}

.timeline li.broken .timeline__bar {
  background: var(--nt-bg-3);
}

.timeline li.broken .timeline__height {
  color: var(--nt-text-3);
}

.timeline__amount {
  flex: none;
  width: 64px;
  text-align: right;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-2);
}

.timeline__flag {
  flex: none;
  width: 56px;
  font-size: 11px;
  color: var(--nt-text-3);
}

.timeline__flag.warn {
  color: var(--nt-warn);
}
</style>
