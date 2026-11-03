<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { getIndexSnapshot, type IndexQuoteView } from '@/api/indexSnapshot'

/** 三大指数快照条（BE-10）。自拉数据、自轮询；接口失败降级为注记，不影响页面其余功能。 */
const props = withDefaults(defineProps<{ refreshMs?: number }>(), { refreshMs: 60000 })

const indices = ref<IndexQuoteView[]>([])
const asOf = ref('')
const error = ref('')
let timer: ReturnType<typeof setInterval> | null = null

function fmt(n: number): string {
  return Number.isFinite(n) ? n.toFixed(2) : '—'
}

function fmtPct(n: number): string {
  if (!Number.isFinite(n)) return '—'
  return `${n > 0 ? '+' : ''}${n.toFixed(2)}%`
}

function cls(n: number): string {
  if (!Number.isFinite(n) || n === 0) return ''
  return n > 0 ? 'up' : 'down'
}

async function load() {
  try {
    const snap = await getIndexSnapshot()
    indices.value = snap.indices
    asOf.value = snap.asOf
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

onMounted(() => {
  load()
  if (props.refreshMs > 0) timer = setInterval(load, props.refreshMs)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="index-strip" :title="asOf ? `快照时间 ${asOf}` : ''">
    <span v-if="error" class="hint">指数快照暂不可用</span>
    <template v-else>
      <div v-for="ix in indices" :key="ix.code" class="ix" :class="{ stale: ix.stale || !ix.found }">
        <span class="name">{{ ix.name || ix.code }}</span>
        <template v-if="ix.found">
          <span class="last">{{ fmt(ix.last) }}</span>
          <span class="chg" :class="cls(ix.pctChg)">{{ fmtPct(ix.pctChg) }}</span>
          <span v-if="ix.stale" class="stale-tag">{{ ix.source === '1d' ? '昨收' : '旧' }}</span>
        </template>
        <span v-else class="last">—</span>
      </div>
      <span v-if="!indices.length" class="hint">指数快照加载中…</span>
    </template>
  </div>
</template>

<style scoped>
.index-strip {
  display: flex;
  gap: 20px;
  align-items: baseline;
  flex-wrap: wrap;
  padding: 6px 12px;
  border: 1px solid var(--nt-border-1);
  border-radius: 6px;
  background: var(--nt-bg-1);
}

.ix {
  display: flex;
  gap: 6px;
  align-items: baseline;
  font-size: 13px;
}

.ix .name {
  color: var(--nt-text-2);
}

.ix .last {
  color: var(--nt-text-1);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.chg {
  font-variant-numeric: tabular-nums;
}

.chg.up {
  color: var(--nt-up);
}

.chg.down {
  color: var(--nt-down);
}

.ix.stale .last,
.ix.stale .chg {
  color: var(--nt-text-3);
}

.stale-tag {
  font-size: 11px;
  color: var(--nt-text-3);
  border: 1px solid var(--nt-border-2);
  padding: 0 4px;
  border-radius: 3px;
}

.hint {
  color: var(--nt-text-3);
  font-size: 12px;
}
</style>
