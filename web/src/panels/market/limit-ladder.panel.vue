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
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, compactAmount, intText, messageOf, pctText, ratioPct } from '@/utils/format'
import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'
import SentimentStages from './SentimentStages.vue'

const REFRESH_THROTTLE_MS = 15000

const detail = ref<Sentiment | null>(null)
const upBoards = ref<LimitBoard[]>([])
const downBoards = ref<LimitBoard[]>([])
const error = ref('')
const loading = ref(false)

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
          // 涨跌停表没有名称字段，留空由 NtLadderTiers 只显代码，不再重复两遍。
          symbol: row.symbol,
          name: '',
          sealAmount: row.sealAmount,
          openCount: row.openCount,
          status: row.status,
        })),
    }))
})

const sealedCount = computed(() => (sealed.value.length ? sealed.value.length : (detail.value?.upCount ?? 0)))
const tapeFlat = computed(() => (detail.value?.advance ?? 0) === 0 && (detail.value?.decline ?? 0) === 0)
const noTape = computed(() => !upBoards.value.length && !downBoards.value.length && tapeFlat.value)
const brokenRate = computed(() => {
  const listed = upBoards.value.length
  if (listed > 0) return broken.value.length / listed
  return detail.value?.brokenRate ?? 0
})
const maxHeight = computed(() => {
  const fromTiers = tiers.value.reduce((max, tier) => Math.max(max, tier.height), 0)
  return Math.max(fromTiers, detail.value?.maxHeight ?? 0)
})
/** 全市场封单总额（已封板股票 sealAmount 之和）——情绪指标口径。 */
const totalSealAmount = computed(() => {
  if (!sealed.value.length) return null
  return sealed.value.reduce((acc, row) => acc + (row.sealAmount ?? 0), 0)
})
/** 最高连板标（梯队里第一梯队的带队股）。 */
const topLeader = computed(() => tiers.value[0]?.stocks[0] ?? null)
const asOf = computed(() => detail.value?.asOf || upBoards.value[0]?.asOf || '')

const brokenColumns: NtTableColumn<LimitBoard & Record<string, unknown>>[] = [
  { key: 'symbol', title: '代码', mono: true },
  { key: 'consecutive', title: '连板', align: 'right', mono: true },
  { key: 'openCount', title: '开板次数', align: 'right', mono: true },
  { key: 'lastSealAt', title: '最后封板', mono: true },
]
const brokenRows = computed(() => broken.value as unknown as (LimitBoard & Record<string, unknown>)[])

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined
let lastAt = 0

async function load() {
  loading.value = true
  error.value = ''
  const [sentimentRes, upRes, downRes] = await Promise.allSettled([
    getSentimentDetail(),
    listLimits('UP'),
    listLimits('DOWN'),
  ])
  if (sentimentRes.status === 'fulfilled') detail.value = sentimentRes.value
  if (upRes.status === 'fulfilled') upBoards.value = upRes.value
  else error.value = messageOf(upRes.reason)
  if (downRes.status === 'fulfilled') downBoards.value = downRes.value
  loading.value = false
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'market') return
  const now = Date.now()
  if (now - lastAt < REFRESH_THROTTLE_MS) return
  lastAt = now
  void load()
}

onMounted(() => {
  void load()
  // subscribe 负责让 2003 通道把 market 主题推上来（引用计数），onPush 才拿得到帧；
  // 二者生命周期一致，卸载时成对释放。
  unsubscribe = subscribe('market')
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
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
      <NtStatCard label="跌停" :value="intText(detail?.downCount)" tone="down" />
      <NtStatCard label="炸板率" :value="ratioPct(brokenRate)" :sub="`${broken.length} 只炸板`" tone="warn" />
      <NtStatCard label="连板高度" :value="`${maxHeight} 板`" tone="accent" />
      <NtStatCard
        label="赚钱效应"
        :value="pctText(detail?.profitEffect)"
        :tone="(detail?.profitEffect ?? 0) >= 0 ? 'up' : 'down'"
      />
      <NtStatCard
        label="封单总额"
        :value="compactAmount(totalSealAmount)"
        :sub="
          topLeader
            ? `最高标 ${topLeader.symbol} · ${tiers[0].height} 板 · 封 ${compactAmount(topLeader.sealAmount)}`
            : '无封板'
        "
      />
    </div>

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <h2 class="nt-panel__title">涨停梯队 · 按连板高度</h2>
      <NtLadderTiers :tiers="tiers" />
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">炸板 · {{ broken.length }} 只</h2>
      <NtTable :columns="brokenColumns" :rows="brokenRows" row-key="symbol" dense empty="今日没有炸板" />
    </section>

    <section v-if="downBoards.length" class="nt-panel">
      <h2 class="nt-panel__title">跌停 · {{ downBoards.length }} 只</h2>
      <p class="nt-note break">{{ downBoards.map((row) => row.symbol).join(' · ') }}</p>
    </section>
  </div>
</template>

<style scoped>
.break {
  word-break: break-all;
  font-family: var(--nt-font-mono);
}
</style>
