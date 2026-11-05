<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { hotWords, timeline, type HotWord, type IntelItem } from '@/api/intel'
import { listSectors, type SectorRow } from '@/api/market'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtInput from '@/ui/NtInput.vue'
import IntelRow from '@/display/IntelRow.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import SymbolName from '@/display/SymbolName.vue'
import { clock, compactAmount, intText, messageOf, pnlClass, ppText, ratioPct } from '@/utils/format'
import { normalizeCode } from '@/utils/symbol'
import { useSymbolStore } from '@/stores/symbol'
import { consumePanelParams, onPanelParams } from '@/workspace/panel-bus'
import { getWorkbench } from '@/workspace/inject'

const symbol = ref('')
/** QB 参数直达时同步全局当前证券（§8 恒随），手工查询留在面板内不写。 */
const symbolStore = useSymbolStore()
const items = ref<IntelItem[]>([])
const words = ref<HotWord[]>([])
const sectors = ref<SectorRow[]>([])
const sentiment = ref<number | null>(null)
const error = ref('')
const loading = ref(false)
/** 面板参数带入的高亮消息 id（行情面板点消息跳来）。 */
const highlight = ref('')

const sectorColumns: NtTableColumn<SectorRow>[] = [
  { key: 'name', title: '板块' },
  { key: 'avgPct', title: '涨跌', align: 'right' },
  /* 批次 A9：板块全景补全——「哪个板块在热、为什么」：涨停数/上涨占比/成交额/龙头理由。 */
  { key: 'upLimitCount', title: '涨停数', align: 'right' },
  { key: 'advanceRatio', title: '上涨占比', align: 'right' },
  { key: 'amount', title: '成交额', align: 'right' },
  { key: 'overseas', title: '外围脉冲', align: 'right' },
  { key: 'leader', title: '龙头' },
  { key: 'leaderReason', title: '龙头逻辑' },
]

/* 批次 A9：消息流重要度筛选（后端 importance 早已下发，只渲染过 ≥4 徽章未做筛选）。 */
const minImportance = ref('')
const importanceOptions = [
  { value: '', label: '全部重要度' },
  { value: '3', label: '重要度 ≥3' },
  { value: '4', label: '只看重要（≥4）' },
]
const filteredItems = computed(() => {
  if (!minImportance.value) return items.value
  const min = Number(minImportance.value)
  return items.value.filter((it) => (it.importance ?? 0) >= min)
})

/** §7.3 收编：情报时点取时间线条目 publish_time 最大者（SignalView/watchlist 先例——行自带时间戳直喂，
 *  取最大者为最新证据）；无条目/全非法 → null 显「时点未知」，不冒充当前时刻。
 *  本视图无实时链路证据（无 WS 订阅），cut 不挂——不拿无时源判定冒充断流。 */
const intelFreshAt = computed(() => {
  let best: number | null = null
  for (const item of items.value) {
    const t = Date.parse(item.time)
    if (Number.isFinite(t) && (best === null || t > best)) best = t
  }
  return best
})

/** 综合情感量纲说明：后端口径 -1（利空）到 +1（利好），0 为中轴。 */
function sentimentText(value: number | null): string {
  if (value == null) return ''
  return `${value.toFixed(2)}（-1 利空 ~ +1 利好）`
}

async function loadNews(codeOverride?: string) {
  const code = (codeOverride ?? symbol.value).trim()
  if (!code) {
    error.value = '请输入代码后再读时间线'
    return
  }
  symbol.value = code
  // 手动加载（表单/热词）后旧的高亮不再适用，清掉避免错标记。
  if (codeOverride === undefined || codeOverride !== '') highlight.value = ''
  loading.value = true
  error.value = ''
  try {
    const data = await timeline(code)
    items.value = data.items
    sentiment.value = data.sentiment
  } catch (err) {
    items.value = []
    sentiment.value = null
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

function applySideResults(wordRes: PromiseSettledResult<HotWord[]>, sectorRes: PromiseSettledResult<SectorRow[]>) {
  words.value = wordRes.status === 'fulfilled' ? wordRes.value : words.value
  sectors.value = sectorRes.status === 'fulfilled' ? sectorRes.value : sectors.value
}

async function loadSide() {
  const [wordRes, sectorRes] = await Promise.allSettled([hotWords(), listSectors(12)])
  applySideResults(wordRes, sectorRes)
  if (!error.value && wordRes.status === 'rejected' && sectorRes.status === 'rejected') {
    error.value = messageOf(wordRes.reason)
  }
}

/**
 * 静默追帧（W3-1 值守面）：30s 重拉侧栏热词/板块；已加载代码的时间线一并静默补帧——
 * 不翻 loading 章、不清高亮、失败保留旧数据（消息断更由行内时间戳与面板头新鲜度章表达）。
 */
async function refreshQuiet() {
  applySideResults(...(await Promise.allSettled([hotWords(), listSectors(12)])))
  const code = symbol.value.trim()
  if (!code) return
  try {
    const data = await timeline(code)
    items.value = data.items
    sentiment.value = data.sentiment
  } catch {
    /* 静默：保留旧时间线 */
  }
}

function wordClickable(word: HotWord): boolean {
  return word.type === 'stock'
}

function openMarket(code: string) {
  getWorkbench()?.openPanel('market', { symbol: code })
}

/** 面板参数（symbol/highlight）统一入口：冷启动 consume + 已挂载时 openPanel 实时推送。 */
function applyPanelParams(params: Record<string, unknown>) {
  // 归一兜底：总线进站已归一，这里再兜保证时间线 REST 请求用通达信形态。
  const code = typeof params.symbol === 'string' ? normalizeCode(params.symbol) : ''
  if (!code) return
  highlight.value = typeof params.highlight === 'string' ? params.highlight : ''
  symbolStore.setSymbol(code, 'QB')
  void loadNews(code)
}

const offPanelParams = onPanelParams('intel', applyPanelParams)

/** W3-1 值守面静默追帧节奏：与 Ops/数据源健康同款 30s、隐藏页暂停。 */
const QUIET_POLL_MS = 30_000
let pollTimer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  void loadSide()
  const params = consumePanelParams('intel')
  if (params) applyPanelParams(params)
  pollTimer = setInterval(() => {
    if (document.hidden) return
    void refreshQuiet()
  }, QUIET_POLL_MS)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
  offPanelParams()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">情报</h1>
        <p class="nt-note">消息流、热词、板块和外围脉冲。</p>
      </div>
      <NtFreshness label="情报" :at="intelFreshAt" />
    </header>
    <form class="nt-form" @submit.prevent="loadNews()">
      <label class="grow">
        <span>代码</span>
        <NtInput v-model="symbol" placeholder="600519.SH" mono />
      </label>
      <NtButton variant="primary" :loading="loading" @click="loadNews()">时间线</NtButton>
    </form>
    <p v-if="sentiment != null" class="nt-note">近端综合情感 {{ sentimentText(sentiment) }}</p>
    <NtErrorGuide :error="error" />
    <div class="nt-split">
      <section class="nt-panel">
        <h2 class="nt-panel__title">消息流</h2>
        <!-- 批次 A9：重要度筛选 + 计数（筛到几条例行可见） -->
        <div class="news-tools">
          <NtSelect v-model="minImportance" :options="importanceOptions" />
          <span v-if="items.length" class="nt-note">{{ filteredItems.length }} / {{ items.length }} 条</span>
        </div>
        <p v-if="!items.length" class="nt-empty">还没有这条时间线。</p>
        <p v-else-if="!filteredItems.length" class="nt-empty">当前重要度筛选下没有消息——放宽筛选再看看。</p>
        <ul v-else class="list">
          <!-- W0-3b/FE-I4：消息行=IntelRow 单一事实组件（时点/来源/重要度/kind/原文外链唯一出处） -->
          <IntelRow v-for="item in filteredItems" :key="item.id || item.title" :item="item" :hit="highlight === item.id" />
        </ul>
      </section>
      <div class="stack">
        <section class="nt-panel">
          <h2 class="nt-panel__title">热词</h2>
          <p v-if="!words.length" class="nt-empty">热词未返回。</p>
          <ul v-else class="list">
            <li v-for="word in words" :key="word.type + word.target">
              <!-- §10-6：热词为结构化证券目标（type==='stock'）→ SymbolName inline；
                   保留本页「读该票时间线」联动，打开行情侧栏走组件自身合同；非个股热词纯文本不解析。 -->
              <SymbolName v-if="wordClickable(word)" :code="word.target" @click="loadNews(word.target)" />
              <span v-else>{{ word.target }}</span>
              <b>{{ word.score ?? word.count }}</b>
            </li>
          </ul>
        </section>
        <section class="nt-panel">
          <h2 class="nt-panel__title">板块 / 外围</h2>
          <NtTable :columns="sectorColumns" :rows="sectors" row-key="name" empty="板块未返回">
            <template #cell-avgPct="{ value }">
              <span :class="pnlClass(value)">{{ ppText(value) }}</span>
            </template>
            <template #cell-upLimitCount="{ value }">{{ intText(value) }}</template>
            <!-- advanceRatio 份额口径（0.62=62%）走 ratioPct；amount 元走 compactAmount。 -->
            <template #cell-advanceRatio="{ value }">{{ ratioPct(value, 0) }}</template>
            <template #cell-amount="{ value }">{{ compactAmount(value) }}</template>
            <template #cell-overseas="{ value }">
              <span :class="pnlClass(value)">{{ ppText(value) }}</span>
            </template>
            <template #cell-leader="{ value }">
              <!-- §10-6：龙头为结构化证券目标（leaderSymbol）→ SymbolName inline；
                   保留本页「打开行情」联动，缺代码显「—」。 -->
              <SymbolName v-if="value" :code="String(value)" @click="openMarket(String(value))" />
              <template v-else>—</template>
            </template>
          </NtTable>
          <p v-if="sectors[0]?.asOf" class="nt-note foot">板块快照 {{ clock(sectors[0].asOf) }}</p>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.stack {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.list {
  list-style: none;
  margin: 10px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

/* 批次 A9：消息流工具行（筛选 + 计数）。行级样式（item-head/imp/kind/ext/hit）已随 W0-3b 收进 IntelRow 组件。 */
.news-tools {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 6px 0 0;
}

.news-tools :deep(.nt-select select) {
  min-width: 110px;
}

.foot {
  margin: 8px 0 0;
}
</style>
