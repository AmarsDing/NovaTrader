<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { BACKUP_CONFIRM, opsStatus, runBackup, type BackupResult, type OpsNode } from '@/api/ops'
import { router } from '@/router'
import NtSourceTable from '@/display/NtSourceTable.vue'
import { opsNodeStatus, toOpsSourceRow } from '@/display/source-rows'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
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
  { key: 'monitor', title: '' },
]
const modelColumns: NtTableColumn<NodeRow>[] = [
  { key: 'name', title: '名称' },
  { key: 'status', title: '状态' },
  { key: 'latency', title: '延迟' },
  { key: 'detail', title: '说明' },
  { key: 'monitor', title: '' },
]

/**
 * 网关节点状态（点色+册内状态词）唯一出处（W0-3b/FE-D1）：display/source-rows 的 opsNodeStatus。
 * 服务/模型/本机块经这两个转发复用同一份映射；数据源块直接走 NtSourceTable(ops)。
 */
/** W0-3b/FE-D1：数据源块归一行（状态点/状态词/缺位在 NtSourceTable 收口），本视图只喂 opsStatus 数据。 */
const opsRows = computed(() => sources.value.map(toOpsSourceRow))

function dotStatus(status: string) {
  return opsNodeStatus(status).dot
}
function nodeStatusText(status: string): string {
  return opsNodeStatus(status).text
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

/** 静默追帧（W3-1 值守面）：不翻 loading 章、失败不清空旧数据——中断本身会由行内状态点表达。 */
async function refreshQuiet() {
  try {
    const data = await opsStatus()
    services.value = data.services
    sources.value = data.sources
    models.value = data.models as OpsModelRow[]
    resources.value = data.resources
    error.value = ''
  } catch {
    /* 静默：保留上一份数据，等下一拍 */
  }
}

/** 运维是值守常驻视图（W3-1）：30s 静默追帧补齐手动刷新的盲区；隐藏页暂停。 */
const QUIET_POLL_MS = 30_000
let pollTimer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  void load()
  pollTimer = setInterval(() => {
    if (document.hidden) return
    void refreshQuiet()
  }, QUIET_POLL_MS)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})

function openMonitor() {
  void router.push('/monitor')
}

/** FE-D3：行级深链——/monitor?node=<名> 让监控大屏定位到对应实体。 */
function openMonitorEntity(name: string) {
  void router.push({ path: '/monitor', query: { node: name } })
}

/** FE-D3：首屏红黄汇总条。红=down 及其余异常；黄=degraded/warn；灰（off/未知）不计入异常。 */
const summary = computed(() => {
  let red = 0
  let warn = 0
  let total = 0
  const tally = (dot: string) => {
    total += 1
    if (dot === 'warn') warn += 1
    else if (dot !== 'live' && dot !== 'off') red += 1
  }
  for (const row of [...services.value, ...models.value, ...resources.value]) tally(dotStatus(row.status))
  for (const row of opsRows.value) tally(row.status)
  return { red, warn, total }
})

// 备份是写盘动作（FR-11-08）：先要口令，再跑，成败都写审计。
const asking = ref(false)
const phrase = ref('')
const busy = ref(false)
const backupResult = ref<BackupResult | null>(null)
const backupError = ref('')

const canRun = computed(() => phrase.value.trim() === BACKUP_CONFIRM && !busy.value)
const sizeText = computed(() => {
  const size = backupResult.value?.size
  if (size == null) return '大小未返回'
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1048576).toFixed(2)} MB`
})
const durationText = computed(() => {
  const ms = backupResult.value?.durationMs
  return ms == null ? '耗时未返回' : `${ms} ms`
})

function startBackup() {
  backupError.value = ''
  backupResult.value = null
  phrase.value = ''
  asking.value = true
}

function cancelBackup() {
  if (busy.value) return
  asking.value = false
  phrase.value = ''
}

async function doBackup() {
  if (!canRun.value) return
  busy.value = true
  backupError.value = ''
  backupResult.value = null
  try {
    backupResult.value = await runBackup(phrase.value.trim())
    asking.value = false
    phrase.value = ''
  } catch (err) {
    backupError.value = messageOf(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">运维</h1>
        <p class="nt-note">服务、数据源、模型和本机磁盘、显存。状态来自管理网关聚合。</p>
      </div>
      <div class="actions">
        <NtButton size="sm" @click="openMonitor">打开监控大屏</NtButton>
        <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
      </div>
    </header>
    <NtErrorGuide :error="error" />
    <!-- FE-D3：红黄汇总条（红=down 及其余异常；黄=degraded/warn） -->
    <p v-if="summary.total" class="ops-summary" :data-tone="summary.red ? 'red' : summary.warn ? 'warn' : 'ok'">
      共 {{ summary.total }} 个节点 · 异常 {{ summary.red }} · 预警 {{ summary.warn }}
      <template v-if="!summary.red && !summary.warn">· 全部正常</template>
    </p>
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
      <!-- W0-3b/FE-D1：数据源块走 NtSourceTable(ops) 单一事实组件，与配置中心链路页同源。 -->
      <NtSourceTable v-if="block.title === '数据源'" variant="ops" :ops="opsRows" empty="暂无" />
      <NtTable
        v-else
        :columns="block.extra ? modelColumns : baseColumns"
        :rows="block.rows as NodeRow[]"
        row-key="name"
        empty="暂无"
      >
        <template #cell-name="{ value, row }">
          <span class="cell-name">
            <NtStatusDot
              :status="dotStatus(row.status)"
              :label="nodeStatusText(row.status)"
              :title="row.status || '状态未返回'"
            />{{ value }}
          </span>
        </template>
        <!-- 状态列保留网关原始字面值（数据原样直读，非自封文案）；状态判定词已随点名册化。 -->
        <template #cell-status="{ value }">{{ value || '—' }}</template>
        <template #cell-latency="{ value }">{{ value || '—' }}</template>
        <template #cell-detail="{ value }">{{ value || '—' }}</template>
        <!-- FE-D3：行级深链到监控大屏（实体定位） -->
        <template #cell-monitor="{ row }">
          <button type="button" class="nt-link" @click="openMonitorEntity(row.name)">在监控大屏查看</button>
        </template>
      </NtTable>
    </section>
    <section>
      <h2 class="nt-panel__title">数据库备份</h2>
      <p class="nt-note">
        在后端跑一次 pg_dump，写出到备份目录（NT_BACKUP_DIR，默认 backups/）。写盘动作，需要二次确认。
      </p>
      <div v-if="!asking" class="actions">
        <NtButton size="sm" :disabled="busy" @click="startBackup">备份数据库</NtButton>
      </div>
      <div v-else class="confirm">
        <NtInput
          v-model="phrase"
          :label="`输入 ${BACKUP_CONFIRM} 确认`"
          :placeholder="BACKUP_CONFIRM"
          mono
          :disabled="busy"
        />
        <div class="actions">
          <NtButton size="sm" variant="primary" :disabled="!canRun" :loading="busy" @click="doBackup">
            执行备份
          </NtButton>
          <NtButton size="sm" :disabled="busy" @click="cancelBackup">取消</NtButton>
        </div>
      </div>
      <NtErrorGuide :error="backupError" />
      <p v-if="backupResult" class="result">
        已写出 <b>{{ backupResult.path }}</b>（{{ sizeText }}，{{ durationText }}，库
        {{ backupResult.database }}）
      </p>
      <p class="nt-note">
        服务重启的二次确认（FR-11-08 另一半）未做：admin 重启不了同级进程，需要外部进程管理器，当前部署没有；重启在机器上手动执行。
      </p>
    </section>
  </div>
</template>

<style scoped>
.actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

/* FE-D3 红黄汇总条 */
.ops-summary {
  margin: 8px 0 0;
  padding: 6px 10px;
  border: 1px solid var(--nt-border-1);
  border-radius: 4px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.ops-summary[data-tone='red'] {
  border-color: var(--nt-danger);
  color: var(--nt-danger);
}

.ops-summary[data-tone='warn'] {
  border-color: var(--nt-warn);
  color: var(--nt-warn);
}

.ops-summary[data-tone='ok'] {
  color: var(--nt-success);
}

.nt-link {
  border: 0;
  background: none;
  padding: 0;
  cursor: pointer;
  color: var(--nt-accent);
  font-size: var(--nt-fs-caption);
  text-decoration: underline;
}

.confirm {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 380px;
}

.result {
  margin: 8px 0 0;
  font-size: 12.5px;
  color: var(--nt-text-2);
  word-break: break-all;
}

.result b {
  font-family: var(--nt-font-mono);
  font-weight: 500;
  color: var(--nt-text-1);
}

.cell-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
</style>
