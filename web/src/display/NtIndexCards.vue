<script setup lang="ts">
/**
 * 三大指数卡墙（上证/深成/创指，BE-10 快照口径）：名称 + 点位 + 涨跌点/涨跌幅 + 振幅。
 * 纪律：
 * - found=false 显 NtMissing（数据纪律「没取到≠0」，不拿 0 点位冒充）；
 * - stale 快照标「昨收/旧」（source=1d 为收盘回退档，与 NtIndexStrip 同口径）；
 * - 红涨绿跌走 pnlClass；数字 mono + tabular-nums；禁 hero（--nt-text-0 零使用）。
 * pctChg/amplitude 是百分点口径（1.23 = 1.23%），chg 是绝对点位。
 */
import type { IndexQuoteView } from '@/api/indexSnapshot'
import NtEmpty from '@/ui/NtEmpty.vue'
import NtMissing from '@/ui/NtMissing.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import { pnlClass } from '@/utils/format'

withDefaults(
  defineProps<{
    indices: IndexQuoteView[]
    /** 首屏读取中（无旧值时的骨架）；有旧值刷新不清屏。 */
    loading?: boolean
  }>(),
  { loading: false },
)

/** 有符号百分文本：正数补 +，负数自带 −；非有限值显「—」。 */
function pctText(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${v > 0 ? '+' : ''}${v.toFixed(2)}%`
}

function pointsText(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${v > 0 ? '+' : ''}${v.toFixed(2)}`
}

function lastText(ix: IndexQuoteView): string {
  return Number.isFinite(ix.last) ? ix.last.toFixed(2) : '—'
}
</script>

<template>
  <div class="ix-wall">
    <template v-if="loading && !indices.length">
      <NtSkeleton v-for="i in 3" :key="i" kind="card" class="ix-card" />
    </template>
    <NtEmpty
      v-else-if="!indices.length"
      state="missing"
      title="指数快照未返回"
      hint="行情源恢复后自动刷新"
    />
    <article v-for="ix in indices" v-else :key="ix.code" class="ix-card" :class="{ 'ix-card--off': !ix.found }">
      <p class="ix-card__name">{{ ix.name || ix.code }}</p>
      <template v-if="ix.found">
        <p class="ix-card__last">{{ lastText(ix) }}</p>
        <p class="ix-card__chg" :class="pnlClass(ix.pctChg)">
          <span class="ix-card__pct">{{ pctText(ix.pctChg) }}</span>
          <span class="ix-card__pts">{{ pointsText(ix.chg) }}</span>
        </p>
        <p class="ix-card__sub">
          振幅 {{ pctText(ix.amplitude) }}
          <span v-if="ix.stale" class="ix-card__tag">{{ ix.source === '1d' ? '昨收' : '旧' }}</span>
        </p>
      </template>
      <p v-else class="ix-card__miss"><NtMissing label="没取到" reason="指数快照" /></p>
    </article>
  </div>
</template>

<style scoped>
.ix-wall {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 8px;
}

.ix-card {
  padding: 10px 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.ix-card__name {
  margin: 0;
  font-size: 11px;
  color: var(--nt-text-3);
}

.ix-card__last {
  margin: 4px 0 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-num-lg);
  font-weight: 700;
  color: var(--nt-text-1);
}

.ix-card__chg {
  display: flex;
  gap: 8px;
  margin: 2px 0 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-body);
}

.ix-card__chg.up {
  color: var(--nt-up);
}

.ix-card__chg.down {
  color: var(--nt-down);
}

.ix-card__sub {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 3px 0 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--nt-text-3);
}

.ix-card__tag {
  padding: 0 4px;
  border: 1px solid var(--nt-border-2);
  border-radius: 3px;
  font-size: 10.5px;
  color: var(--nt-warn);
}

.ix-card__miss {
  margin: 8px 0 4px;
}

/* 未取到档：名称行之外全部弱化，不与正常读数抢层级。 */
.ix-card--off {
  opacity: 0.75;
}
</style>
