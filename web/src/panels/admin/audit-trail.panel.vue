<script setup lang="ts">
/**
 * 审计日志面板（M11）：读取 admin 网关 /api/v1/audit。
 * 支持按动作过滤 + 条数限制，时间倒序展示谁在什么时候动了什么。
 */
import { computed, onMounted, ref } from 'vue'
import { listAudit, type AuditRow } from '@/api/audit'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { clock, messageOf } from '@/utils/format'

const rows = ref<AuditRow[]>([])
const loading = ref(false)
const error = ref('')
const action = ref('')
const limit = ref(100)

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
const columns: NtTableColumn<Row>[] = [
  { key: 'createdAt', title: '时间', mono: true },
  { key: 'actor', title: '操作人' },
  { key: 'role', title: '角色', mono: true },
  { key: 'action', title: '动作', mono: true },
  { key: 'target', title: '目标' },
  { key: 'detail', title: '详情' },
  { key: 'ok', title: '结果' },
]
const tableRows = computed(() => rows.value as unknown as Row[])

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

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">审计日志</h1>
        <p class="nt-note">{{ rows.length }} 条记录 · 登录与敏感操作全量留痕</p>
      </div>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </header>

    <div class="filters">
      <NtSelect v-model="action" :options="actionOptions" placeholder="动作" @update:model-value="load" />
      <NtSelect v-model="limit" :options="limitOptions" @update:model-value="load" />
    </div>

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <NtTable :columns="columns" :rows="tableRows" row-key="id" dense :empty="loading ? '加载中…' : '还没有审计记录'">
        <template #cell-createdAt="{ row }">{{ clock(row.createdAt) }}</template>
        <template #cell-role="{ row }">
          <NtBadge :tone="row.role === 'owner' ? 'accent' : 'neutral'">{{ row.role || '—' }}</NtBadge>
        </template>
        <template #cell-target="{ row }">{{ row.target || '—' }}</template>
        <template #cell-detail="{ row }">{{ row.detail || '—' }}</template>
        <template #cell-ok="{ row }">
          <NtBadge :tone="row.ok ? 'success' : 'danger'" dot>{{ row.ok ? '成功' : '失败' }}</NtBadge>
        </template>
      </NtTable>
    </section>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 8px;
  margin-bottom: 10px;
}

.filters > * {
  min-width: 140px;
}
</style>
