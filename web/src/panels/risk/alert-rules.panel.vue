<script setup lang="ts">
/**
 * G3 盯盘价格告警规则管理读面（risk 组首版）。
 * - 读/写全走 risk.proto 的 /risk/v1/alert/rules 系（经 admin 前缀聚合）：五口一律回全量整换；
 * - 状态机由 M08 AlertCenter 驱动（2s 节拍、触发转 triggered 只响一次、通知经 outbox 由 M10 出站），
 *   面板只直读直写、不在前端评估，状态行靠手动刷新 + 15s 轻轮询保持不陈旧；
 * - 编辑/重置布防是服务端白名单重建（清触发态、重立基线，防幽灵基线），表单本地仅做形状校验。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  createAlertRule,
  deleteAlertRule,
  editAlertRule,
  listAlertRules,
  rearmAlertRule,
  validateRuleDraft,
  type AlertRule,
  type AlertRuleDraft,
} from '@/api/alert-rules'
import { useAuthStore } from '@/stores/auth'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { statusOf } from '@/ui/status-map'
import type { AlertLevel } from '@/ui/alert-levels'
import { loadQuiet, saveQuiet, type QuietWindow } from '@/utils/alert-quiet'
import { clock } from '@/utils/format'

const REFRESH_MS = 15000

const auth = useAuthStore()

const rules = ref<AlertRule[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')

/* 引擎语义严格对齐 pkg/alert.rule.Step()：above/below 严格比较（贴线不响）、crosses 为现价相对上一笔基线越过目标价任一侧。 */
const CONDITION_OPTIONS = [
  { value: 'above', label: '上破（首次 > 目标价）' },
  { value: 'below', label: '下破（首次 < 目标价）' },
  { value: 'crosses', label: '穿越（相对上一笔越过目标价）' },
] as const
const CONDITION_TEXT: Record<string, string> = { above: '上破', below: '下破', crosses: '穿越' }

/** 新建/编辑同一表单；editingId 非空即编辑态（服务端 Edit 白名单重建）。 */
const editingId = ref('')
const form = ref<AlertRuleDraft>({ symbol: '', condition: 'above', targetPrice: '', note: '' })
const formError = computed(() =>
  form.value.symbol.trim() || form.value.targetPrice.trim() ? validateRuleDraft(form.value) : '',
)
const canSubmit = computed(() => !saving.value && !validateRuleDraft(form.value))

/** 删除两步确认（不引入弹窗）：首点臂起，5s 内再点执行。 */
const armingDeleteId = ref('')
let armTimer: ReturnType<typeof setTimeout> | undefined

/** NtTable 列键须是行键：`ops` 虚拟列由视图行补齐（取 id 仅作占位值，实际渲染走 #cell-ops 插槽）。 */
interface RuleRow extends AlertRule, Record<string, unknown> {
  ops: string
}
const rows = computed<RuleRow[]>(() => rules.value.map((r) => ({ ...r, ops: r.id })))

const columns: NtTableColumn<RuleRow>[] = [
  { key: 'symbol', title: '证券', kind: 'symbol', symbolMode: 'inline', width: '150px' },
  { key: 'condition', title: '条件', width: '72px' },
  { key: 'targetPrice', title: '目标价', align: 'right', mono: true, width: '90px' },
  { key: 'lastPrice', title: '最近价/基线', align: 'right', mono: true, width: '96px' },
  { key: 'status', title: '状态', width: '88px' },
  { key: 'armedAt', title: '布防时刻', mono: true, width: '86px' },
  { key: 'triggeredAt', title: '触发时刻', mono: true, width: '86px' },
  { key: 'note', title: '备注' },
  { key: 'ops', title: '操作', width: '200px' },
]

/** 写口成功后递增：轮询在途期间若已有写成功批次，本批陈旧读到站即弃（防「提交前旧快照覆盖权威全量」）。 */
let epoch = 0

async function load() {
  const burst = epoch
  loading.value = true
  error.value = ''
  try {
    const next = await listAlertRules()
    if (burst === epoch) rules.value = next
  } catch (e) {
    if (burst === epoch) error.value = e instanceof Error ? e.message : '规则列表读取失败'
  } finally {
    loading.value = false
  }
}

function resetForm() {
  editingId.value = ''
  form.value = { symbol: '', condition: 'above', targetPrice: '', note: '' }
}

function startEdit(rule: AlertRule) {
  editingId.value = rule.id
  form.value = {
    symbol: rule.symbol,
    condition: rule.condition,
    targetPrice: rule.targetPrice == null ? '' : String(rule.targetPrice),
    note: rule.note,
  }
}

async function submitForm() {
  if (!canSubmit.value) return
  saving.value = true
  error.value = ''
  try {
    const operator = auth.operatorName
    const next = editingId.value
      ? await editAlertRule(editingId.value, form.value, operator)
      : await createAlertRule(form.value, operator)
    epoch++
    rules.value = next
    resetForm()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存规则失败'
  } finally {
    saving.value = false
  }
}

async function doRearm(rule: AlertRule) {
  saving.value = true
  error.value = ''
  try {
    const next = await rearmAlertRule(rule.id, auth.operatorName)
    epoch++
    rules.value = next
  } catch (e) {
    error.value = e instanceof Error ? e.message : '重置布防失败'
  } finally {
    saving.value = false
  }
}

async function doDelete(rule: AlertRule) {
  if (armingDeleteId.value !== rule.id) {
    armingDeleteId.value = rule.id
    clearTimeout(armTimer)
    armTimer = setTimeout(() => {
      armingDeleteId.value = ''
    }, 5000)
    return
  }
  armingDeleteId.value = ''
  saving.value = true
  error.value = ''
  try {
    const next = await deleteAlertRule(rule.id, auth.operatorName)
    epoch++
    rules.value = next
  } catch (e) {
    error.value = e instanceof Error ? e.message : '删除规则失败'
  } finally {
    saving.value = false
  }
}

/** 状态机文案/tone 出自 alert 册（§7.1，一字锁定）：active 布防中 / triggered 已触发 / released 已解除；
 *  册外值返回 null，由调用处原样回显（不自封文案、不用 0/空串冒充）。 */
function statusEntry(status: string | null | undefined) {
  if (status === 'active') return statusOf('alert.armed')
  if (status === 'triggered') return statusOf('alert.triggered')
  if (status === 'released') return statusOf('alert.released')
  return null
}

/** 三分态诚呈（同仓 TradeView/audit-trail 约定）：首载中 / 读取失败 / 真空表分说，不把失败误示为「暂无」。 */
const emptyText = computed(() => {
  if (loading.value) return '读取中…'
  if (error.value) return '规则未返回，点上方刷新重试'
  return '暂无盯盘规则——先在上方新建一条'
})

/* ---------- 汇聚与静默（§8.3/§8.5） ----------
 * 读写全走 utils/alert-quiet（localStorage 持久化，读路径全容错，L4 恒直通不在级别选项内）。
 * 后端字段留账：proto 扩 alert_rule 冷却/静默字段、改服务端持久化，待 e4 ent 落批后接——本区先落本机。 */
const cooldownMin = ref('')
const quietWindows = ref<QuietWindow[]>([])

/** 'HH:MM' 校验：半成品窗留在编辑态不入库，feed 侧 quietHours 对坏格式本就判不适用。 */
const HHMM_RE = /^([01]?\d|2[0-3]):[0-5]\d$/

function syncQuietDraft() {
  const cfg = loadQuiet()
  cooldownMin.value = String(cfg.cooldownMs / 60000)
  quietWindows.value = cfg.windows.map((w) => ({ start: w.start, end: w.end, levels: [...w.levels] }))
}

watch(cooldownMin, (raw) => {
  const minutes = Number(raw)
  if (!raw.trim() || !Number.isFinite(minutes) || minutes < 0) return
  saveQuiet({ ...loadQuiet(), cooldownMs: minutes * 60000 })
})

function persistQuietWindows() {
  saveQuiet({ ...loadQuiet(), windows: quietWindows.value.filter((w) => HHMM_RE.test(w.start) && HHMM_RE.test(w.end)) })
}

function addQuietWindow() {
  /* 默认隔夜窗 + L1/L2：L3 重要不默认压，留用户自行抬手。 */
  quietWindows.value.push({ start: '22:00', end: '07:00', levels: ['L1', 'L2'] })
  persistQuietWindows()
}

function removeQuietWindow(index: number) {
  quietWindows.value.splice(index, 1)
  persistQuietWindows()
}

function setQuietLevel(w: QuietWindow, level: AlertLevel, on: boolean) {
  const next = new Set(w.levels)
  if (on) next.add(level)
  else next.delete(level)
  w.levels = [...next]
  persistQuietWindows()
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  syncQuietDraft()
  timer = setInterval(() => {
    if (!saving.value) void load()
  }, REFRESH_MS)
})

onBeforeUnmount(() => {
  clearInterval(timer)
  clearTimeout(armTimer)
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">盯盘告警规则</h1>
        <p class="nt-note">
          价格三态盯盘 · 引擎 2s 节拍评估（M08）· 触发一次后转「已触发」不再响，经 outbox 出站通知（M10）·
          编辑/重置布防按白名单重建防幽灵基线 · {{ rules.length }} 条规则
        </p>
      </div>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </header>

    <section class="nt-panel">
      <h2 class="nt-panel__title">{{ editingId ? '编辑规则（按白名单重建布防）' : '新建规则' }}</h2>
      <div class="form">
        <NtInput v-model="form.symbol" placeholder="600519.SH" mono class="form__symbol" />
        <NtSelect v-model="form.condition" :options="[...CONDITION_OPTIONS]" />
        <NtInput v-model="form.targetPrice" placeholder="目标价" type="number" class="form__price" />
        <NtInput v-model="form.note" placeholder="备注（可选）" class="form__note" />
        <NtButton size="sm" variant="primary" :loading="saving" :disabled="!canSubmit" @click="submitForm">
          {{ editingId ? '保存修改' : '创建' }}
        </NtButton>
        <NtButton v-if="editingId" size="sm" :disabled="saving" @click="resetForm">取消编辑</NtButton>
      </div>
      <p v-if="formError" class="nt-err">{{ formError }}</p>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">规则列表</h2>
      <NtTable :columns="columns" :rows="rows" row-key="id" dense :empty="emptyText">
        <template #cell-condition="{ row }">
          {{ CONDITION_TEXT[row.condition] || row.condition || '—' }}
        </template>
        <template #cell-status="{ row }">
          <NtBadge :tone="statusEntry(row.status)?.tone ?? 'neutral'">
            {{ statusEntry(row.status)?.text ?? (row.status || '—') }}
          </NtBadge>
        </template>
        <template #cell-armedAt="{ row }">
          {{ row.armedAt ? clock(new Date(row.armedAt)) : '—' }}
        </template>
        <template #cell-triggeredAt="{ row }">
          {{ row.triggeredAt ? clock(new Date(row.triggeredAt)) : '—' }}
        </template>
        <template #cell-ops="{ row }">
          <div class="ops">
            <NtButton size="sm" :disabled="saving" @click="startEdit(row)">编辑</NtButton>
            <NtButton size="sm" :disabled="saving || row.status !== 'triggered'" @click="doRearm(row)">重置布防</NtButton>
            <NtButton size="sm" :variant="armingDeleteId === row.id ? 'danger' : 'ghost'" :disabled="saving" @click="doDelete(row)">
              {{ armingDeleteId === row.id ? '再点确认删除' : '删除' }}
            </NtButton>
          </div>
        </template>
      </NtTable>
      <p v-if="error" class="nt-err">{{ error }}</p>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">汇聚与静默</h2>
      <div class="quiet">
        <div class="quiet__cooldown">
          <NtInput v-model="cooldownMin" type="number" label="同因冷却（分钟）" mono class="quiet__cooldown-input" />
          <p class="nt-note">同一告警原因再次触发时，窗内合并计数、不重复弹 toast。</p>
        </div>
        <div class="quiet__list">
          <div v-for="(w, i) in quietWindows" :key="i" class="quiet__row">
            <NtInput
              v-model="w.start"
              placeholder="22:00"
              mono
              class="quiet__time"
              @update:model-value="persistQuietWindows"
            />
            <span class="quiet__sep">至</span>
            <NtInput
              v-model="w.end"
              placeholder="07:00"
              mono
              class="quiet__time"
              @update:model-value="persistQuietWindows"
            />
            <div class="quiet__levels">
              <NtSwitch :model-value="w.levels.includes('L1')" label="L1" @update:model-value="setQuietLevel(w, 'L1', $event)" />
              <NtSwitch :model-value="w.levels.includes('L2')" label="L2" @update:model-value="setQuietLevel(w, 'L2', $event)" />
              <NtSwitch :model-value="w.levels.includes('L3')" label="L3" @update:model-value="setQuietLevel(w, 'L3', $event)" />
            </div>
            <NtButton size="sm" variant="ghost" @click="removeQuietWindow(i)">删除</NtButton>
          </div>
          <p v-if="!quietWindows.length" class="nt-note">暂无静默窗——时段内命中的级别只落账、不弹窗不响铃。</p>
          <div>
            <NtButton size="sm" @click="addQuietWindow">添加静默窗</NtButton>
          </div>
        </div>
        <p class="nt-note">L4 风险不受静默与冷却限制，始终全屏直达。</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.form {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
}

.form__symbol {
  width: 150px;
}

.form__price {
  width: 110px;
}

.form__note {
  flex: 1;
  min-width: 140px;
}

.ops {
  display: flex;
  gap: 6px;
  align-items: center;
}

.quiet {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.quiet__cooldown {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  flex-wrap: wrap;
}

.quiet__cooldown-input {
  width: 160px;
}

.quiet__list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.quiet__row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.quiet__time {
  width: 96px;
}

.quiet__sep {
  color: var(--nt-text-3);
}

.quiet__levels {
  display: flex;
  align-items: center;
  gap: 12px;
}
</style>
