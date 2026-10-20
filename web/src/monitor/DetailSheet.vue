<script setup lang="ts">
/**
 * S5 详情窗深化渲染：把 detail.ts 的六张牌模型画出来。
 * 只读——全组件没有任何写操作按钮；§5.2 的「出口」是一次路由跳转。
 * 发光预算不在这里花：缺口用 --nt-warn 文字，环形色阶由 NtProgressRing 自己带。
 */
import NtProgressRing from '@/ui/NtProgressRing.vue'
import NtRadar from '@/viz/NtRadar.vue'
import Sparkline from './Sparkline.vue'
import type { DetailPanel, DetailRow } from './detail'

defineProps<{
  panel: DetailPanel
}>()

const emit = defineEmits<{
  exit: [to: string]
}>()

function rowTone(row: DetailRow): string {
  return row.tone && row.tone !== 'flat' ? row.tone : ''
}
</script>

<template>
  <div class="nt-deep">
    <section v-for="section in panel.sections" :key="section.key" class="nt-deep__sec">
      <h3>{{ section.title }}</h3>

      <div v-if="section.rows" class="nt-deep__rows">
        <div v-for="row in section.rows" :key="row.key" class="nt-deep__row">
          <span class="nt-deep__label">{{ row.label }}</span>
          <span class="nt-deep__value" :class="rowTone(row)">
            <NtProgressRing v-if="row.ring" :value="row.ring.pct" :tone="row.ring.tone" :size="40" />
            <template v-else>{{ row.value }}</template>
          </span>
          <small v-if="row.note" class="nt-deep__note">{{ row.note }}</small>
          <small v-if="row.ring" class="nt-deep__note">{{ row.value }}</small>
        </div>
      </div>

      <div v-if="section.radar" class="nt-deep__radar">
        <NtRadar :dims="section.radar" :size="120" />
        <ul>
          <li v-for="dim in section.radar" :key="dim.label">
            {{ dim.label }} <strong>{{ dim.value == null ? '未评' : dim.value }}</strong>
          </li>
        </ul>
      </div>

      <div v-if="section.bars" class="nt-deep__bars">
        <div v-for="bar in section.bars" :key="bar.label" class="nt-deep__bar" :data-warn="bar.tone === 'warn' || undefined">
          <span>{{ bar.label }}</span>
          <i :style="{ width: `${Math.min(100, (bar.count / Math.max(1, ...section.bars.map((b) => b.count))) * 100)}%` }"></i>
          <strong>{{ bar.count }}</strong>
        </div>
      </div>

      <div v-if="section.table" class="nt-deep__tablewrap">
        <table class="nt-deep__table" :data-key="section.table.key">
          <thead>
            <tr>
              <th v-for="column in section.table.columns" :key="column">{{ column }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="section.table.rows.length === 0">
              <td :colspan="section.table.columns.length" class="nt-deep__empty">{{ section.table.empty }}</td>
            </tr>
            <tr v-for="(row, r) in section.table.rows" :key="r" :data-flag="section.table.flags?.[r] || undefined">
              <td v-for="(cell, c) in row" :key="c">
                <!-- §10-27：名称列名字套壳——定宽 + 超长 ellipsis，hover title 显全名，不挤爆布局 -->
                <span v-if="section.table.columns[c] === '名称'" class="nt-deep__namecell" :title="cell">{{ cell }}</span>
                <template v-else>{{ cell }}</template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="section.spark" class="nt-deep__spark">
        <Sparkline :points="section.spark.points" tone="flat" />
        <small>{{ section.spark.caption }}</small>
      </div>

      <p v-if="section.foot" class="nt-deep__foot">{{ section.foot }}</p>
    </section>

    <button v-if="panel.exit" type="button" class="nt-deep__exit" @click="emit('exit', panel.exit.to)">
      {{ panel.exit.label }}
    </button>
  </div>
</template>

<style scoped>
.nt-deep {
  display: grid;
  gap: 14px;
  margin-top: 4px;
}

.nt-deep__sec h3 {
  margin: 0 0 6px;
  font-size: 12px;
  letter-spacing: 0.1em;
  color: var(--nt-text-3);
  font-weight: 600;
}

.nt-deep__rows {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.nt-deep__row {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 7px 10px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  background: var(--nt-bg-2);
  min-width: 0;
}

.nt-deep__label {
  font-size: 11px;
  color: var(--nt-text-3);
}

.nt-deep__value {
  font-family: var(--nt-font-mono);
  font-size: 14px;
  font-weight: 600;
  word-break: break-all;
}

.nt-deep__value.up {
  color: var(--nt-up);
}

.nt-deep__value.down {
  color: var(--nt-down);
}

.nt-deep__value.warn {
  color: var(--nt-warn);
}

.nt-deep__value.danger {
  color: var(--nt-danger);
}

.nt-deep__note {
  font-size: 11px;
  color: var(--nt-text-2);
  line-height: 1.45;
}

.nt-deep__radar {
  display: flex;
  align-items: center;
  gap: 16px;
}

.nt-deep__radar ul {
  margin: 0;
  padding: 0;
  list-style: none;
  display: grid;
  gap: 4px;
  font-size: 12px;
  color: var(--nt-text-2);
}

.nt-deep__radar strong {
  font-family: var(--nt-font-mono);
  color: var(--nt-text-1);
  margin-left: 6px;
}

.nt-deep__bars {
  display: grid;
  gap: 4px;
}

.nt-deep__bar {
  display: grid;
  grid-template-columns: minmax(90px, 160px) 1fr 40px;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.nt-deep__bar span {
  color: var(--nt-text-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nt-deep__bar i {
  display: block;
  height: 6px;
  border-radius: 3px;
  background: var(--nt-accent-border);
}

.nt-deep__bar[data-warn] i {
  background: var(--nt-warn);
}

.nt-deep__bar strong {
  font-family: var(--nt-font-mono);
  text-align: right;
}

.nt-deep__tablewrap {
  overflow-x: auto;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.nt-deep__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
  white-space: nowrap;
}

.nt-deep__table th {
  position: sticky;
  top: 0;
  background: var(--nt-bg-2);
  color: var(--nt-text-3);
  font-weight: 500;
  text-align: left;
  padding: 6px 8px;
}

.nt-deep__table td {
  padding: 5px 8px;
  border-top: 1px solid var(--nt-border-1);
  font-family: var(--nt-font-mono);
  color: var(--nt-text-1);
}

.nt-deep__table td:first-child {
  font-family: inherit;
}

/* §10-27 名称列定宽：名字套壳裁切（max-width 只在内联块上可靠），超长 ellipsis，
 * 五表列宽各不相同按 data-key 定额（持仓最宽表最省、成交最宽、质量挤检查项最窄），
 * 默认 120px 兜底（sig/miss/prem 的次列名称同纪律） */
.nt-deep__namecell {
  display: inline-block;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

.nt-deep__table[data-key='pos'] .nt-deep__namecell {
  max-width: 92px;
}

.nt-deep__table[data-key='ord'] .nt-deep__namecell {
  max-width: 96px;
}

.nt-deep__table[data-key='fill'] .nt-deep__namecell {
  max-width: 112px;
}

.nt-deep__table[data-key='qi'] .nt-deep__namecell {
  max-width: 88px;
}

.nt-deep__table[data-key='rush'] .nt-deep__namecell {
  max-width: 104px;
}

/* 宽表横滚时第一列（代码/名称）钉在左缘，后列从下面滑过 */
.nt-deep__table th:first-child,
.nt-deep__table td:first-child {
  position: sticky;
  left: 0;
  z-index: 1;
  background: var(--nt-bg-1);
  box-shadow: inset -1px 0 0 var(--nt-border-1);
}

.nt-deep__table th:first-child {
  z-index: 2;
  background: var(--nt-bg-2);
}

.nt-deep__table tr[data-flag='warn'] td:first-child {
  box-shadow: inset 3px 0 0 var(--nt-warn), inset -1px 0 0 var(--nt-border-1);
}

.nt-deep__table tr[data-flag='danger'] td:first-child {
  box-shadow: inset 3px 0 0 var(--nt-danger), inset -1px 0 0 var(--nt-border-1);
}

.nt-deep__empty {
  color: var(--nt-text-3);
  text-align: center;
  font-family: inherit;
}

.nt-deep__spark {
  display: grid;
  gap: 2px;
}

.nt-deep__spark small,
.nt-deep__foot {
  font-size: 11px;
  color: var(--nt-text-3);
  line-height: 1.5;
  margin: 6px 0 0;
}

.nt-deep__exit {
  justify-self: start;
  border: 1px solid var(--nt-border-2);
  border-radius: 999px;
  background: transparent;
  color: var(--nt-text-1);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  padding: 6px 16px;
}

.nt-deep__exit:hover {
  border-color: var(--nt-accent);
}
</style>
