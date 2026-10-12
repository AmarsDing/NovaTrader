<script setup lang="ts">
/**
 * 股池/黑名单面板（M06）：候选股分池查看 + 黑名单维护（owner 写）。
 * 候选列表带 stage/pool 分组和回测收益（retT1/T3/T5）。
 */
import { computed, onMounted, ref } from 'vue'
import { addBlacklist, listCandidates, removeBlacklist, type Candidate } from '@/api/strategy'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { useAuthStore } from '@/stores/auth'
import { messageOf, missLabel, pnlClass, ratioPct } from '@/utils/format'
import { getWorkbench } from '@/workspace/inject'

const auth = useAuthStore()

const items = ref<Candidate[]>([])
const loading = ref(false)
const error = ref('')
const pool = ref('')
const stage = ref('')
const blSymbol = ref('')
const blReason = ref('')
const blBusy = ref(false)

const pools = computed(() => [...new Set(items.value.map((r) => r.pool).filter(Boolean))].sort())
const stages = computed(() => [...new Set(items.value.map((r) => r.stage).filter(Boolean))].sort())

const filtered = computed(() =>
  items.value.filter((r) => (!pool.value || r.pool === pool.value) && (!stage.value || r.stage === stage.value)),
)

type Row = Candidate & Record<string, unknown>
const columns: NtTableColumn<Row>[] = [
  { key: 'tradeDate', title: '交易日', mono: true },
  { key: 'symbol', title: '代码', mono: true },
  { key: 'name', title: '名称' },
  { key: 'pool', title: '池' },
  { key: 'stage', title: '阶段' },
  { key: 'finalScore', title: '评分', align: 'right' },
  { key: 'retT1', title: 'T+1', align: 'right' },
  { key: 'retT3', title: 'T+3', align: 'right' },
  { key: 'retT5', title: 'T+5', align: 'right' },
  { key: 'missReason', title: '未入选原因' },
]
const rows = computed(() => filtered.value as unknown as Row[])

function openMarket(symbol: string) {
  getWorkbench()?.openPanel('market', { symbol })
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    items.value = await listCandidates()
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

async function blAdd() {
  const symbol = blSymbol.value.trim().toUpperCase()
  if (!symbol) return
  blBusy.value = true
  error.value = ''
  try {
    await addBlacklist({ symbol, reason: blReason.value.trim(), operator: auth.operatorName || 'web' })
    blSymbol.value = ''
    blReason.value = ''
    await load()
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    blBusy.value = false
  }
}

async function blRemove() {
  const symbol = blSymbol.value.trim().toUpperCase()
  if (!symbol) return
  blBusy.value = true
  error.value = ''
  try {
    await removeBlacklist(symbol)
    blSymbol.value = ''
    await load()
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    blBusy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">股池 / 黑名单</h1>
        <p class="nt-note">{{ filtered.length }} / {{ items.length }} 只候选。</p>
      </div>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </header>

    <div class="filters">
      <NtSelect
        v-model="pool"
        :options="[{ value: '', label: '全部池' }, ...pools.map((p) => ({ value: p, label: p }))]"
      />
      <NtSelect
        v-model="stage"
        :options="[{ value: '', label: '全部阶段' }, ...stages.map((s) => ({ value: s, label: s }))]"
      />
    </div>

    <section class="nt-panel">
      <h2 class="nt-panel__title">候选股</h2>
      <NtTable :columns="columns" :rows="rows" row-key="id" dense empty="今日还没有候选股">
        <template #cell-symbol="{ row }">
          <button type="button" class="sym" @click="openMarket(String(row.symbol))">{{ row.symbol }}</button>
        </template>
        <template #cell-retT1="{ row }">
          <span :class="pnlClass(row.retT1)">{{ ratioPct(row.retT1) }}</span>
        </template>
        <template #cell-retT3="{ row }">
          <span :class="pnlClass(row.retT3)">{{ ratioPct(row.retT3) }}</span>
        </template>
        <template #cell-retT5="{ row }">
          <span :class="pnlClass(row.retT5)">{{ ratioPct(row.retT5) }}</span>
        </template>
        <template #cell-missReason="{ row }">{{ missLabel(row.missReason) }}</template>
      </NtTable>
    </section>

    <section v-if="!auth.isViewer()" class="nt-panel">
      <h2 class="nt-panel__title">黑名单维护</h2>
      <div class="bl">
        <NtInput v-model="blSymbol" placeholder="000001.SZ" class="bl__symbol" />
        <NtInput v-model="blReason" placeholder="原因（可选）" class="bl__reason" />
        <NtButton size="sm" :loading="blBusy" :disabled="!blSymbol.trim()" @click="blAdd">加入</NtButton>
        <NtButton size="sm" variant="danger" :loading="blBusy" :disabled="!blSymbol.trim()" @click="blRemove">移出</NtButton>
      </div>
      <p class="nt-note">加入黑名单的标的会被策略扫描剔除；移出后立即恢复。</p>
    </section>

    <p v-if="error" class="nt-err">{{ error }}</p>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 8px;
}

.sym {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-accent);
  font: inherit;
  cursor: pointer;
}

.sym:hover {
  text-decoration: underline;
}

.up {
  color: var(--nt-up);
}

.down {
  color: var(--nt-down);
}

.bl {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
}

.bl__symbol {
  width: 150px;
}

.bl__reason {
  flex: 1;
  min-width: 160px;
}
</style>
