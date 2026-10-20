<script setup lang="ts">
/**
 * 模型运行链路面板（M05）：分层（tier）调用统计 + 手动触发分析链路观察。
 * 数据走 useBrainStore.tiers（connection-watchdog 周期刷新），分析进度走 brain 主题 WS 推送。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { analyzeBrain, type Analysis } from '@/api/brain'
import { useBrainStore } from '@/stores/brain'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import SymbolName from '@/display/SymbolName.vue'
import { linkStatus } from '@/ui/status-map'
import { clock, intText, messageOf, num } from '@/utils/format'
import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'

const brain = useBrainStore()

const symbol = ref('')
const analyzing = ref(false)
const result = ref<Analysis | null>(null)
const error = ref('')
const progress = ref<TimelineEntry[]>([])
/** 推理进度 done/total（WS brain 主题帧里带）。 */
const progDone = ref<number | null>(null)
const progTotal = ref<number | null>(null)

const progPct = computed(() => {
  if (progDone.value == null || progTotal.value == null || progTotal.value <= 0) return null
  return Math.min(100, Math.round((progDone.value / progTotal.value) * 100))
})

interface TierRow {
  tier: string
  model: string
  calls: number | null
  failures: number | null
  p50Ms: number | null
  p95Ms: number | null
  inFlight: number | null
  queued: number | null
  state: string
  [key: string]: unknown
}

const columns: NtTableColumn<TierRow>[] = [
  { key: 'tier', title: '层级' },
  { key: 'model', title: '模型', mono: true },
  { key: 'calls', title: '调用', align: 'right' },
  { key: 'failures', title: '失败', align: 'right' },
  { key: 'p50Ms', title: 'P50(ms)', align: 'right' },
  { key: 'p95Ms', title: 'P95(ms)', align: 'right' },
  { key: 'inFlight', title: '在飞', align: 'right' },
  { key: 'queued', title: '排队', align: 'right' },
  { key: 'state', title: '状态' },
]

const rows = computed<TierRow[]>(() =>
  brain.tiers.map((t) => ({
    tier: t.tier || '—',
    model: t.model || '—',
    calls: t.calls,
    failures: t.failures,
    p50Ms: t.p50Ms,
    p95Ms: t.p95Ms,
    inFlight: t.inFlight,
    queued: t.queued,
    state: t.circuitOpen ? '熔断' : t.configured ? '正常' : '未配置',
  })),
)

const lastScore = computed(() => (result.value?.score == null ? '—' : String(Math.round(result.value.score))))

/** §10-15：四维得分→链路状态键（无分 off / <60 stale / 其余 ok），tone 由 NtStatusDot 承接，文案走 linkStatus 册。 */
function dimStatus(dim: { score: number | null }): 'off' | 'stale' | 'ok' {
  if (dim.score == null) return 'off'
  return dim.score >= 60 ? 'ok' : 'stale'
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'brain') return
  const data = (msg.data ?? {}) as Record<string, unknown>
  const stage = String(data.stage || data.phase || 'progress')
  const text = String(data.message || data.symbol || '')
  const done = num(data.done)
  const total = num(data.total)
  if (done != null) progDone.value = done
  if (total != null) progTotal.value = total
  progress.value = [
    { id: `${Date.now()}-${progress.value.length}`, time: stage, title: text || stage, tone: 'accent' },
    ...progress.value.slice(0, 19),
  ]
}

async function runAnalysis() {
  const code = symbol.value.trim()
  if (!code) return
  analyzing.value = true
  error.value = ''
  progress.value = []
  progDone.value = null
  progTotal.value = null
  try {
    result.value = await analyzeBrain({ symbol: code })
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    analyzing.value = false
  }
}

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined

onMounted(() => {
  void brain.refresh()
  unsubscribe = subscribe('brain')
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
        <h1 class="nt-title">模型运行链路</h1>
        <p class="nt-note">分层调用统计随连接看门狗刷新；手动分析可观察推理过程。</p>
      </div>
      <NtButton size="sm" :loading="brain.loading" @click="brain.refresh">刷新统计</NtButton>
    </header>

    <section class="nt-panel">
      <h2 class="nt-panel__title">分层统计</h2>
      <NtTable :columns="columns" :rows="rows" row-key="tier" dense empty="还没有模型统计" />
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">手动触发分析</h2>
      <div class="run">
        <NtInput v-model="symbol" placeholder="如 000001.SZ" class="run__symbol" @keydown.enter="runAnalysis" />
        <NtButton size="sm" :loading="analyzing" :disabled="!symbol.trim()" @click="runAnalysis">分析</NtButton>
        <span v-if="result" class="run__score">
          得分 <b>{{ lastScore }}</b> · {{ result.verdict || '—' }}
        </span>
      </div>
      <p v-if="error" class="nt-err">{{ error }}</p>
      <div v-if="progPct != null" class="prog">
        <div class="prog__track"><i class="prog__bar" :style="{ width: `${progPct}%` }" /></div>
        <span class="prog__text">{{ progDone }}/{{ progTotal }}</span>
      </div>
      <NtTimeline :items="progress" :empty="analyzing ? '等待推理进度推送…' : '暂无推理进度（WS brain 主题）'" />
    </section>

    <section v-if="result" class="nt-panel">
      <h2 class="nt-panel__title result__title">
        <!-- §10-15：自制「代码+名称」双件统一 SymbolName(inline)，缺名显「名称未返回」 -->
        最近结果 · <SymbolName :code="result.symbol" mode="inline" />
      </h2>
      <p class="nt-note">
        模型 {{ result.model || '—' }}<template v-if="result.generatedAt"> · 生成于 {{ clock(result.generatedAt) }}</template>
      </p>
      <ul class="dims">
        <li v-for="dim in result.dimensions" :key="dim.name">
          <NtStatusDot :status="dimStatus(dim)" :label="dim.name" />
          <b>{{ intText(dim.score) }}</b>
          <!-- 四维状态文案走 status-map 链路册（正常/陈旧/关闭），tone 仍由 NtStatusDot 键承担 -->
          <span class="dim-state">{{ linkStatus(dimStatus(dim)).text }}</span>
          <span>{{ dim.comment }}</span>
        </li>
      </ul>
      <template v-if="result.reasons.length">
        <h3 class="sub">理由</h3>
        <ul class="lines">
          <li v-for="(line, index) in result.reasons.slice(0, 5)" :key="index">{{ line }}</li>
        </ul>
      </template>
      <template v-if="result.risks.length">
        <h3 class="sub warn">风险（模型的反对意见）</h3>
        <ul class="lines warn">
          <li v-for="(line, index) in result.risks" :key="index">{{ line }}</li>
        </ul>
      </template>
    </section>
  </div>
</template>

<style scoped>
/* SymbolName 嵌进 h2：跟随标题字号、基线对齐。 */
.result__title :deep(.sn) {
  font-size: inherit;
}

.run {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.run__symbol {
  width: 160px;
}

.run__score {
  font-size: 12.5px;
  color: var(--nt-text-2);
}

.run__score b {
  font-family: var(--nt-font-mono);
  color: var(--nt-accent);
}

.dims {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 8px;
}

.dims li {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 12.5px;
  color: var(--nt-text-2);
}

.dims b {
  font-family: var(--nt-font-mono);
  min-width: 2.2em;
  color: var(--nt-text-1);
}

.dim-state {
  font-size: 11px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

.prog {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.prog__track {
  flex: 1;
  height: 6px;
  background: var(--nt-bg-2);
  border-radius: 999px;
  overflow: hidden;
}

.prog__bar {
  display: block;
  height: 100%;
  background: var(--nt-accent);
  transition: width 0.4s ease;
}

.prog__text {
  font-size: 11px;
  color: var(--nt-text-3);
  font-family: var(--nt-font-mono);
  min-width: 48px;
  text-align: right;
}

.sub {
  margin: 12px 0 0;
  font-size: 12.5px;
  font-weight: 600;
}

.sub.warn {
  color: var(--nt-warn);
}

.lines {
  list-style: none;
  margin: 6px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
  color: var(--nt-text-2);
}

.lines.warn {
  color: var(--nt-warn);
}
</style>
