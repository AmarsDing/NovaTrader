<script lang="ts">
/** 龙头榜行（装配在 panels/market/useMarketBoard.ts 的 buildLeaderRows）。 */
export interface LeaderRow {
  sectorCode: string
  sectorName: string
  /** 板块均涨幅（百分点口径）；null = 未返回显「—」。 */
  avgPct: number | null
  /** 龙头证券代码（可能非规范全码，SymbolName 内部归一）。 */
  leader: string
  /** 龙头理由（空串显「—」）。 */
  leaderReason: string
}
</script>

<script setup lang="ts">
/**
 * 板块龙头榜（TSP 个股榜的口径内替代）：板块 / 龙头 / 理由 / 均涨幅 TOP8。
 * 龙头名走 SymbolName 字典（缺名显「名称未返回」，ensure 后台补名由装配层触发）；
 * 理由超长截断 + title 悬浮全文；点击行 emit select(龙头代码)。
 */
import NtEmpty from '@/ui/NtEmpty.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import SymbolName from '@/display/SymbolName.vue'
import { ppText, pnlClass } from '@/utils/format'

withDefaults(
  defineProps<{
    rows: LeaderRow[]
    /** false = 板块快照未返回（missing 态）。 */
    ready?: boolean
    loading?: boolean
  }>(),
  { ready: true, loading: false },
)

const emit = defineEmits<{ (e: 'select', symbol: string): void }>()
</script>

<template>
  <NtSkeleton v-if="loading && !ready" :rows="4" />
  <NtEmpty
    v-else-if="!ready"
    state="missing"
    title="板块快照未返回"
    hint="板块任务恢复后自动刷新"
  />
  <NtEmpty
    v-else-if="!rows.length"
    title="暂无龙头数据"
    hint="板块龙头由板块热度快照产出"
  />
  <ol v-else class="lboard">
    <li v-for="(row, i) in rows" :key="row.sectorCode || row.sectorName">
      <button type="button" class="lboard__row" @click="emit('select', row.leader)">
        <span class="lboard__rank">{{ i + 1 }}</span>
        <span class="lboard__sector">{{ row.sectorName }}</span>
        <span class="lboard__leader"><SymbolName :code="row.leader" mode="compact" :clickable="false" /></span>
        <span class="lboard__reason" :title="row.leaderReason || undefined">{{ row.leaderReason || '—' }}</span>
        <span class="lboard__pct" :class="pnlClass(row.avgPct)">{{ ppText(row.avgPct) }}</span>
      </button>
    </li>
  </ol>
</template>

<style scoped>
.lboard {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.lboard__row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 32px;
  padding: 4px 6px;
  border: 0;
  border-radius: var(--nt-radius-s);
  background: transparent;
  color: var(--nt-text-2);
  font-size: var(--nt-fs-body);
  text-align: left;
  cursor: pointer;
}

.lboard__row:hover {
  background: var(--nt-bg-2);
}

.lboard__rank {
  flex: none;
  width: 16px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--nt-text-3);
}

.lboard__sector {
  flex: none;
  max-width: 7em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--nt-text-1);
}

.lboard__leader {
  flex: none;
  min-width: 5em;
}

.lboard__reason {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}

.lboard__pct {
  flex: none;
  width: 56px;
  text-align: right;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.lboard__pct.up {
  color: var(--nt-up);
}

.lboard__pct.down {
  color: var(--nt-down);
}
</style>
