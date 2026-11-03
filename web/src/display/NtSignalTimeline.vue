<script setup lang="ts">
import { computed, ref } from 'vue'
import { getSignalEvents, type SignalEventItem } from '@/api/strategy'

/**
 * FE-S2 信号生命周期时间轴（BE-02）。
 * 展开时才请求事件流；事件为空或接口不可用时回退「创建/关闭」近似节点（数据纪律：标注近似，不伪造中间态）。
 */
const props = defineProps<{
  signalId: string
  /** 信号时间（创建近似节点）。 */
  time: string
  /** 平仓时间，未平仓为空串。 */
  closedAt: string
  status: string
}>()

interface TimelineNode {
  label: string
  at: string
  hint: string
}

const open = ref(false)
const loading = ref(false)
const unavailable = ref(false)
const events = ref<SignalEventItem[] | null>(null)

function fmtAt(at: string): string {
  if (!at) return '时点未知'
  const d = new Date(at)
  return Number.isNaN(d.getTime()) ? at : d.toLocaleString('zh-CN', { hour12: false })
}

/** 真实事件流转时间轴节点；无事件流时退化为「创建 → 关闭」近似两节点。 */
const nodes = computed<TimelineNode[]>(() => {
  if (events.value && events.value.length) {
    return events.value.map((ev) => ({
      label: ev.fromStatus ? `${ev.fromStatus} → ${ev.toStatus}` : `创建（${ev.toStatus || '—'}）`,
      at: fmtAt(ev.at),
      hint: [ev.actor, ev.reason, ev.traceId].filter(Boolean).join(' · '),
    }))
  }
  const approx: TimelineNode[] = [{ label: `创建（${props.status || '—'}）`, at: fmtAt(props.time), hint: '' }]
  if (props.closedAt) {
    approx.push({ label: `关闭（${props.status}）`, at: fmtAt(props.closedAt), hint: '' })
  }
  return approx
})

/** 近似模式：事件流为空或不可用，只有创建/关闭两端点。 */
const approximated = computed(() => !events.value || !events.value.length)

async function toggle() {
  open.value = !open.value
  if (!open.value || events.value !== null || loading.value) return
  if (!props.signalId) {
    events.value = []
    return
  }
  loading.value = true
  try {
    events.value = await getSignalEvents(props.signalId)
    unavailable.value = false
  } catch {
    events.value = []
    unavailable.value = true
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="signal-timeline">
    <button type="button" class="tl-toggle" @click="toggle">
      生命周期 {{ open ? '▴' : '▾' }}
    </button>
    <div v-if="open" class="tl-body">
      <p v-if="loading" class="tl-note">读取中…</p>
      <template v-else>
        <ol class="tl-list">
          <li v-for="(n, i) in nodes" :key="i" class="tl-node">
            <span class="tl-dot" />
            <div class="tl-content">
              <span class="tl-label">{{ n.label }}</span>
              <span class="tl-at">{{ n.at }}</span>
              <span v-if="n.hint" class="tl-hint">{{ n.hint }}</span>
            </div>
          </li>
        </ol>
        <p v-if="approximated" class="tl-note">
          {{ unavailable ? '事件流暂不可用，展示创建/关闭近似节点' : '暂无状态流转事件，展示创建/关闭近似节点' }}
        </p>
      </template>
    </div>
  </div>
</template>

<style scoped>
.signal-timeline {
  width: 100%;
}

.tl-toggle {
  border: 1px solid var(--nt-border-1);
  background: transparent;
  color: var(--nt-text-2);
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 4px;
  cursor: pointer;
}

.tl-toggle:hover {
  color: var(--nt-text-1);
  border-color: var(--nt-border-2);
}

.tl-body {
  margin-top: 8px;
  padding: 8px 10px;
  border: 1px solid var(--nt-border-1);
  border-radius: 6px;
  background: var(--nt-bg-1);
}

.tl-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.tl-node {
  display: flex;
  gap: 8px;
  position: relative;
  padding-bottom: 10px;
}

.tl-node:last-child {
  padding-bottom: 0;
}

.tl-node:not(:last-child)::before {
  content: '';
  position: absolute;
  left: 4px;
  top: 12px;
  bottom: 0;
  width: 1px;
  background: var(--nt-border-1);
}

.tl-dot {
  flex: none;
  width: 9px;
  height: 9px;
  margin-top: 4px;
  border-radius: 50%;
  background: var(--nt-accent);
}

.tl-node:last-child .tl-dot {
  background: var(--nt-text-3);
}

.tl-content {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  align-items: baseline;
  font-size: 12px;
}

.tl-label {
  color: var(--nt-text-1);
  font-weight: 600;
}

.tl-at {
  color: var(--nt-text-3);
  font-variant-numeric: tabular-nums;
}

.tl-hint {
  color: var(--nt-text-3);
  width: 100%;
  word-break: break-all;
}

.tl-note {
  margin: 6px 0 0;
  color: var(--nt-text-3);
  font-size: 11px;
}
</style>
