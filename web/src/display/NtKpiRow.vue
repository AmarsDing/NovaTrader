<script setup lang="ts">
/**
 * 市场 KPI 六格行（TSP KPI 行的 NT 重述）：
 * 涨/平/跌（三色拼接格）· 涨停/跌停 · 炸板率 · 最高连板 · 成交额 · 情绪阶段。
 * NtStatCard 单 value 单 tone 表达不了三色拼接，故专用件（样式合同与 NtStatCard 同族：
 * label 11px text-3 / value mono num-lg 700 / bg-1 卡 + border-1 + radius-m）。
 * 纪律：字段缺失一律「—」（intText/ratioPct/compactAmount 自带缺失口径），禁 hero。
 */
import type { Sentiment } from '@/api/market'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import { compactAmount, intText, phaseName, ratioPct } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    /** null = 情绪快照未取到（六格全部「—」，不冒充 0）。 */
    sentiment: Sentiment | null
    loading?: boolean
  }>(),
  { loading: false },
)

const scoreSub = () => {
  const s = props.sentiment
  if (!s) return ''
  if (s.stale) return '快照过期'
  return s.scoreCoef != null ? `评分系数 ×${s.scoreCoef.toFixed(2)}` : ''
}
</script>

<template>
  <div v-if="loading && !sentiment" class="kpi-row">
    <NtSkeleton v-for="i in 6" :key="i" kind="card" class="kpi-cell" />
  </div>
  <div v-else class="kpi-row" role="group" aria-label="市场关键指标">
    <article class="kpi-cell">
      <p class="kpi-cell__k">涨 / 平 / 跌</p>
      <p class="kpi-cell__v kpi-cell__v--tri">
        <span class="up">{{ intText(sentiment?.advance) }}</span>
        <i>/</i>
        <span class="flat">{{ intText(sentiment?.flat) }}</span>
        <i>/</i>
        <span class="down">{{ intText(sentiment?.decline) }}</span>
      </p>
      <p class="kpi-cell__s">家数</p>
    </article>
    <article class="kpi-cell">
      <p class="kpi-cell__k">涨停 / 跌停</p>
      <p class="kpi-cell__v kpi-cell__v--tri">
        <span class="up">{{ intText(sentiment?.upCount) }}</span>
        <i>/</i>
        <span class="down">{{ intText(sentiment?.downCount) }}</span>
      </p>
      <p class="kpi-cell__s">只</p>
    </article>
    <article class="kpi-cell">
      <p class="kpi-cell__k">炸板率</p>
      <p class="kpi-cell__v warn">{{ ratioPct(sentiment?.brokenRate) }}</p>
      <p class="kpi-cell__s">{{ sentiment?.brokenCount != null ? `${sentiment.brokenCount} 只炸板` : ' ' }}</p>
    </article>
    <article class="kpi-cell">
      <p class="kpi-cell__k">最高连板</p>
      <p class="kpi-cell__v accent">{{ sentiment?.maxHeight != null ? `${sentiment.maxHeight} 板` : '—' }}</p>
      <p class="kpi-cell__s">梯队高度</p>
    </article>
    <article class="kpi-cell">
      <p class="kpi-cell__k">成交额</p>
      <p class="kpi-cell__v">{{ compactAmount(sentiment?.amount) }}</p>
      <p class="kpi-cell__s">两市合计</p>
    </article>
    <article class="kpi-cell">
      <p class="kpi-cell__k">情绪阶段</p>
      <p class="kpi-cell__v accent">{{ phaseName(sentiment?.phase) }}</p>
      <p class="kpi-cell__s" :class="{ 'kpi-cell__s--warn': sentiment?.stale }">{{ scoreSub() || ' ' }}</p>
    </article>
  </div>
</template>

<style scoped>
.kpi-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 8px;
}

.kpi-cell {
  padding: 10px 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.kpi-cell__k {
  margin: 0;
  font-size: 11px;
  color: var(--nt-text-3);
}

.kpi-cell__v {
  margin: 4px 0 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-num-lg);
  font-weight: 700;
  color: var(--nt-text-1);
}

/* 三色拼接格：涨红 / 平灰 / 跌绿，分隔符弱化。 */
.kpi-cell__v--tri {
  display: flex;
  align-items: baseline;
  gap: 4px;
}

.kpi-cell__v--tri i {
  font-style: normal;
  color: var(--nt-text-3);
  font-weight: 400;
}

.kpi-cell__v--tri .up {
  color: var(--nt-up);
}

.kpi-cell__v--tri .flat {
  color: var(--nt-text-3);
}

.kpi-cell__v--tri .down {
  color: var(--nt-down);
}

.kpi-cell__v.warn {
  color: var(--nt-warn);
}

.kpi-cell__v.accent {
  color: var(--nt-accent);
}

.kpi-cell__s {
  margin: 3px 0 0;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--nt-text-2);
}

.kpi-cell__s--warn {
  color: var(--nt-warn);
}
</style>
