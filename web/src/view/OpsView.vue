<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { opsStatus, type OpsNode } from '@/api/ops'
import NtButton from '@/ui/NtButton.vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { messageOf } from '@/utils/format'

interface OpsModelRow extends OpsNode {
  latency?: string
}

const services = ref<OpsNode[]>([])
const sources = ref<OpsNode[]>([])
const models = ref<OpsModelRow[]>([])
const resources = ref<OpsNode[]>([])
const error = ref('')
const loading = ref(false)

type NodeRow = Record<string, unknown> & { name: string; status: string; detail: string; latency?: string }

const baseColumns: NtTableColumn<NodeRow>[] = [
  { key: 'name', title: '名称' },
  { key: 'status', title: '状态' },
  { key: 'detail', title: '说明' },
]
const modelColumns: NtTableColumn<NodeRow>[] = [
  { key: 'name', title: '名称' },
  { key: 'status', title: '状态' },
  { key: 'latency', title: '延迟' },
  { key: 'detail', title: '说明' },
]

function dotStatus(status: string): 'live' | 'off' | 'down' {
  const text = String(status || '').toLowerCase()
  if (['ok', 'up', 'running', 'healthy', 'online'].includes(text)) return 'live'
  if (!text) return 'off'
  return 'down'
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await opsStatus()
    services.value = data.services
    sources.value = data.sources
    models.value = data.models as OpsModelRow[]
    resources.value = data.resources
  } catch (err) {
    services.value = []
    sources.value = []
    models.value = []
    resources.value = []
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
        <h1 class="nt-title">运维</h1>
        <p class="nt-note">服务、数据源、模型和本机磁盘、显存。状态来自管理网关聚合。</p>
      </div>
      <NtButton :loading="loading" @click="load">刷新</NtButton>
    </header>
    <p v-if="error" class="nt-err">{{ error }}</p>
    <section
      v-for="block in [
        { title: '服务', rows: services, extra: false },
        { title: '数据源', rows: sources, extra: false },
        { title: '模型', rows: models, extra: true },
        { title: '本机', rows: resources, extra: false },
      ]"
      :key="block.title"
    >
      <h2 class="nt-panel__title">{{ block.title }}</h2>
      <NtTable
        :columns="block.extra ? modelColumns : baseColumns"
        :rows="block.rows as NodeRow[]"
        row-key="name"
        empty="暂无"
      >
        <template #cell-name="{ value, row }">
          <span class="cell-name"><NtStatusDot :status="dotStatus(row.status)" />{{ value }}</span>
        </template>
        <template #cell-status="{ value }">{{ value || '—' }}</template>
        <template #cell-latency="{ value }">{{ value || '—' }}</template>
        <template #cell-detail="{ value }">{{ value || '—' }}</template>
      </NtTable>
    </section>
  </div>
</template>

<style scoped>
.cell-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
</style>
