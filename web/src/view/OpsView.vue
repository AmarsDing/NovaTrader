<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { BACKUP_CONFIRM, opsStatus, runBackup, type BackupResult, type OpsNode } from '@/api/ops'
import { router } from '@/router'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtStatusDot, { type DotStatus } from '@/ui/NtStatusDot.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { linkStatus, statusOf as statusMapOf } from '@/ui/status-map'
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

/**
 * 网关四态（W2-c 拆态）→ 链路五点色：ok 族=live 绿；warn=stale 点取 warn 色（半开/覆盖率未证实等降级类）；
 * unknown=off 点取中性灰（未观测/未配置/不采集等结构性缺省）；down 与其余异常=红。
 * 此前 warn/unknown 落 default 画红，把「降级」「没测过」谎报成「中断」。
 */
function dotStatus(status: string): DotStatus {
  const text = String(status || '').toLowerCase()
  if (['ok', 'up', 'running', 'healthy', 'online'].includes(text)) return 'live'
  if (text === 'warn') return 'stale'
  if (text === 'unknown' || !text) return 'off'
  return 'down'
}

/** 节点状态词只从册取（§7.1/§10-8，一字锁定）：warn=册键 degraded.partial「部分降级」，
 *  unknown=册键 ops.unknown「未知」；裸色点禁单显（§7.2-1）。精确归因见「说明」列 detail。 */
function nodeStatusText(status: string): string {
  const text = String(status || '').toLowerCase()
  if (text === 'warn') return statusMapOf('degraded.partial').text
  if (text === 'unknown') return statusMapOf('ops.unknown').text
  return linkStatus(dotStatus(status)).text
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
