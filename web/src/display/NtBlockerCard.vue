<script setup lang="ts">
// NtBlockerCard：闸门就绪引导卡（W3b；UI 宪法 R-UI-30/36）。
// 只展示与发导航意图：deeplink 原样 emit 给接线面板，本件不碰 router/store（组件三层纪律）。
// 「没取到≠0」：state=unknown 显「未证实」暗点，不显绿；progress 缺省不画条。
export interface BlockerItem {
  gate: string
  state: 'ok' | 'warn' | 'down' | 'unknown'
  severity?: 'danger' | 'warn'
  why?: string
  deeplink?: string
  progress?: { done: number; total: number }
}

const props = withDefaults(
  defineProps<{
    ready: boolean
    blockers: BlockerItem[]
    asOf?: string
    title?: string
  }>(),
  { asOf: '', title: '就绪检查' },
)

const emit = defineEmits<{ (e: 'navigate', deeplink: string): void }>()

/** 闸门代码 → 中文名；未登记代码原样显（宁可露码也不编名）。 */
const GATE_LABEL: Record<string, string> = {
  risk_engine: '风险引擎',
  risk_config: '风控配置',
  risk_killswitch: '急停开关',
  sim_admission: '模拟准入',
  broker_gw: '文件单通道',
  datahub_sources: '数据源',
  llm_config: '模型配置',
}

const STATE_LABEL: Record<string, string> = {
  warn: '待处理',
  down: '不可用',
  unknown: '未证实',
}

function labelOf(gate: string): string {
  return GATE_LABEL[gate] || gate
}

function stateLabel(b: BlockerItem): string {
  return STATE_LABEL[b.state] || b.state
}

/** 进度百分比；total 缺失或非正时不画条（缺数不伪造 0%）。 */
function pctOf(b: BlockerItem): number | null {
  const p = b.progress
  if (!p || !(p.total > 0)) return null
  return Math.max(0, Math.min(100, Math.round((p.done / p.total) * 100)))
}
</script>

<template>
  <section class="nt-blocker">
    <header class="nt-blocker__head">
      <span class="nt-blocker__dot" :class="props.ready ? 'is-ok' : 'is-bad'" />
      <h3 class="nt-blocker__title">{{ props.title }}</h3>
      <span v-if="props.ready" class="nt-blocker__state is-ok">全部就绪</span>
      <span v-else class="nt-blocker__state is-bad">{{ props.blockers.length }} 项待处理</span>
      <span v-if="props.asOf" class="nt-blocker__asof">{{ props.asOf }}</span>
    </header>

    <ul v-if="!props.ready && props.blockers.length" class="nt-blocker__list">
      <li
        v-for="b in props.blockers"
        :key="b.gate"
        class="nt-blocker__row"
        :class="b.severity === 'danger' ? 'is-danger' : 'is-warn'"
        :data-state="b.state"
      >
        <div class="nt-blocker__line">
          <span class="nt-blocker__dot" :class="`is-${b.state}`" />
          <span class="nt-blocker__gate">{{ labelOf(b.gate) }}</span>
          <span class="nt-blocker__tag">{{ stateLabel(b) }}</span>
          <button
            v-if="b.deeplink"
            type="button"
            class="nt-blocker__go"
            :data-deeplink="b.deeplink"
            @click="emit('navigate', b.deeplink!)"
          >
            去处理
          </button>
        </div>
        <p v-if="b.why" class="nt-blocker__why">{{ b.why }}</p>
        <div v-if="pctOf(b) !== null" class="nt-blocker__progress">
          <div class="nt-blocker__bar"><i :style="{ width: pctOf(b) + '%' }" /></div>
          <span class="nt-blocker__count">{{ b.progress!.done }}/{{ b.progress!.total }}</span>
        </div>
      </li>
    </ul>

    <p v-else-if="!props.ready" class="nt-blocker__empty">就绪检查未返回明细（上游无响应）</p>
    <slot v-else />
  </section>
</template>

<style scoped>
.nt-blocker {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.nt-blocker__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.nt-blocker__title {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-blocker__state {
  font-size: 11px;
  font-weight: 600;
}

.nt-blocker__asof {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-size: 10px;
  color: var(--nt-text-3);
}

.nt-blocker__list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.nt-blocker__row {
  padding: 7px 9px;
  background: var(--nt-bg-2, var(--nt-bg-1));
  border: 1px solid var(--nt-border-1);
  border-left-width: 2px;
  border-radius: var(--nt-radius-s, 4px);
}

.nt-blocker__row.is-danger {
  border-left-color: var(--nt-danger);
}

.nt-blocker__row.is-warn {
  border-left-color: var(--nt-warn);
}

.nt-blocker__line {
  display: flex;
  align-items: center;
  gap: 7px;
}

.nt-blocker__dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--nt-text-3);
}

.nt-blocker__dot.is-ok {
  background: var(--nt-accent);
}

.nt-blocker__dot.is-warn {
  background: var(--nt-warn);
}

.nt-blocker__dot.is-down {
  background: var(--nt-danger);
}

.nt-blocker__dot.is-unknown {
  background: var(--nt-text-3);
}

.nt-blocker__gate {
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-blocker__tag {
  font-size: 10px;
  padding: 0 5px;
  border: 1px solid var(--nt-border-2, var(--nt-border-1));
  border-radius: 3px;
  color: var(--nt-text-2);
}

.nt-blocker__go {
  margin-left: auto;
  padding: 2px 8px;
  font-size: 11px;
  color: var(--nt-accent);
  background: transparent;
  border: 1px solid var(--nt-accent);
  border-radius: 3px;
  cursor: pointer;
}

.nt-blocker__go:hover {
  background: color-mix(in srgb, var(--nt-accent) 12%, transparent);
}

.nt-blocker__why {
  margin: 5px 0 0 14px;
  font-size: 11px;
  line-height: 1.5;
  color: var(--nt-text-2);
}

.nt-blocker__progress {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 6px 0 0 14px;
}

.nt-blocker__bar {
  flex: 1;
  height: 4px;
  background: var(--nt-border-1);
  border-radius: 2px;
  overflow: hidden;
}

.nt-blocker__bar > i {
  display: block;
  height: 100%;
  background: var(--nt-warn);
}

.nt-blocker__count {
  font-family: var(--nt-font-mono);
  font-size: 10px;
  color: var(--nt-text-2);
}

.nt-blocker__empty {
  margin: 0;
  font-size: 11px;
  color: var(--nt-text-3);
}

.is-ok {
  color: var(--nt-accent);
}

.is-bad {
  color: var(--nt-danger);
}
</style>
