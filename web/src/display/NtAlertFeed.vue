<script setup lang="ts">
/**
 * 盘中异动流卡（TSP 监控中心流的 NT 重述）：严重度色条 + kind 徽标 + 时间 + 幅度。
 * kind 徽标文案/tone 只走 statusOf('move.*') 册（禁自封文案）；时间 mono text-3；
 * 列表 aria-live="polite"（实时帧前置，读屏有序播报）；点击行 emit select（面板层跳行情）。
 */
import type { IntradayAlert, IntradayAlertKind } from '@/api/market'
import NtBadge, { type BadgeTone } from '@/ui/NtBadge.vue'
import NtEmpty from '@/ui/NtEmpty.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import SymbolName from '@/display/SymbolName.vue'
import { statusOf } from '@/ui/status-map'
import { clock, pnlClass } from '@/utils/format'

withDefaults(
  defineProps<{
    alerts: IntradayAlert[]
    /** false = 异动流水未返回（missing 态）。 */
    ready?: boolean
    loading?: boolean
  }>(),
  { ready: true, loading: false },
)

const emit = defineEmits<{ (e: 'select', alert: IntradayAlert): void }>()

const KIND_META: Record<IntradayAlertKind, { text: string; tone: BadgeTone; bar: string }> = {
  surge: { text: statusOf('move.surge').text, tone: statusOf('move.surge').tone, bar: 'warn' },
  limit_up: { text: statusOf('move.limit_up').text, tone: statusOf('move.limit_up').tone, bar: 'up' },
  break: { text: statusOf('move.break').text, tone: statusOf('move.break').tone, bar: 'flat' },
}

/** 册外 kind 兜底：api 层 kind 是宽松 cast（后端新类别会原样透传），查表必须防炸——
 *  原样回显类别词 + 中性 tone/无色条（intraday-alerts.panel.vue:222 同口径）。 */
function kindMeta(kind: string): { text: string; tone: BadgeTone; bar: string } {
  return KIND_META[kind as IntradayAlertKind] ?? { text: kind, tone: 'neutral', bar: 'flat' }
}

function pctText(pct: number | null): string {
  if (pct == null) return '—'
  return `${pct >= 0 ? '+' : ''}${pct.toFixed(2)}%`
}
</script>

<template>
  <NtSkeleton v-if="loading && !ready" :rows="4" />
  <NtEmpty
    v-else-if="!ready"
    state="missing"
    title="异动流水未返回"
    hint="行情源恢复后自动刷新；实时帧走 WS 前置"
  />
  <NtEmpty
    v-else-if="!alerts.length"
    title="盘中暂无异动"
    hint="急拉 / 封板 / 开板出现后在这里实时前置"
  />
  <ol v-else class="afeed" aria-live="polite" aria-label="盘中异动流">
    <li v-for="a in alerts" :key="`${a.symbol}|${a.kind}|${a.time}`">
      <button type="button" class="afeed__row" :class="`afeed__row--${kindMeta(a.kind).bar}`" @click="emit('select', a)">
        <span class="afeed__time">{{ clock(a.time) || '—' }}</span>
        <span class="afeed__stock"><SymbolName :code="a.symbol" mode="inline" :clickable="false" /></span>
        <NtBadge :tone="kindMeta(a.kind).tone" dot>{{ kindMeta(a.kind).text }}</NtBadge>
        <span class="afeed__pct" :class="pnlClass(a.pct)">{{ pctText(a.pct) }}</span>
      </button>
    </li>
  </ol>
</template>

<style scoped>
.afeed {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.afeed__row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 32px;
  padding: 3px 8px 3px 6px;
  border: 0;
  border-left: 2px solid transparent;
  border-radius: var(--nt-radius-s);
  background: transparent;
  color: var(--nt-text-2);
  font-size: var(--nt-fs-body);
  text-align: left;
  cursor: pointer;
}

.afeed__row--warn {
  border-left-color: var(--nt-warn);
}

.afeed__row--up {
  border-left-color: var(--nt-up);
}

.afeed__row--flat {
  border-left-color: var(--nt-text-3);
}

.afeed__row:hover {
  background: var(--nt-bg-2);
}

.afeed__time {
  flex: none;
  width: 44px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--nt-text-3);
}

.afeed__stock {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.afeed__pct {
  flex: none;
  width: 64px;
  text-align: right;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.afeed__pct.up {
  color: var(--nt-up);
}

.afeed__pct.down {
  color: var(--nt-down);
}
</style>
