<script setup lang="ts">
/**
 * 股池/黑名单面板（M06）：候选股分池查看 + 黑名单维护（owner 写）。
 * 候选列表带 stage/pool 分组和回测收益（retT1/T3/T5）。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { addBlacklist, listCandidates, removeBlacklist, type Candidate } from '@/api/strategy'
import { getOpsReadiness, type OpsReadiness } from '@/api/ops'
import NtBlockerCard from '@/display/NtBlockerCard.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { useAuthStore } from '@/stores/auth'
import { openDeeplink } from '@/utils/deeplink'
import { clock, messageOf, missLabel, pnlClass, ratioPct } from '@/utils/format'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'

const auth = useAuthStore()

// W3-2 面板偏好持久化：键版勿改，旧档自然失效。
const PREFS_KEY = 'nt.prefs.pool-manager.v1'
const PREFS_ALLOW = { pool: '*', stage: '*' } as const
const prefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)

const items = ref<Candidate[]>([])
const loading = ref(false)
const error = ref('')
const pool = ref(prefs.pool ?? '')
const stage = ref(prefs.stage ?? '')
const blSymbol = ref('')
const blReason = ref('')
const blBusy = ref(false)

const pools = computed(() => [...new Set(items.value.map((r) => r.pool).filter(Boolean))].sort())
const stages = computed(() => [...new Set(items.value.map((r) => r.stage).filter(Boolean))].sort())

const filtered = computed(() =>
  items.value.filter((r) => (!pool.value || r.pool === pool.value) && (!stage.value || r.stage === stage.value)),
)

// 每次写整档（含未变字段），单字段变化不丢兄弟字段。
watch([pool, stage], () => savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { pool: pool.value, stage: stage.value }))

// 动态字段兜底：选项就绪后校验存档值，不在集合内即回默认，防陈旧档把 select 卡死在不存在选项上。
watch(pools, (list) => {
  if (list.length && pool.value && !list.includes(pool.value)) pool.value = ''
})
watch(stages, (list) => {
  if (list.length && stage.value && !list.includes(stage.value)) stage.value = ''
})

type Row = Candidate & Record<string, unknown>
const columns: NtTableColumn<Row>[] = [
  { key: 'tradeDate', title: '交易日', mono: true },
  // 名称列合并进证券列：SymbolName inline 一格同时给名称与代码（方案 §6.1/§10-18）。
  { key: 'symbol', title: '证券', kind: 'symbol', symbolMode: 'inline', width: '150px' },
  { key: 'pool', title: '池' },
  { key: 'stage', title: '阶段' },
  { key: 'finalScore', title: '评分', align: 'right' },
  { key: 'retT1', title: 'T+1', align: 'right' },
  { key: 'retT3', title: 'T+3', align: 'right' },
  { key: 'retT5', title: 'T+5', align: 'right' },
  { key: 'missReason', title: '未入选原因' },
]
const rows = computed(() => filtered.value as unknown as Row[])

/* ---------- W3b.b 拦截位：扫描链闸门就绪引导卡（R-UI-30/36） ---------- */

/** 只列扫描链闸门（数据源+模型配置）；风控执行链闸门归下单条呈现。 */
const SCAN_GATES = ['datahub_sources', 'llm_config']
const readiness = ref<OpsReadiness | null>(null)
const scanBlockers = computed(() => (readiness.value?.blockers ?? []).filter((b) => SCAN_GATES.includes(b.gate)))
/** 就绪口取不到不盖面板：股池列表另有空态/错误态兜底，卡只在「拿到回执且未就绪」时现身。 */
async function refreshReadiness() {
  try {
    readiness.value = await getOpsReadiness()
  } catch {
    /* fail-silent：理由见上，股池主体不被就绪口拖死 */
  }
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

onMounted(() => {
  void load()
  void refreshReadiness()
})
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

    <!-- W3b.b：扫描链闸门未就绪先亮卡指路（数据源/模型未配时扫描必空，先修链再看池）。 -->
    <NtBlockerCard
      v-if="readiness && !readiness.ready && scanBlockers.length"
      :ready="false"
      :blockers="scanBlockers"
      :as-of="clock(readiness.asOf)"
      title="扫描链前置检查"
      @navigate="openDeeplink"
    />

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

    <NtErrorGuide :error="error" />

    <section class="nt-panel">
      <h2 class="nt-panel__title">候选股</h2>
      <NtTable :columns="columns" :rows="rows" row-key="id" dense :empty="loading ? '读取中…' : error ? '候选股未返回——点上方刷新重试' : '今日还没有候选股——股池标的经策略扫描出入池后在这里展开'">
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
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 8px;
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
