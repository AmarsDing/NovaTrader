<script setup lang="ts">
/**
 * 审计日志面板（M11）：读取 admin 网关 /api/v1/audit。
 * 支持按动作过滤 + 条数限制，时间倒序展示谁在什么时候动了什么。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { listAudit, type AuditRow } from '@/api/audit'
import SymbolName from '@/display/SymbolName.vue'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { statusOf } from '@/ui/status-map'
import { clock, messageOf } from '@/utils/format'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'

const rows = ref<AuditRow[]>([])
const loading = ref(false)
const error = ref('')

// W3-2 面板偏好持久化：键版勿改，旧档自然失效。
const PREFS_KEY = 'nt.prefs.audit-trail.v1'
const PREFS_ALLOW = {
  action: [
    '',
    'login',
    'risk.mode',
    'risk.killswitch',
    'risk.killswitch.reset',
    'risk.params',
    'trade.order',
    'trade.cancel',
    'trade.confirm',
    'strategy.approve',
    'strategy.reject',
    'strategy.rollback',
  ],
  limit: ['50', '100', '200', '500'],
} as const
const prefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)
const action = ref(prefs.action ?? '')
const limit = ref(Number(prefs.limit ?? 100))
watch([action, limit], () =>
  savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { action: action.value, limit: String(limit.value) }),
)

const ACTIONS = [
  'login',
  'risk.mode',
  'risk.killswitch',
  'risk.killswitch.reset',
  'risk.params',
  'trade.order',
  'trade.cancel',
  'trade.confirm',
  'strategy.approve',
  'strategy.reject',
  'strategy.rollback',
]

const actionOptions = computed(() => [
  { value: '', label: '全部动作' },
  ...ACTIONS.map((a) => ({ value: a, label: a })),
])
const limitOptions = [
  { value: 50, label: '50 条' },
  { value: 100, label: '100 条' },
  { value: 200, label: '200 条' },
  { value: 500, label: '500 条' },
]

type Row = AuditRow & Record<string, unknown>
// W4b：detail 长文出列表格进抽屉——主表 6 列；行点击开 320 抽屉放全文+行画像。
const columns: NtTableColumn<Row>[] = [
  { key: 'createdAt', title: '时间', mono: true },
  { key: 'actor', title: '操作人' },
  { key: 'role', title: '角色', mono: true },
  { key: 'action', title: '动作', mono: true },
  { key: 'target', title: '目标' },
  { key: 'ok', title: '结果' },
]
const tableRows = computed(() => rows.value as unknown as Row[])

/** §10-26：审计目标凡是「代码+市场后缀」形如 000001.SZ 的统一 SymbolName，其余目标（面板/参数键等）保持原文。 */
const SYMBOL_RE = /^\d{6}\.(SH|SZ|BJ)$/i
function isSymbol(value: unknown): value is string {
  return typeof value === 'string' && SYMBOL_RE.test(value)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    rows.value = await listAudit({
      ...(action.value ? { action: action.value } : {}),
      limit: limit.value,
    })
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

/** W4b：角色徽标入册（第十一批 role.*）；未识别角色显「未知」不冒绿不冒红（没取到≠0 的角色域投影）。 */
function roleEntry(role: unknown) {
  const r = String(role || '')
  return r === 'owner' || r === 'viewer' || r === 'operator'
    ? statusOf(`role.${r}` as const)
    : statusOf('role.unknown')
}

/** W4b：detail 长文收抽屉（320 档）——表格瘦身留 6 列，行点击开抽屉放全文+行画像。 */
const drawerOpen = ref(false)
const selected = ref<AuditRow | null>(null)
function openRow(row: Row) {
  selected.value = row as AuditRow
  drawerOpen.value = true
}

onMounted(load)
</script>

<template>
  <!-- W4b 三段式：筛选条（两 Select）→ 主体（瘦身 6 列 NtTable，行点击开抽屉）→ 320 抽屉（detail 全文+行画像）。 -->
  <NtAdminScaffold v-model:drawer-open="drawerOpen" title="审计日志" :drawer-width="320">
    <template #actions>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </template>
    <template #filter>
      <NtSelect v-model="action" :options="actionOptions" placeholder="动作" @update:model-value="load" />
      <NtSelect v-model="limit" :options="limitOptions" @update:model-value="load" />
    </template>
    <div class="audit-body">
      <NtErrorGuide :error="error" />
      <p class="nt-note">{{ rows.length }} 条记录 · 登录与敏感操作全量留痕 · 点行看详情</p>
      <NtTable
        :columns="columns"
        :rows="tableRows"
        row-key="id"
        dense
        clickable
        :empty="loading ? '读取中…' : error ? '审计记录未返回——点上方刷新重试' : action ? '当前筛选条件下没有审计记录——可放宽动作筛选或清空后再看' : '还没有审计记录——登录与敏感操作都会在这里留痕'"
        @row-click="openRow"
      >
        <template #cell-createdAt="{ row }">{{ clock(row.createdAt) }}</template>
        <template #cell-role="{ row }">
          <NtBadge :tone="roleEntry(row.role).tone">{{ roleEntry(row.role).text }}</NtBadge>
        </template>
        <template #cell-target="{ row }">
          <!-- §10-26：目标带代码象征字段统一 SymbolName inline；非证券目标（action/键名等）按原文，缺省「—」 -->
          <SymbolName v-if="isSymbol(row.target)" :code="row.target" mode="inline" />
          <template v-else>{{ row.target || '—' }}</template>
        </template>
        <template #cell-ok="{ row }">
          <!-- §7.1 册 audit.success/audit.failure：文案与 tone 唯一出处 -->
          <NtBadge :tone="statusOf(row.ok ? 'audit.success' : 'audit.failure').tone" dot>{{
            statusOf(row.ok ? 'audit.success' : 'audit.failure').text
          }}</NtBadge>
        </template>
      </NtTable>
    </div>
    <template #drawer>
      <div v-if="selected" class="audit-drawer">
        <h4>记录详情</h4>
        <dl>
          <div><dt>时间</dt><dd>{{ clock(selected.createdAt) }}</dd></div>
          <div><dt>操作人</dt><dd>{{ selected.actor || '—' }}</dd></div>
          <div><dt>角色</dt><dd>{{ roleEntry(selected.role).text }}</dd></div>
          <div><dt>动作</dt><dd>{{ selected.action }}</dd></div>
          <div><dt>目标</dt><dd>{{ selected.target || '—' }}</dd></div>
          <div><dt>结果</dt><dd>{{ statusOf(selected.ok ? 'audit.success' : 'audit.failure').text }}</dd></div>
        </dl>
        <h4>明细</h4>
        <p class="detail-full">{{ selected.detail || '—' }}</p>
        <p class="id-line">id：{{ selected.id }}</p>
      </div>
      <p v-else class="audit-drawer-empty">在左侧点一行看全文。</p>
    </template>
  </NtAdminScaffold>
</template>

<style scoped>
.audit-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 12px;
}

.audit-body .nt-note {
  margin: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

.audit-drawer {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
}

.audit-drawer h4 {
  margin: 4px 0 2px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.audit-drawer dl {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: var(--nt-fs-caption);
}

.audit-drawer dl > div {
  display: flex;
  gap: 8px;
}

.audit-drawer dt {
  flex: none;
  width: 44px;
  color: var(--nt-text-3);
}

.audit-drawer dd {
  margin: 0;
  color: var(--nt-text-1);
  word-break: break-all;
}

.detail-full {
  margin: 0;
  font-size: var(--nt-fs-caption);
  line-height: 1.6;
  color: var(--nt-text-1);
  white-space: pre-wrap;
  word-break: break-all;
}

.id-line {
  margin: 0;
  font-family: var(--nt-font-mono);
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}

.audit-drawer-empty {
  margin: 0;
  padding: 12px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}
</style>
