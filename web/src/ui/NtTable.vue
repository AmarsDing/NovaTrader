<script setup lang="ts" generic="T extends Record<string, unknown>">
export interface NtTableColumn<T> {
  key: keyof T & string
  title: string
  align?: 'left' | 'right' | 'center'
  width?: string
  mono?: boolean
}

const props = withDefaults(
  defineProps<{
    columns: NtTableColumn<T>[]
    rows: T[]
    rowKey?: string
    empty?: string
    dense?: boolean
    clickable?: boolean
  }>(),
  { rowKey: '', empty: '暂无数据', dense: false, clickable: false },
)

const emit = defineEmits<{ (e: 'row-click', row: T): void }>()

function keyOf(row: T, index: number): string {
  const record = row as Record<string, unknown>
  if (props.rowKey) return String(record[props.rowKey] ?? index)
  return String(record.id ?? record.symbol ?? index)
}

function onRow(row: T) {
  emit('row-click', row)
}
</script>

<template>
  <div class="nt-table" :class="{ 'nt-table--dense': dense }">
    <table>
      <thead>
        <tr>
          <th
            v-for="col in columns"
            :key="col.key"
            :style="{ width: col.width, textAlign: col.align || 'left' }"
            :class="{ mono: col.mono || col.align === 'right' }"
          >
            {{ col.title }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!rows.length">
          <td :colspan="columns.length" class="nt-table__empty">{{ empty }}</td>
        </tr>
        <tr
          v-for="(row, index) in rows"
          :key="keyOf(row, index)"
          :class="{ 'nt-table__row--click': clickable }"
          @click="clickable && onRow(row)"
        >
          <td
            v-for="col in columns"
            :key="col.key"
            :style="{ textAlign: col.align || 'left' }"
            :class="{ mono: col.mono || col.align === 'right' }"
          >
            <slot :name="`cell-${col.key}`" :row="row" :value="row[col.key]">
              {{ row[col.key] == null || row[col.key] === '' ? '—' : row[col.key] }}
            </slot>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.nt-table {
  width: 100%;
  overflow: auto;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  background: var(--nt-bg-1);
}

.nt-table table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
  font-variant-numeric: tabular-nums;
}

.nt-table th {
  position: sticky;
  top: 0;
  z-index: 1;
  padding: var(--nt-cell-pad);
  height: var(--nt-row-h);
  font-size: 11.5px;
  font-weight: 500;
  color: var(--nt-text-3);
  background: var(--nt-bg-2);
  border-bottom: 1px solid var(--nt-border-2);
  white-space: nowrap;
}

.nt-table td {
  padding: var(--nt-cell-pad);
  height: var(--nt-row-h);
  color: var(--nt-text-1);
  border-bottom: 1px solid var(--nt-border-1);
  white-space: nowrap;
}

.nt-table tbody tr:last-child td {
  border-bottom: none;
}

.nt-table tbody tr:hover td {
  background: rgba(0, 229, 255, 0.04);
}

.nt-table__row--click {
  cursor: pointer;
}

.mono {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.nt-table__empty {
  padding: 18px !important;
  text-align: center !important;
  color: var(--nt-text-3) !important;
}
</style>
