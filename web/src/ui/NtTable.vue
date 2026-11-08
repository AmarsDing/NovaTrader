<script setup lang="ts" generic="T extends Record<string, unknown>">
/**
 * NtTable v2（2026-11-08，借法 FreqUI @tanstack/vue-table / Perspective 虚拟网格 / vnpy BaseMonitor）：
 * 在 v1（列定义+空态+SymbolName 列+sticky 表头）上补专业表格两件套——
 *  1) 客户端列排序：列定义 `sortable`（可 `compare` 自定义），表头点击按
 *     升序 → 降序 → 还原 循环，方向指示 + aria-sort + 键盘可达；
 *     受控（sortKey/sortDir）与非受控（内部状态）两模式，`sort-change` 事件外发。
 *  2) 可选虚拟滚动：`virtual` + `height`（+ `rowHeight`）开启，只渲染可视窗口
 *     （useVirtualRows，overscan 6 行），上下 spacer 撑高；行高未传入时实测首行，
 *     兜底 dense 22 / 常规 26（对齐 --nt-row-h 令牌两档）。
 * 兼容性纪律：新 props 全可选；非 virtual 且无 sortable 的默认渲染路径 DOM 与 v1
 * 逐像素一致（'table' golden 不晃）；零裸色，全部走 --nt-* 令牌。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import SymbolName, { type SymbolNameMode } from '@/display/SymbolName.vue'
import { useVirtualRows } from './useVirtualRows'

export interface NtTableColumn<T> {
  key: keyof T & string
  title: string
  align?: 'left' | 'right' | 'center'
  width?: string
  mono?: boolean
  /** 证券标识列（方案 §6.1）：cell 值作证券代码交给 SymbolName 渲染；具名槽仍优先可覆盖。 */
  kind?: 'symbol'
  /** SymbolName 渲染模式，缺省 'inline'；仅 kind='symbol' 时生效。 */
  symbolMode?: SymbolNameMode
  /** 可排序：点击表头按 升序 → 降序 → 还原 循环。空值（null/''）恒排尾，不随方向翻转。 */
  sortable?: boolean
  /** 自定义比较函数；缺省按数值差 / 中文数字感知字典序自动推断。 */
  compare?: (a: T, b: T) => number
}

type SortDir = '' | 'asc' | 'desc'

const props = withDefaults(
  defineProps<{
    columns: NtTableColumn<T>[]
    rows: T[]
    rowKey?: string
    empty?: string
    dense?: boolean
    clickable?: boolean
    /** 开启虚拟滚动（大表）。开启后容器高由 height 约束，只渲染可视窗口。 */
    virtual?: boolean
    /** virtual 时的容器高（如 '420px' / '100%'）；缺省则容器自然撑高、虚拟不生效。 */
    height?: string
    /** 行高 px；缺省实测首行，再兜底 dense 22 / 常规 26。全局密度档切换会自动重测。 */
    rowHeight?: number
    /** 受控排序键；传入后排序状态由父级持有（配合 sortDir / sort-change）。 */
    sortKey?: string
    /** 受控排序方向。 */
    sortDir?: SortDir
  }>(),
  { rowKey: '', empty: '暂无数据', dense: false, clickable: false, virtual: false, height: '', rowHeight: 0, sortKey: undefined, sortDir: undefined },
)

const emit = defineEmits<{
  (e: 'row-click', row: T): void
  (e: 'sort-change', key: string, dir: SortDir): void
}>()

/* ---------- 排序（借法 FreqUI TradeList 列头排序） ---------- */

const innerKey = ref('')
const innerDir = ref<SortDir>('')
const effKey = computed(() => (props.sortKey !== undefined ? props.sortKey : innerKey.value))
const effDir = computed<SortDir>(() => (props.sortKey !== undefined ? (props.sortDir ?? '') : innerDir.value))

function cycleDir(d: SortDir): SortDir {
  return d === '' ? 'asc' : d === 'asc' ? 'desc' : ''
}

function onHeaderClick(col: NtTableColumn<T>) {
  if (!col.sortable) return
  const next: SortDir = effKey.value === col.key ? cycleDir(effDir.value) : 'asc'
  if (props.sortKey === undefined) {
    innerKey.value = next === '' ? '' : col.key
    innerDir.value = next
  }
  emit('sort-change', col.key, next)
}

function ariaSort(col: NtTableColumn<T>): 'ascending' | 'descending' | 'none' | undefined {
  if (!col.sortable) return undefined
  if (effKey.value !== col.key || !effDir.value) return 'none'
  return effDir.value === 'asc' ? 'ascending' : 'descending'
}

function sortIcon(col: NtTableColumn<T>): string {
  if (effKey.value !== col.key || !effDir.value) return '↕'
  return effDir.value === 'asc' ? '▲' : '▼'
}

const collator = new Intl.Collator('zh-Hans-CN', { numeric: true })
const isBlank = (v: unknown): boolean => v == null || v === ''
function defaultCompare(a: unknown, b: unknown): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b
  return collator.compare(String(a), String(b))
}

/** 排序不改原数组；空值恒排尾（数据纪律：未知不混入首尾两端）；同值保持原序（索引决胜）。 */
const sortedRows = computed<T[]>(() => {
  const dir = effDir.value
  if (!dir) return props.rows
  const col = props.columns.find((c) => c.key === effKey.value)
  if (!col) return props.rows
  const cmp = (a: T, b: T): number => {
    const av = a[col.key]
    const bv = b[col.key]
    const ab = isBlank(av)
    const bb = isBlank(bv)
    if (ab && bb) return 0
    if (ab) return 1
    if (bb) return -1
    const base = col.compare ? col.compare(a, b) : defaultCompare(av, bv)
    return dir === 'desc' ? -base : base
  }
  return props.rows
    .map((row, i) => ({ row, i }))
    .sort((x, y) => cmp(x.row, y.row) || x.i - y.i)
    .map((p) => p.row)
})

/* ---------- 虚拟滚动（借法 Perspective：只渲染可视窗口） ---------- */

const tableRef = ref<HTMLElement | null>(null)
const measuredRowH = ref(0)
const effRowHeight = computed(() => props.rowHeight || measuredRowH.value || (props.dense ? 22 : 26))
const totalRows = computed(() => sortedRows.value.length)
const virtualOn = computed(() => props.virtual && props.height !== '')
const { window: win, onScroll } = useVirtualRows({
  enabled: virtualOn,
  total: totalRows,
  rowHeight: effRowHeight,
  viewportRef: tableRef,
})

function measureRow() {
  const tr = tableRef.value?.querySelector('tbody tr:not(.nt-table__spacer)')
  const h = tr?.getBoundingClientRect().height ?? 0
  if (h > 0) measuredRowH.value = Math.round(h)
}

let densityObserver: MutationObserver | null = null
onMounted(() => {
  if (!props.virtual) return
  void nextTick(measureRow)
  // 全局密度档（data-density）切换会改变 --nt-row-h：重测保持窗口计算准确。
  densityObserver = new MutationObserver(() => void nextTick(measureRow))
  densityObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-density'] })
})
// 行高只在挂载与密度切换时实测；表格常先空载再填数（loading→rows），空载时
// 测不到行会一直用兜底值。补上「0→N」首次填数时的重测，让 spacer/窗口按真实行高算。
watch(totalRows, (n, prev) => {
  if (props.virtual && measuredRowH.value === 0 && prev === 0 && n > 0) {
    void nextTick(measureRow)
  }
})
onBeforeUnmount(() => {
  densityObserver?.disconnect()
  densityObserver = null
})

const visibleRows = computed<T[]>(() => {
  if (!virtualOn.value) return sortedRows.value
  return sortedRows.value.slice(win.value.start, win.value.end)
})

/* ---------- 行渲染 ---------- */

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
  <div
    ref="tableRef"
    class="nt-table"
    :class="{ 'nt-table--dense': dense, 'nt-table--virtual': virtualOn }"
    :style="virtualOn ? { height } : undefined"
    @scroll.passive="virtualOn && onScroll($event)"
  >
    <table>
      <thead>
        <tr>
          <th
            v-for="col in columns"
            :key="col.key"
            :style="{ width: col.width, textAlign: col.align || 'left' }"
            :class="{ mono: col.mono || col.align === 'right', 'nt-table__th--sort': col.sortable }"
            :aria-sort="ariaSort(col)"
            :tabindex="col.sortable ? 0 : undefined"
            @click="onHeaderClick(col)"
            @keydown.enter.prevent="onHeaderClick(col)"
            @keydown.space.prevent="onHeaderClick(col)"
          >
            {{ col.title }}
            <span
              v-if="col.sortable"
              class="nt-table__sort"
              :class="{ 'nt-table__sort--on': effKey === col.key && effDir !== '' }"
              aria-hidden="true"
            >{{ sortIcon(col) }}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!visibleRows.length">
          <!-- 空态富化预留：named slot 供消费方传「图标+一句话+动作」（B3 NtEmpty 落位即插）；
               默认渲染与原「一句灰字」逐像素一致（'table' golden 不晃）。 -->
          <td :colspan="columns.length" class="nt-table__empty"><slot name="empty">{{ empty }}</slot></td>
        </tr>
        <tr v-if="virtualOn && win.padTop > 0" class="nt-table__spacer" aria-hidden="true">
          <td :colspan="columns.length" :style="{ height: `${win.padTop}px` }"></td>
        </tr>
        <tr
          v-for="(row, index) in visibleRows"
          :key="keyOf(row, virtualOn ? win.start + index : index)"
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
              <SymbolName
                v-if="col.kind === 'symbol'"
                :code="row[col.key] == null ? '' : String(row[col.key])"
                :mode="col.symbolMode"
              />
              <template v-else>{{ row[col.key] == null || row[col.key] === '' ? '—' : row[col.key] }}</template>
            </slot>
          </td>
        </tr>
        <tr v-if="virtualOn && win.padBottom > 0" class="nt-table__spacer" aria-hidden="true">
          <td :colspan="columns.length" :style="{ height: `${win.padBottom}px` }"></td>
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

.nt-table--virtual {
  display: block;
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

.nt-table th.nt-table__th--sort {
  cursor: pointer;
  user-select: none;
}

.nt-table th.nt-table__th--sort:focus-visible {
  outline: 2px solid var(--nt-accent);
  outline-offset: -2px;
  box-shadow: 0 0 0 4px var(--nt-focus-ring);
}

.nt-table__sort {
  margin-left: 4px;
  font-size: 9px;
  color: var(--nt-text-3);
}

.nt-table__sort--on {
  color: var(--nt-accent);
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
  background: var(--nt-accent-soft);
}

.nt-table tbody tr.nt-table__spacer:hover td {
  background: transparent;
}

.nt-table__spacer td {
  padding: 0 !important;
  border-bottom: none !important;
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
