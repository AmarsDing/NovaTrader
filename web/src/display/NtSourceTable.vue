<script setup lang="ts">
/**
 * NtSourceTable（W0-3b / FE-D1）：数据源行单一事实组件，四处复用——
 *   health 变体：datasource-health（治理面板，行点击开抽屉 + 启停/采集操作槽）、
 *                datasource-center（只读页签）；
 *   ops 变体：   OpsView 数据源块、ConfigCenter 链路页清单。
 * 状态灯/状态词（source-rows 收口）/数字缺位（没取到≠0）全部唯一出处，四处不再各裁口径。
 */
import { computed } from 'vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, ratioPct } from '@/utils/format'
import type { OpsSourceRow, SourceHealthRow } from './source-rows'

type Row = Record<string, unknown>

const props = withDefaults(
  defineProps<{
    /** health=数据源读面列组（治理/中心两面板同源）；ops=网关 name/status/detail 列组（Ops/配置中心同源）。 */
    variant: 'health' | 'ops'
    health?: SourceHealthRow[]
    ops?: OpsSourceRow[]
    clickable?: boolean
    empty?: string
  }>(),
  { health: () => [], ops: () => [], clickable: false, empty: '还没有数据源' },
)

const emit = defineEmits<{ (e: 'row-click', row: Row): void }>()

/* 槽内行收窄（模板零 as：cast 收在 script，与全仓既有风格一致）。 */
const slots = defineSlots<{
  /** 自定义启停格（治理面板放开关；缺省渲 开/关 文本）。 */
  enabled?(props: { row: SourceHealthRow }): unknown
  /** 追加操作列格（治理面板放采集按钮）。 */
  ops?(props: { row: SourceHealthRow }): unknown
}>()

function h(row: unknown): SourceHealthRow {
  return row as SourceHealthRow
}
function o(row: unknown): OpsSourceRow {
  return row as OpsSourceRow
}

/** 成功率告警线（模板零 as 化）：null=无采样不参与告警；<0.9 红字（P3-10c 守卫区语义）。 */
function rateWarn(r: SourceHealthRow): boolean {
  return r.successRate != null && r.successRate < 0.9
}
function latencyText(r: SourceHealthRow): string {
  return r.latencyMs != null ? String(Math.round(r.latencyMs)) : '—'
}

/* health 变体列组（W4b 说明长文出列的既定列序唯一出处；操作列随槽提供与否追加）。 */
const healthColumns = computed<NtTableColumn<Row>[]>(() => {
  const cols: NtTableColumn<Row>[] = [
    { key: 'name', title: '数据源' },
    { key: 'domain', title: '域', mono: true },
    { key: 'successRate', title: '成功率', align: 'right' },
    { key: 'latencyMs', title: '最近延迟(ms)', align: 'right' },
    { key: 'fails', title: '成功/失败', align: 'right' },
    { key: 'lastAt', title: '最近成功', mono: true },
    { key: 'enabled', title: '启停' },
  ]
  if (slots.ops) cols.push({ key: 'ops', title: '' })
  return cols
})

const opsColumns: NtTableColumn<Row>[] = [
  { key: 'name', title: '名称' },
  { key: 'status', title: '状态' },
  { key: 'detail', title: '说明' },
]

const rows = computed(() => (props.variant === 'health' ? (props.health as unknown as Row[]) : (props.ops as unknown as Row[])))
const columns = computed(() => (props.variant === 'health' ? healthColumns.value : opsColumns))

function onRow(row: Row) {
  emit('row-click', row)
}
</script>

<template>
  <NtTable :columns="columns" :rows="rows" row-key="key" dense :clickable="clickable" :empty="empty" @row-click="onRow">
    <!-- 同名槽不可重复下发（vue/valid-v-slot）：name 格按 variant 内部分支；其余格按列键各归各变体，缺列即不渲。 -->
    <template #cell-name="{ row }">
      <template v-if="variant === 'health'">
        <NtStatusDot :status="h(row).status" :label="h(row).dotLabel" />
      </template>
      <span v-else class="cell-name">
        <NtStatusDot :status="o(row).status" :label="o(row).statusText" :title="o(row).rawStatus || '状态未返回'" />{{
          o(row).name
        }}
      </span>
    </template>
    <template #cell-successRate="{ row }">
      <!-- 「没取到≠0」（P3-10c 守卫区语义随组件收口）：null=统计窗口无采样，显「—」不参与告警（不虚警），title 写明原因 -->
      <span v-if="h(row).successRate != null" :class="{ warn: rateWarn(h(row)) }">{{ ratioPct(h(row).successRate, 0) }}</span>
      <span v-else class="mid" title="统计窗口内无采样，成功率无法计算">—</span>
    </template>
    <template #cell-latencyMs="{ row }">
      {{ latencyText(h(row)) }}
    </template>
    <template #cell-fails="{ row }">
      <span :class="{ warn: (h(row).consecutiveFailures ?? 0) > 0 }">
        {{ h(row).successCount ?? '—' }} / {{ h(row).failureCount ?? '—' }}
      </span>
    </template>
    <template #cell-lastAt="{ row }">
      {{ clock(h(row).lastAt) || '—' }}
    </template>
    <template #cell-enabled="{ row }">
      <slot name="enabled" :row="h(row)">{{ h(row).enabled ? '开' : '关' }}</slot>
    </template>
    <template #cell-ops="{ row }">
      <slot name="ops" :row="h(row)" />
    </template>
    <template #cell-status="{ row }">
      <!-- 状态列保留网关原始字面值（数据原样直读）；判定词已随点名册化。 -->
      {{ o(row).rawStatus || '—' }}
    </template>
    <template #cell-detail="{ row }">
      {{ o(row).detail || '—' }}
    </template>
  </NtTable>
</template>

<style scoped>
/* G4 棘轮候选（§7.1）：.warn 文字级告警色待 G4 收编为册导出（P3-10c 守卫区语义随组件迁移，一处收口）。 */
.warn {
  color: var(--nt-warn);
  font-weight: 600;
}

.mid {
  color: var(--nt-text-2);
}

.cell-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
</style>
