<script setup lang="ts">
/**
 * 自选/扫描面板：登记市场候选池（10 分钟租约，需心跳续期）+ 触发策略扫描。
 * 写操作仅 owner；扫描结果跳到 pool-manager 面板查看。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { scan, type ScanReply } from '@/api/strategy'
import NtButton from '@/ui/NtButton.vue'
import { useAuthStore } from '@/stores/auth'
import { getWorkbench } from '@/workspace/inject'
import { clock, messageOf, missLabel } from '@/utils/format'
import { request } from '@/api/http'

const auth = useAuthStore()

const draft = ref('')
const saving = ref(false)
const scanning = ref(false)
const error = ref('')
const expiresAt = ref('')
const lastScan = ref<ScanReply | null>(null)

/** dropped: 规则号 → 只数，按数量倒序。 */
const droppedRows = computed(() =>
  Object.entries(lastScan.value?.dropped ?? {})
    .map(([rule, count]) => ({ rule, count }))
    .sort((a, b) => b.count - a.count),
)
/** misses: 代码 → 原因。 */
const missRows = computed(() =>
  Object.entries(lastScan.value?.misses ?? {}).map(([symbol, reason]) => ({ symbol, reason })),
)

const SYMBOL_RE = /^\d{6}\.(SZ|SH|BJ)$/i
let heartbeat: ReturnType<typeof setInterval> | undefined

async function save() {
  const symbols = draft.value
    .split(/[\s,;，；]+/)
    .map((s) => s.trim().toUpperCase())
    .filter(Boolean)
  const bad = symbols.filter((s) => !SYMBOL_RE.test(s))
  if (bad.length) {
    error.value = `代码格式不对：${bad.join('、')}（应为 000001.SZ）`
    return
  }
  saving.value = true
  error.value = ''
  try {
    const data = (await request('/api/market/watchlist', {
      method: 'POST',
      json: { owner: auth.operatorName || 'web', symbols },
    })) as Record<string, unknown>
    expiresAt.value = String(data?.expiresAt ?? data?.expires_at ?? '')
    startHeartbeat(symbols)
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    saving.value = false
  }
}

function startHeartbeat(symbols: string[]) {
  stopHeartbeat()
  if (!symbols.length) return
  // 租约 10 分钟，8 分钟续一次。
  heartbeat = setInterval(
    () => {
      void request('/api/market/watchlist', {
        method: 'POST',
        json: { owner: auth.operatorName || 'web', symbols },
      }).then((d) => {
        expiresAt.value = String((d as Record<string, unknown>)?.expiresAt ?? '')
      })
    },
    8 * 60 * 1000,
  )
}

function stopHeartbeat() {
  if (heartbeat) clearInterval(heartbeat)
  heartbeat = undefined
}

async function runScan() {
  scanning.value = true
  error.value = ''
  try {
    lastScan.value = await scan({})
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    scanning.value = false
  }
}

function openPool() {
  getWorkbench()?.openPanel('pool-manager')
}

function openMarket(symbol: string) {
  getWorkbench()?.openPanel('market', { symbol })
}

onMounted(() => {})
onBeforeUnmount(stopHeartbeat)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">自选 / 扫描</h1>
        <p class="nt-note">登记候选池租约 10 分钟，面板开着会自动续期。</p>
      </div>
    </header>

    <section class="nt-panel">
      <h2 class="nt-panel__title">候选池</h2>
      <textarea v-model="draft" class="draft" rows="3" placeholder="000001.SZ 600519.SH …（空格或逗号分隔）" />
      <div class="row">
        <NtButton v-if="!auth.isViewer()" size="sm" :loading="saving" :disabled="!draft.trim()" @click="save">登记</NtButton>
        <NtButton v-if="!auth.isViewer()" size="sm" variant="primary" :loading="scanning" @click="runScan">
          立即扫描
        </NtButton>
        <NtButton size="sm" @click="openPool">打开股池面板</NtButton>
      </div>
      <p v-if="expiresAt" class="nt-note">租约有效至 {{ clock(expiresAt) }}</p>
      <p v-if="error" class="nt-err">{{ error }}</p>
    </section>

    <section v-if="lastScan" class="nt-panel">
      <h2 class="nt-panel__title">本次扫描</h2>
      <p class="nt-note">
        范围 {{ lastScan.universe ?? '—' }} 只 → 命中 {{ lastScan.matched ?? '—' }} → 进模型排序
        {{ lastScan.ranked ?? '—' }}（调用 {{ lastScan.aiCalls ?? '—' }} 次
        <template v-if="lastScan.degraded">，模型降级</template>） → 买入信号 {{ lastScan.signalIds.length }} 条 / 卖出信号
        {{ lastScan.sellSignalIds.length }} 条。耗时 {{ lastScan.elapsedMs ?? '—' }} ms。
        <span v-if="lastScan.killSwitch" class="kill">扫描时急停开着，买入信号不会执行。</span>
      </p>

      <template v-if="droppedRows.length">
        <h3 class="sub">被规则剔除</h3>
        <ul class="kv">
          <li v-for="row in droppedRows" :key="row.rule">
            <span>{{ missLabel(row.rule) }}</span>
            <b>{{ row.count }} 只</b>
          </li>
        </ul>
      </template>

      <template v-if="missRows.length">
        <h3 class="sub">通过规则但没进信号</h3>
        <ul class="kv">
          <li v-for="row in missRows" :key="row.symbol">
            <button type="button" class="sym" @click="openMarket(row.symbol)">{{ row.symbol }}</button>
            <span>{{ missLabel(row.reason) }}</span>
          </li>
        </ul>
      </template>

      <p class="nt-note">trace {{ lastScan.traceId || '—' }}，凭它可以在股池面板核对候选。</p>
    </section>
  </div>
</template>

<style scoped>
.row {
  display: flex;
  gap: 8px;
  margin-top: 8px;
  flex-wrap: wrap;
}

.draft {
  width: 100%;
  box-sizing: border-box;
  padding: 8px 10px;
  font-family: var(--nt-font-mono);
  font-size: 12.5px;
  color: var(--nt-text-1);
  background: var(--nt-bg-2);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  resize: vertical;
}

.draft:focus {
  outline: none;
  border-color: var(--nt-accent);
}

.sub {
  margin: 12px 0 0;
  font-size: 12.5px;
  font-weight: 600;
}

.kv {
  list-style: none;
  margin: 6px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
}

.kv li {
  display: flex;
  gap: 8px;
  align-items: baseline;
}

.kv b {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.sym {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-accent);
  font: inherit;
  font-family: var(--nt-font-mono);
  cursor: pointer;
}

.sym:hover {
  text-decoration: underline;
}

.kill {
  color: var(--nt-danger);
  font-weight: 600;
}
</style>
