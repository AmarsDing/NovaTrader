<script setup lang="ts">
/**
 * 涨停聚焦（独立详情视图）——与「综合扫描」职责拆开：
 * 综合扫描只保留市场阶段/涨跌家数/广度/聚合读数等诊断指标；
 * 这里只负责个股层：封板/最后开板时间、连板、开板次数、封单、板块归属、可跳个股。
 * 数据复用 listLimits('UP') 与监控页同源的 10s 刷新（仅在弹层可见时轮询，不另起一条常驻轮询）。
 * 缺开板时间显「未采集」，接口失败显「未返回」——都不与前端的 0 或 as_of 快照混淆。
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { listLimits } from '@/api/market'
import { sectorMembership } from '@/api/membership'
import type { LimitBoard } from '@/api/types/market'
import { createModalFocus } from '@/ui/modal-focus'
import { useSymbolStore } from '@/stores/symbol'
import { getWorkbench } from '@/workspace/inject'
import { messageOf } from '@/utils/format'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (event: 'close'): void }>()

const symbolStore = useSymbolStore()

interface Row extends LimitBoard {
  sectors: string[]
}

const rows = ref<Row[]>([])
const loading = ref(false)
const failed = ref(false)
const error = ref('')
const sectorFailed = ref(false)
const asOf = ref('')
const sheetEl = ref<HTMLElement | null>(null)
const focus = createModalFocus()

const sorted = computed(() =>
  [...rows.value].sort((a, b) => {
    const rank = (row: Row) => (row.status === 'SEALED' || row.status === 'sealed' ? 0 : 1)
    if (rank(a) !== rank(b)) return rank(a) - rank(b)
    const ca = a.consecutive ?? 0
    const cb = b.consecutive ?? 0
    if (ca !== cb) return cb - ca
    return (a.firstSealAt || 'zzz').localeCompare(b.firstSealAt || 'zzz')
  }),
)

const sealedCount = computed(() => rows.value.filter((r) => r.status === 'SEALED' || r.status === 'sealed').length)
const brokenCount = computed(() => Math.max(0, rows.value.length - sealedCount.value))
const maxHeight = computed(() => (rows.value.length ? Math.max(...rows.value.map((r) => r.consecutive ?? 0)) : null))
const totalSeal = computed(() => {
  if (!rows.value.length) return null
  return rows.value.reduce((sum, r) => sum + (r.sealAmount ?? 0), 0)
})

function timeText(value: string | undefined): string {
  if (!value) return '未采集'
  const ms = Date.parse(value)
  if (!Number.isFinite(ms)) return value
  return new Date(ms).toLocaleTimeString('zh-CN', { hour12: false })
}

function moneyText(value: number | null): string {
  if (value == null) return '未采集'
  if (value >= 100_000_000) return `${(value / 100_000_000).toFixed(2)} 亿`
  if (value >= 10_000) return `${(value / 10_000).toFixed(0)} 万`
  return value.toFixed(0)
}

async function load() {
  loading.value = true
  failed.value = false
  error.value = ''
  sectorFailed.value = false
  try {
    const boards = await listLimits('UP')
    asOf.value = boards.find((b) => b.asOf)?.asOf || ''
    const symbols = boards.map((b) => b.symbol).filter(Boolean)
    let sectors: Map<string, string[]> | null = null
    if (symbols.length) {
      try {
        const res = await sectorMembership(symbols.slice(0, 200))
        sectors = new Map(res.items.map((item) => [item.symbol, item.sectors.map((sec) => sec.name)]))
      } catch {
        sectorFailed.value = true
      }
    }
    rows.value = boards.map((b) => ({
      ...b,
      sectors: sectors?.get(b.symbol) ?? [],
    }))
  } catch (err) {
    failed.value = true
    rows.value = []
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setInterval> | undefined

function stopTimer() {
  if (timer) clearInterval(timer)
  timer = undefined
}

function startTimer() {
  stopTimer()
  timer = setInterval(() => {
    if (document.visibilityState !== 'visible') return
    void load()
  }, 10_000)
}

watch(
  () => props.open,
  async (open) => {
    if (open) {
      await nextTick()
      focus.activate(sheetEl.value, () => emit('close'))
      void load()
      startTimer()
    } else {
      stopTimer()
      focus.deactivate()
    }
  },
)

function goto(row: Row) {
  if (!row.symbol) return
  symbolStore.setSymbol(row.symbol, 'NOTIFY')
  const code = symbolStore.code || row.symbol
  const bench = getWorkbench()
  bench?.openPanel('market', { symbol: code })
  bench?.openPanel('trade', { symbol: code })
}

function close() {
  emit('close')
}

onBeforeUnmount(() => {
  stopTimer()
})
</script>

<template>
  <Teleport to="body">
    <div v-if="props.open" class="nt-pop" role="dialog" aria-modal="true" aria-labelledby="nt-limit-focus-title" @click.self="close">
      <section ref="sheetEl" class="nt-pop__sheet nt-pop__sheet--limit limit-sheet" tabindex="-1">
        <header>
          <div>
            <h2 id="nt-limit-focus-title">涨停聚焦</h2>
            <p>个股层只在这里看：封板/开板时间、连板、封单、板块归属；市场诊断请回综合扫描。</p>
          </div>
          <button type="button" @click="close">关闭</button>
        </header>
        <div class="nt-pop__body">
          <dl class="limit-summary">
            <div><dt>已封板</dt><dd>{{ failed ? '未返回' : sealedCount }}</dd></div>
            <div><dt>炸板</dt><dd>{{ failed ? '未返回' : brokenCount }}</dd></div>
            <div><dt>最高板</dt><dd>{{ failed || maxHeight == null ? (failed ? '未返回' : '—') : `${maxHeight} 板` }}</dd></div>
            <div><dt>封单总额</dt><dd>{{ failed ? '未返回' : moneyText(totalSeal) }}</dd></div>
            <div><dt>数据时间</dt><dd>{{ asOf ? timeText(asOf) : '未采集' }}</dd></div>
          </dl>

          <p v-if="failed" class="limit-state" data-state="fail">涨停接口未返回（{{ error || '未知错误' }}）——这里不把失败显示为 0。</p>
          <p v-else-if="loading && !rows.length" class="limit-state">读取中…</p>
          <p v-else-if="!rows.length" class="limit-state" data-state="empty">今天没有涨停股。</p>
          <p v-if="sectorFailed" class="limit-state" data-state="sector">板块未取到——表格里的板块列留空，不用「未归属」冒充。</p>

          <div v-if="rows.length" class="limit-tablewrap" data-scroll="x">
            <table class="limit-table">
              <thead>
                <tr>
                  <th>证券</th>
                  <th>状态</th>
                  <th class="num">连板</th>
                  <th>首封</th>
                  <th>最后封板</th>
                  <th>最后开板</th>
                  <th class="num">开板次数</th>
                  <th class="num">封单</th>
                  <th>所属板块</th>
                  <th>个股</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in sorted" :key="row.symbol">
                  <td class="mono">{{ row.symbol }}</td>
                  <td>{{ row.status === 'SEALED' || row.status === 'sealed' ? '封板' : '炸板' }}</td>
                  <td class="num">{{ row.consecutive == null ? '未采集' : `${row.consecutive} 板` }}</td>
                  <td class="mono">{{ timeText(row.firstSealAt) }}</td>
                  <td class="mono">{{ timeText(row.lastSealAt) }}</td>
                  <td class="mono" :data-missing="!row.lastBreakAt">{{ timeText(row.lastBreakAt) }}</td>
                  <td class="num">{{ row.openCount == null ? '未采集' : row.openCount }}</td>
                  <td class="num">{{ moneyText(row.sealAmount) }}</td>
                  <td>{{ row.sectors.length ? row.sectors.join(' · ') : sectorFailed ? '板块未取到' : '未归属' }}</td>
                  <td><button type="button" class="limit-goto" :disabled="!row.symbol" @click="goto(row)">看行情</button></td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="limit-foot">开板时间「未采集」= 当日该股从未开板或历史数据未落该列，不用快照时间冒充开板时间。</p>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.limit-sheet {
  min-width: 0;
}

.limit-summary {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 8px;
  margin: 0 0 10px;
}

.limit-summary > div {
  min-width: 0;
  padding: 8px 10px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.limit-summary dt {
  font-size: 11px;
  color: var(--nt-text-3);
}

.limit-summary dd {
  margin: 4px 0 0;
  font-family: var(--nt-font-mono);
  font-size: 14px;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.limit-state {
  margin: 6px 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--nt-text-2);
  overflow-wrap: anywhere;
}

.limit-state[data-state='fail'],
.limit-state[data-state='sector'] {
  color: var(--nt-warn);
}

.limit-tablewrap {
  width: 100%;
  max-width: 100%;
  min-width: 0;
  overflow-x: auto;
  overflow-y: hidden;
}

.limit-table {
  width: 100%;
  min-width: 760px;
  border-collapse: collapse;
  font-size: 12px;
  white-space: nowrap;
}

.limit-table th,
.limit-table td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--nt-border-1);
  text-align: left;
}

.limit-table th {
  font-size: 11px;
  color: var(--nt-text-3);
}

.limit-table .num {
  text-align: right;
  font-family: var(--nt-font-mono);
}

.limit-table .mono {
  font-family: var(--nt-font-mono);
}

.limit-table td[data-missing='true'] {
  color: var(--nt-text-3);
}

.limit-goto {
  border: 1px solid var(--nt-border-2);
  border-radius: 999px;
  background: transparent;
  color: var(--nt-text-1);
  font: inherit;
  font-size: 11.5px;
  padding: 2px 10px;
  cursor: pointer;
}

.limit-goto:disabled {
  color: var(--nt-text-3);
  cursor: default;
}

.limit-foot {
  margin: 8px 0 0;
  font-size: 11px;
  line-height: 1.5;
  color: var(--nt-text-3);
  overflow-wrap: anywhere;
}

@media (max-width: 640px) {
  .limit-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
