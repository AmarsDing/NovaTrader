<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { hotWords, timeline, type HotWord, type IntelItem } from '@/api/intel'
import { listSectors, type SectorRow } from '@/api/market'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, messageOf, pnlClass } from '@/utils/format'
import { consumePanelParams } from '@/workspace/panel-bus'
import { getWorkbench } from '@/workspace/inject'

const symbol = ref('')
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
  { key: 'overseas', title: '外围脉冲', align: 'right' },
  { key: 'leader', title: '龙头' },
]

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

async function loadSide() {
  const [wordRes, sectorRes] = await Promise.allSettled([hotWords(), listSectors(12)])
  words.value = wordRes.status === 'fulfilled' ? wordRes.value : []
  sectors.value = sectorRes.status === 'fulfilled' ? sectorRes.value : []
  if (!error.value && wordRes.status === 'rejected' && sectorRes.status === 'rejected') {
    error.value = messageOf(wordRes.reason)
  }
}

function wordClickable(word: HotWord): boolean {
  return word.type === 'stock'
}

function openMarket(code: string) {
  getWorkbench()?.openPanel('market', { symbol: code })
}

onMounted(() => {
  void loadSide()
  // 行情面板点消息带参进来：直接预填并加载时间线。
  const params = consumePanelParams('intel')
  if (params && typeof params.symbol === 'string' && params.symbol) {
    if (typeof params.highlight === 'string') highlight.value = params.highlight
    void loadNews(params.symbol)
  }
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">情报</h1>
        <p class="nt-note">消息流、热词、板块和外围脉冲。</p>
      </div>
    </header>
    <form class="nt-form" @submit.prevent="loadNews()">
      <label class="grow">
        <span>代码</span>
        <NtInput v-model="symbol" placeholder="600519.SH" mono />
      </label>
      <NtButton variant="primary" :loading="loading" @click="loadNews()">时间线</NtButton>
    </form>
    <p v-if="sentiment != null" class="nt-note">近端综合情感 {{ sentimentText(sentiment) }}</p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <div class="nt-split">
      <section class="nt-panel">
        <h2 class="nt-panel__title">消息流</h2>
        <p v-if="!items.length" class="nt-empty">还没有这条时间线。</p>
        <ul v-else class="list">
          <li v-for="item in items" :key="item.id || item.title" :class="{ hit: highlight && item.id === highlight }">
            <div class="item-head">
              <b :class="pnlClass(item.sentiment)">{{ clock(item.time) }} {{ item.source }}</b>
              <NtBadge v-if="(item.importance ?? 0) >= 4" tone="warn">重要</NtBadge>
              <span v-if="item.kind" class="kind">{{ item.kind }}</span>
            </div>
            <span>{{ item.title }}</span>
          </li>
        </ul>
      </section>
      <div class="stack">
        <section class="nt-panel">
          <h2 class="nt-panel__title">热词</h2>
          <p v-if="!words.length" class="nt-empty">热词未返回。</p>
          <ul v-else class="list">
            <li v-for="word in words" :key="word.type + word.target">
              <button
                v-if="wordClickable(word)"
                type="button"
                class="word-link"
                @click="loadNews(word.target)"
              >
                {{ word.target }}
              </button>
              <span v-else>{{ word.target }}</span>
              <b>{{ word.score ?? word.count }}</b>
            </li>
          </ul>
        </section>
        <section class="nt-panel">
          <h2 class="nt-panel__title">板块 / 外围</h2>
          <NtTable :columns="sectorColumns" :rows="sectors" row-key="code" empty="板块未返回">
            <template #cell-avgPct="{ value }">
              <span :class="pnlClass(value)">{{ value == null ? '—' : Number(value).toFixed(2) + '%' }}</span>
            </template>
            <template #cell-overseas="{ value }">{{ value ?? '—' }}</template>
            <template #cell-leader="{ value }">
              <button v-if="value" type="button" class="word-link" @click="openMarket(String(value))">
                {{ value }}
              </button>
              <template v-else>—</template>
            </template>
          </NtTable>
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

.list li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12.5px;
}

.list li.hit {
  border-left: 2px solid var(--nt-accent);
  padding-left: 8px;
}

.list li b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.item-head {
  display: flex;
  gap: 8px;
  align-items: baseline;
}

.kind {
  font-size: 11px;
  color: var(--nt-text-3);
}

.word-link {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-accent);
  font: inherit;
  font-family: var(--nt-font-mono);
  cursor: pointer;
  text-align: left;
}

.word-link:hover {
  text-decoration: underline;
}
</style>
