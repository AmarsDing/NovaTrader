<script lang="ts">
/** 梯队精简卡的视图模型（装配逻辑在 panels/market/useMarketBoard.ts 的 aggregateLadderMini）。 */
export interface LadderMiniTier {
  /** 连板高度（首板=1）。 */
  height: number
  /** 该高度的封板只数。 */
  count: number
}

export interface LadderMiniStep {
  /** 如「1进2」「3进4+」。 */
  label: string
  /** 晋级率（份额口径，0.45=45%）；sample=0 的档在装配期已剔除。 */
  rate: number
  sample: number
}

export interface LadderMiniModel {
  sealed: number
  broken: number
  /** 封板率（sealed / (sealed+broken)）；总数为 0 时 null（显「—」，不冒充 0%）。 */
  sealRate: number | null
  /** 按高度降序，至多 6 档。 */
  tiers: LadderMiniTier[]
  /** 近 5 日晋级率（仅 sample>0 的档）。 */
  steps: LadderMiniStep[]
}
</script>

<script setup lang="ts">
/**
 * 涨停梯队精简卡（TSP 梯队 widget 的 NT 重述）：封板率横条 + N 板进度条 + 晋级率提示。
 * 分组/过滤口径与 limit-ladder.panel.vue 一致（SEALED 过滤 + consecutive 分组，装配层照抄）。
 * NtLadderTiers 是全量件（无 compact 模式），本卡自绘精简形态；色档：≥5 板 up、≥3 板 accent、其余 text-2。
 */
import { computed } from 'vue'
import NtEmpty from '@/ui/NtEmpty.vue'
import NtSkeleton from '@/ui/NtSkeleton.vue'
import { intText, ratioPct } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    /** null = 涨停名单未返回（missing 态）。 */
    model: LadderMiniModel | null
    loading?: boolean
  }>(),
  { loading: false },
)

const total = computed(() => (props.model ? props.model.sealed + props.model.broken : 0))

const sealWidth = computed(() => {
  if (!props.model || props.model.sealRate == null) return '0%'
  return `${(props.model.sealRate * 100).toFixed(1)}%`
})

const maxCount = computed(() => Math.max(1, ...(props.model?.tiers.map((t) => t.count) ?? [1])))

function tierWidth(count: number): string {
  return `${Math.max(6, Math.round((count / maxCount.value) * 100))}%`
}

function tierTone(height: number): string {
  if (height >= 5) return 'up'
  if (height >= 3) return 'accent'
  return 'plain'
}
</script>

<template>
  <NtSkeleton v-if="loading && !model" :rows="3" />
  <NtEmpty
    v-else-if="!model"
    state="missing"
    title="涨停名单未返回"
    hint="行情源恢复后自动刷新；完整梯队见「涨停梯队」面板（DB）"
  />
  <div v-else class="lmini">
    <div class="lmini__seal">
      <div class="lmini__sealbar" role="img" :aria-label="`封板 ${model.sealed} 只、炸板 ${model.broken} 只`">
        <i :style="{ width: sealWidth }" />
      </div>
      <p class="lmini__sealtext">
        封板率 <b>{{ ratioPct(model.sealRate, 0) }}</b>
        <span class="lmini__sealnum">封 {{ intText(model.sealed) }} / 炸 {{ intText(model.broken) }}</span>
      </p>
    </div>
    <p v-if="!total" class="lmini__none">今日暂无涨停个股</p>
    <template v-else>
      <ul class="lmini__tiers">
        <li v-for="tier in model.tiers" :key="tier.height">
          <span class="lmini__h" :class="tierTone(tier.height)">{{ tier.height }} 板</span>
          <span class="lmini__track"><i :class="tierTone(tier.height)" :style="{ width: tierWidth(tier.count) }" /></span>
          <span class="lmini__n">{{ tier.count }}</span>
        </li>
      </ul>
      <p v-if="model.steps.length" class="lmini__steps">
        <template v-for="(step, i) in model.steps" :key="step.label">
          <span class="lmini__step">{{ step.label }} <b>{{ ratioPct(step.rate, 0) }}</b></span>
          <i v-if="i + 1 < model.steps.length">·</i>
        </template>
      </p>
    </template>
  </div>
</template>

<style scoped>
.lmini {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.lmini__sealbar {
  height: 8px;
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-2);
  overflow: hidden;
}

.lmini__sealbar i {
  display: block;
  height: 100%;
  background: var(--nt-up);
  transition: width var(--nt-transition);
}

.lmini__sealtext {
  margin: 6px 0 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.lmini__sealtext b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-up);
}

.lmini__sealnum {
  margin-left: 8px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-3);
}

.lmini__none {
  margin: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

.lmini__tiers {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.lmini__tiers li {
  display: flex;
  align-items: center;
  gap: 8px;
}

.lmini__h {
  flex: none;
  width: 34px;
  font-size: 11px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.lmini__h.up {
  color: var(--nt-up);
}

.lmini__h.accent {
  color: var(--nt-accent);
}

.lmini__h.plain {
  color: var(--nt-text-2);
}

.lmini__track {
  flex: 1;
  height: 8px;
  background: var(--nt-bg-2);
  border-radius: 2px;
  overflow: hidden;
}

.lmini__track i {
  display: block;
  height: 100%;
  border-radius: 2px;
  transition: width var(--nt-transition);
}

.lmini__track i.up {
  background: var(--nt-up);
}

.lmini__track i.accent {
  background: var(--nt-accent);
}

.lmini__track i.plain {
  background: var(--nt-text-2);
}

.lmini__n {
  flex: none;
  width: 24px;
  text-align: right;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-1);
}

.lmini__steps {
  margin: 0;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}

.lmini__steps i {
  font-style: normal;
  margin: 0 4px;
}

.lmini__step b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-2);
}
</style>
