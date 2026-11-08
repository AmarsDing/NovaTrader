<script setup lang="ts">
/**
 * 板块热度双列榜（TSP 概念/行业热度卡的单口径重述）：领涨 = avgPct>0 前 5，领跌 = avgPct<0 前 5
 * （拆分在装配层 splitSectorRanks，本件只渲染）。
 * 行 = 序号 + 板块名 +「N 只·龙头」+ 均涨幅；点击行 = 看该股龙头行情（emit select，面板层接线）。
 * avgPct 是百分点口径（1.23=1.23%）走 ppText；红涨绿跌 pnlClass；龙头名走 SymbolName 字典
 * （行点击由行承担，SymbolName 置 clickable=false 防嵌套点击）。
 */
import type { SectorRow } from '@/api/market'
import NtEmpty from '@/ui/NtEmpty.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import SymbolName from '@/display/SymbolName.vue'
import { ppText, pnlClass } from '@/utils/format'

withDefaults(
  defineProps<{
    gainers: SectorRow[]
    losers: SectorRow[]
    /** false = 板块快照未返回（missing 态）。 */
    ready?: boolean
    loading?: boolean
  }>(),
  { ready: true, loading: false },
)

const emit = defineEmits<{ (e: 'select', sector: SectorRow): void }>()
</script>

<template>
  <NtSkeleton v-if="loading && !ready" :rows="4" />
  <NtEmpty
    v-else-if="!ready"
    state="missing"
    title="板块快照未返回"
    hint="板块任务恢复后自动刷新"
  />
  <div v-else class="srank">
    <div class="srank__col" role="group" aria-label="领涨板块">
      <p class="srank__head up">领涨</p>
      <p v-if="!gainers.length" class="srank__none">暂无上涨板块</p>
      <button
        v-for="(s, i) in gainers"
        :key="s.code || s.name"
        type="button"
        class="srank__row"
        :disabled="!s.leader"
        :title="s.leaderReason || undefined"
        @click="emit('select', s)"
      >
        <span class="srank__rank">{{ i + 1 }}</span>
        <span class="srank__name">{{ s.name }}</span>
        <span class="srank__meta">
          <template v-if="s.memberCount != null">{{ s.memberCount }} 只</template>
          <template v-if="s.leader">
            · 龙头 <SymbolName :code="s.leader" mode="compact" :clickable="false" />
          </template>
        </span>
        <span class="srank__pct" :class="pnlClass(s.avgPct)">{{ ppText(s.avgPct) }}</span>
      </button>
    </div>
    <div class="srank__col" role="group" aria-label="领跌板块">
      <p class="srank__head down">领跌</p>
      <p v-if="!losers.length" class="srank__none">暂无下跌板块</p>
      <button
        v-for="(s, i) in losers"
        :key="s.code || s.name"
        type="button"
        class="srank__row"
        :disabled="!s.leader"
        :title="s.leaderReason || undefined"
        @click="emit('select', s)"
      >
        <span class="srank__rank">{{ i + 1 }}</span>
        <span class="srank__name">{{ s.name }}</span>
        <span class="srank__meta">
          <template v-if="s.memberCount != null">{{ s.memberCount }} 只</template>
          <template v-if="s.leader">
            · 龙头 <SymbolName :code="s.leader" mode="compact" :clickable="false" />
          </template>
        </span>
        <span class="srank__pct" :class="pnlClass(s.avgPct)">{{ ppText(s.avgPct) }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.srank {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.srank__head {
  margin: 0 0 6px;
  font-size: var(--nt-fs-caption);
  font-weight: 600;
}

.srank__head.up {
  color: var(--nt-up);
}

.srank__head.down {
  color: var(--nt-down);
}

.srank__none {
  margin: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

.srank__row {
  display: flex;
  align-items: center;
  gap: 6px;
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

.srank__row:hover {
  background: var(--nt-bg-2);
}

/* 无龙头的板块行：只读展示（点击无处可去），无悬停反馈、不占 Tab 序。 */
.srank__row:disabled {
  cursor: default;
}

.srank__row:disabled:hover {
  background: transparent;
}

.srank__rank {
  flex: none;
  width: 16px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--nt-text-3);
}

.srank__name {
  flex: none;
  max-width: 7em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--nt-text-1);
}

.srank__meta {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}

.srank__pct {
  flex: none;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.srank__pct.up {
  color: var(--nt-up);
}

.srank__pct.down {
  color: var(--nt-down);
}

@container (max-width: 400px) {
  .srank {
    grid-template-columns: 1fr;
  }
}
</style>
