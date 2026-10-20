<script setup lang="ts">
/**
 * 涨停梯队面板（M02 §5 涨停生态 + §6 情绪阶段）。
 * 全市场涨跌停列表 → 按连板高度分列；炸板单列；上方是情绪五阶段与关键计数。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getSentimentDetail, listLimits, type LimitBoard, type Sentiment } from '@/api/market'
import NtButton from '@/ui/NtButton.vue'
import NtLadderTiers, { type LadderTier } from '@/display/NtLadderTiers.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import SymbolName from '@/display/SymbolName.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, compactAmount, intText, messageOf, pctText, ratioPct } from '@/utils/format'
import { dataFrame } from '@/ws/data-frame'
import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'
import SentimentStages from './SentimentStages.vue'

const detail = ref<Sentiment | null>(null)
const upBoards = ref<LimitBoard[]>([])
const downBoards = ref<LimitBoard[]>([])
const upReady = ref(false)
const downReady = ref(false)
const error = ref('')
const loading = ref(false)
/** 行情快照推送触发的整表重拉走数据帧统一调度（G1）：合帧、隐藏页降频、负载自适应。 */
const PUSH_RELOAD_MIN_INTERVAL_MS = 15000

const sealed = computed(() => upBoards.value.filter((row) => row.status === 'SEALED'))
const broken = computed(() => upBoards.value.filter((row) => row.status === 'BROKEN'))

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
      // 打板视角看带队顺序：先封板的排前面；缺封板时间的按封单额兜底。
      stocks: [...rows]
        .sort((a, b) => {
          const at = a.firstSealAt || ''
          const bt = b.firstSealAt || ''
          if (at && bt && at !== bt) return at.localeCompare(bt)
          if (at && !bt) return -1
          if (!at && bt) return 1
          return (b.sealAmount ?? 0) - (a.sealAmount ?? 0)
        })
        .map((row) => ({
          // 名称由梯队内的 SymbolName 从 security 名称字典解析，缺名显「名称未返回」（数据纪律）。
          symbol: row.symbol,
          sealAmount: row.sealAmount,
          openCount: row.openCount,
          status: row.status,
        })),
    }))
})

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

const brokenColumns: NtTableColumn<LimitBoard & Record<string, unknown>>[] = [
  { key: 'symbol', title: '证券', kind: 'symbol', symbolMode: 'inline', width: '150px' },
  { key: 'consecutive', title: '连板', align: 'right', mono: true },
  { key: 'openCount', title: '开板次数', align: 'right', mono: true },
  { key: 'lastSealAt', title: '最后封板', mono: true },
]
const brokenRows = computed(() => broken.value as unknown as (LimitBoard & Record<string, unknown>)[])

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined
let loadSequence = 0
let disposed = false

async function load() {
  const sequence = ++loadSequence
  loading.value = true
  error.value = ''
  const [sentimentRes, upRes, downRes] = await Promise.allSettled([
    getSentimentDetail(),
    listLimits('UP'),
    listLimits('DOWN'),
  ])
  if (disposed || sequence !== loadSequence) return
  detail.value = sentimentRes.status === 'fulfilled' ? sentimentRes.value : null
  upReady.value = upRes.status === 'fulfilled'
  upBoards.value = upRes.status === 'fulfilled' ? upRes.value : []
  downReady.value = downRes.status === 'fulfilled'
  downBoards.value = downRes.status === 'fulfilled' ? downRes.value : []
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
        <p class="nt-note">
          封板按连板高度分列，炸板单独统计。
          <span v-if="asOf">截至 {{ clock(asOf) }}</span>
        </p>
      </div>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </header>

    <SentimentStages
      :phase="tapeFlat ? '' : detail?.phase"
      :score-coef="tapeFlat ? null : detail?.scoreCoef"
      :position-scale="tapeFlat ? null : detail?.positionScale"
      :risk-level="detail?.riskLevel"
      :stale="noTape || !!detail?.stale"
    />

    <div class="nt-cards">
      <NtStatCard label="涨停" :value="intText(sealedCount)" tone="up" />
      <NtStatCard label="跌停" :value="intText(downCount)" tone="down" />
      <NtStatCard label="炸板率" :value="ratioPct(brokenRate)" :sub="brokenCount == null ? '炸板数量未返回' : `${brokenCount} 只炸板`" tone="warn" />
      <NtStatCard label="连板高度" :value="maxHeight == null ? '—' : `${maxHeight} 板`" tone="accent" />
      <NtStatCard
        label="赚钱效应"
        :value="pctText(detail?.profitEffect)"
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

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <h2 class="nt-panel__title">涨停梯队 · 按连板高度</h2>
      <NtLadderTiers :tiers="tiers" :empty="upReady ? '今日无涨停梯队' : '涨停名单未返回，梯队未知'" />
    </section>

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
/* 跌停名称串：中文名走在 sans 上，仅保留长串折行。 */
.break {
  word-break: break-all;
}
</style>
