<script setup lang="ts">
// NtPreflightChecklist：启用模态的清单式前置检查（W3c；R-UI-30/36）。
// 只展示与发导航意图：deeplink 原样 emit 给宿主，本件不碰 router/store（组件三层纪律）。
// blockinger 徽 「必需/提示」是提交锁的语义标；「没取到≠0」：unknown 显未证实暗点不算就位。
import { computed } from 'vue'
import { GATE_STATE_LABEL } from '@/utils/gate-labels'
import { blockingGaps, type PreflightItem } from '@/utils/preflight'

/* 合同型与判定式唯一出处在 utils/preflight.ts；此处 re-export 仅为既有导入位兼容，勿回抄定义。 */
export type { PreflightItem } from '@/utils/preflight'

const props = withDefaults(defineProps<{ items: PreflightItem[]; title?: string }>(), { title: '' })

const emit = defineEmits<{ (e: 'navigate', deeplink: string): void }>()

/** 必堵项未就位数——直接汇入 utils/preflight.blockingGaps（与宿主锁提交同一实现，双口径漂移归零）。 */
const gaps = computed(() => blockingGaps(props.items))

function stateText(it: PreflightItem): string {
  return GATE_STATE_LABEL[it.state] || it.state
}

/** 进度百分比；total 缺失或非正时不画条（缺数不伪造 0%）。 */
function pctOf(it: PreflightItem): number | null {
  const p = it.progress
  if (!p || !(p.total > 0)) return null
  return Math.max(0, Math.min(100, Math.round((p.done / p.total) * 100)))
}
</script>

<template>
  <section class="nt-preflight">
    <p class="nt-preflight__sum" :class="gaps.length ? 'is-bad' : 'is-ok'">
      {{ props.title ? props.title + '：' : '' }}{{ gaps.length ? `还有 ${gaps.length} 项未达，先按下面提示逐项处理` : '前置检查全部就位' }}
    </p>
    <p v-if="!props.items.length" class="nt-preflight__empty">本动作无需前置检查项。</p>
    <ul v-else class="nt-preflight__list">
      <li v-for="it in props.items" :key="it.key" class="nt-preflight__row" :class="it.state === 'ok' ? 'is-ok' : it.blocking !== false ? 'is-bad' : 'is-warn'" :data-state="it.state">
        <div class="nt-preflight__line">
          <span class="nt-preflight__dot" :class="`is-${it.state}`" />
          <span class="nt-preflight__label">{{ it.label }}</span>
          <span class="nt-preflight__kind" :class="it.blocking !== false ? 'is-blocking' : ''">{{ it.blocking !== false ? '必需' : '提示' }}</span>
          <span class="nt-preflight__state">{{ stateText(it) }}</span>
          <button v-if="it.deeplink" type="button" class="nt-preflight__go" @click="emit('navigate', it.deeplink!)">去处理</button>
        </div>
        <p v-if="it.why" class="nt-preflight__why">{{ it.why }}</p>
        <div v-if="pctOf(it) !== null" class="nt-preflight__progress">
          <div class="nt-preflight__bar"><i :style="{ width: pctOf(it) + '%' }" /></div>
          <span class="nt-preflight__count">{{ it.progress!.done }}/{{ it.progress!.total }}</span>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.nt-preflight {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.nt-preflight__sum {
  margin: 0;
  font-size: var(--nt-fs-caption);
  font-weight: 600;
}

.nt-preflight__sum.is-ok {
  color: var(--nt-accent);
}

.nt-preflight__sum.is-bad {
  color: var(--nt-warn);
}

.nt-preflight__empty {
  margin: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

.nt-preflight__list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
  max-height: 320px;
  overflow-y: auto;
}

.nt-preflight__row {
  padding: 7px 9px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-left-width: 2px;
  border-radius: var(--nt-radius-s);
}

/* 行边色按「是否拦提交」分族：必堵未达=风险色，提示/就位退警示与中性。 */
.nt-preflight__row.is-bad {
  border-left-color: var(--nt-danger);
}

.nt-preflight__row.is-warn {
  border-left-color: var(--nt-warn);
}

.nt-preflight__row.is-ok {
  border-left-color: var(--nt-border-1);
}

.nt-preflight__line {
  display: flex;
  align-items: center;
  gap: 7px;
}

.nt-preflight__dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--nt-text-3);
}

.nt-preflight__dot.is-ok {
  background: var(--nt-accent);
}

.nt-preflight__dot.is-warn {
  background: var(--nt-warn);
}

.nt-preflight__dot.is-down {
  background: var(--nt-danger);
}

.nt-preflight__dot.is-unknown {
  background: var(--nt-text-3);
}

.nt-preflight__label {
  font-size: var(--nt-fs-body);
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-preflight__kind {
  font-size: var(--nt-fs-micro);
  padding: 0 5px;
  border: 1px solid var(--nt-border-2, var(--nt-border-1));
  border-radius: 3px;
  color: var(--nt-text-3);
}

.nt-preflight__kind.is-blocking {
  color: var(--nt-text-2);
}

.nt-preflight__state {
  margin-left: auto;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-2);
}

.nt-preflight__go {
  padding: 2px 8px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-accent);
  background: transparent;
  border: 1px solid var(--nt-accent);
  border-radius: 3px;
  cursor: pointer;
}

.nt-preflight__go:hover {
  background: color-mix(in srgb, var(--nt-accent) 12%, transparent);
}

.nt-preflight__why {
  margin: 5px 0 0 14px;
  font-size: var(--nt-fs-caption);
  line-height: 1.5;
  color: var(--nt-text-2);
}

.nt-preflight__progress {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 6px 0 0 14px;
}

.nt-preflight__bar {
  flex: 1;
  height: 4px;
  background: var(--nt-border-1);
  border-radius: 2px;
  overflow: hidden;
}

.nt-preflight__bar > i {
  display: block;
  height: 100%;
  background: var(--nt-warn);
}

/* 就位行的进度回忆告警琥珀：达标事实配 accent（色彩是词汇，警报位让出）。 */
.nt-preflight__row.is-ok .nt-preflight__bar > i {
  background: var(--nt-accent);
}

.nt-preflight__count {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-2);
}
</style>
