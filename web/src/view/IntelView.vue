<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { hotWords, timeline, type HotWord, type IntelItem } from '@/api/intel'
import { listSectors, type SectorRow } from '@/api/market'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, messageOf, pnlClass } from '@/utils/format'

const symbol = ref('')
const items = ref<IntelItem[]>([])
const words = ref<HotWord[]>([])
const sectors = ref<SectorRow[]>([])
const sentiment = ref<number | null>(null)
const error = ref('')
const loading = ref(false)

const sectorColumns: NtTableColumn<SectorRow>[] = [
  { key: 'name', title: '板块' },
  { key: 'avgPct', title: '涨跌', align: 'right' },
  { key: 'overseas', title: '外围脉冲', align: 'right' },
  { key: 'leader', title: '龙头' },
]

async function loadNews() {
  const code = symbol.value.trim()
  if (!code) {
    error.value = '请输入代码后再读时间线'
    return
  }
  loading.value = true
  error.value = ''
  try {
    const data = await timeline(code)
    items.value = data.items
    sentiment.value = data.sentiment
  } catch (err) {
    items.value = []
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

onMounted(loadSide)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">情报</h1>
        <p class="nt-note">消息流、热词、板块和外围脉冲。</p>
      </div>
    </header>
    <form class="nt-form" @submit.prevent="loadNews">
      <label class="grow">
        <span>代码</span>
        <NtInput v-model="symbol" placeholder="600519.SH" mono />
      </label>
      <NtButton variant="primary" :loading="loading" @click="loadNews">时间线</NtButton>
    </form>
    <p v-if="sentiment != null" class="nt-note">近端综合情感 {{ sentiment }}</p>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <div class="nt-split">
      <section class="nt-panel">
        <h2 class="nt-panel__title">消息流</h2>
        <p v-if="!items.length" class="nt-empty">还没有这条时间线。</p>
        <ul v-else class="list">
          <li v-for="item in items" :key="item.id || item.title">
            <b :class="pnlClass(item.sentiment)">{{ clock(item.time) }} {{ item.source }}</b>
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
              <span>{{ word.target }}</span>
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
            <template #cell-leader="{ value }">{{ value || '—' }}</template>
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

.list li b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}
</style>
