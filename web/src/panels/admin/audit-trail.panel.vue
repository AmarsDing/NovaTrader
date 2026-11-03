<script setup lang="ts">
/**
 * 审计日志面板（M11）：读取 admin 网关 /api/v1/audit。
 * 支持按动作过滤 + 条数限制，时间倒序展示谁在什么时候动了什么。
 * FE-N4/W1：时间段/操作人/结果筛选为读面裁剪（后端 listAudit 只有 action/limit 两参数——
 * 服务端范围过滤属后端扩展；筛选只作用于已取回的最近 N 条，注记明示口径，不冒充全量检索）。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { listAudit, type AuditRow } from '@/api/audit'
import SymbolName from '@/display/SymbolName.vue'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { statusOf } from '@/ui/status-map'
import { clock, messageOf } from '@/utils/format'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'
import { serverNow } from '@/utils/server-clock'

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
  result: ['', 'ok', 'fail'],
  since: ['', '1h', '24h', '3d', '7d'],
} as const
const prefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)
const action = ref(prefs.action ?? '')
const limit = ref(Number(prefs.limit ?? 100))
/** FE-N4：结果/时间段筛选（读面裁剪，不触发重取——作用于已取回记录）。 */
const result = ref(prefs.result ?? '')
const since = ref(prefs.since ?? '')
/** 操作人筛选为自由文本（会话态不入 prefs，与 A3 代码搜索框同纪律）。 */
const actorFilter = ref('')
watch([action, limit, result, since], () =>
  savePanelPrefs(PREFS_KEY, PREFS_ALLOW, {
    action: action.value,
    limit: String(limit.value),
    result: result.value,
    since: since.value,
  }),
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
const resultOptions = [
  { value: '', label: '全部结果' },
  { value: 'ok', label: '仅成功' },
  { value: 'fail', label: '仅失败' },
]
/** 相对窗口（毫秒）：相对窗口避开时区歧义；判定基准 serverNow（P2-10）。 */
const SINCE_MS: Record<string, number> = {
  '1h': 3_600_000,
  '24h': 86_400_000,
  '3d': 259_200_000,
  '7d': 604_800_000,
}
const sinceOptions = [
  { value: '', label: '全部时间' },
  { value: '1h', label: '近 1 小时' },
  { value: '24h', label: '近 24 小时' },
  { value: '3d', label: '近 3 日' },
  { value: '7d', label: '近 7 日' },
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
/** FE-N4 读面裁剪：操作人（子串不区分大小写）/ 结果 / 时间段（相对窗口，serverNow 判定）——
 *  作用范围=已取回的最近 limit 条，裁剪不透传重取（后端 listAudit 只有 action/limit 参数）。 */
const filteredRows = computed(() => {
  const actor = actorFilter.value.trim().toLowerCase()
  const windowMs = SINCE_MS[since.value]
  const cutoff = windowMs ? serverNow() - windowMs : null
  return rows.value.filter((row) => {
    if (actor && !row.actor.toLowerCase().includes(actor)) return false
    if (result.value === 'ok' && !row.ok) return false
    if (result.value === 'fail' && row.ok) return false
    if (cutoff != null) {
      const at = Date.parse(row.createdAt)
      // 时点无法解析的记录在时间段筛选下不参与——藏住比显丑更危险（同 §6 过滤即裁剪纪律）。
      if (!Number.isFinite(at) || at < cutoff) return false
    }
    return true
  })
})
/** 任一读面筛选生效（注记与空态同源判断）。 */
const filtering = computed(() => !!(actorFilter.value.trim() || result.value || since.value))
const tableRows = computed(() => filteredRows.value as unknown as Row[])

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

/** FE-N4：detail 是 JSON 时渲染键值 diff（before/from → after/to），否则回退原文。 */
interface DiffPair { key: string; before: string; after: string; changed: boolean }
function shortText(v: unknown): string {
  if (v == null) return ''
  if (typeof v === 'string') return v
  try { return JSON.stringify(v) } catch { return String(v) }
}
const detailDiff = computed<DiffPair[] | null>(() => {
  const raw = selected.value?.detail
  if (!raw) return null
  let obj: unknown
  try { obj = JSON.parse(raw) } catch { return null }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return null
  const pairs: DiffPair[] = []
  for (const [key, v] of Object.entries(obj as Record<string, unknown>)) {
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      const e = v as Record<string, unknown>
      if ('before' in e || 'after' in e || 'from' in e || 'to' in e) {
        const before = shortText(e.before ?? e.from)
        const after = shortText(e.after ?? e.to)
        pairs.push({ key, before, after, changed: before !== after })
        continue
      }
    }
    pairs.push({ key, before: '', after: shortText(v), changed: false })
  }
  return pairs.length ? pairs : null
})

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
      <!-- FE-N4：操作人自由文本 + 结果 + 时间段为读面裁剪（不重取，作用于已取回记录） -->
      <NtInput v-model="actorFilter" placeholder="操作人" class="actor-input" />
      <NtSelect v-model="result" :options="resultOptions" />
      <NtSelect v-model="since" :options="sinceOptions" />
    </template>
    <div class="audit-body">
      <NtErrorGuide :error="error" />
      <p class="nt-note">
        <template v-if="filtering">筛后 {{ filteredRows.length }} / 已取 {{ rows.length }} 条记录 · 操作人/结果/时间段筛选只作用于已取回的最近 {{ limit }} 条 · </template>
        <template v-else>{{ rows.length }} 条记录 · </template>
        登录与敏感操作全量留痕 · 点行看详情
      </p>
      <NtTable
        :columns="columns"
        :rows="tableRows"
        row-key="id"
        dense
        clickable
        :empty="loading ? '读取中…' : error ? '审计记录未返回——点上方刷新重试' : action || filtering ? '当前筛选条件下没有审计记录——可放宽筛选或清空后再看' : '还没有审计记录——登录与敏感操作都会在这里留痕'"
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
        <table v-if="detailDiff" class="diff-table">
          <tbody>
            <tr v-for="p in detailDiff" :key="p.key" :class="{ changed: p.changed }">
              <td class="k">{{ p.key }}</td>
              <td class="v">{{ p.before || '—' }}</td>
              <td class="arrow">→</td>
              <td class="v">{{ p.after || '—' }}</td>
            </tr>
          </tbody>
        </table>
        <p v-else class="detail-full">{{ selected.detail || '—' }}</p>
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

/* FE-N4：操作人输入框限宽，与两个 Select 同行不占满筛选条。 */
.actor-input {
  width: 140px;
  flex: none;
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

/* FE-N4 键值 diff */
.diff-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--nt-fs-caption);
}

.diff-table td {
  padding: 3px 6px;
  border-bottom: 1px solid var(--nt-border-1);
  color: var(--nt-text-2);
  word-break: break-all;
  vertical-align: top;
}

.diff-table .k {
  color: var(--nt-text-3);
  white-space: nowrap;
}

.diff-table .arrow {
  color: var(--nt-text-3);
  text-align: center;
}

.diff-table tr.changed .v {
  color: var(--nt-accent);
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
