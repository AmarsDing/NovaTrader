<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { opsStatus, type OpsNode } from '@/api/ops'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { getHost, setHost } from '@/utils/endpoint'
import { messageOf } from '@/utils/format'
import { inTauri } from '@/utils/tauri'
import { restartLink } from '@/ws/hub'

const auth = useAuthStore()
const risk = useRiskStore()

const host = ref(getHost())
const sources = ref<OpsNode[]>([])
const auto = ref(false)
const desktop = ref(false)
const paramKey = ref('')
const paramValue = ref('')
const error = ref('')
const hint = ref('')
const modes = ['L0', 'L1', 'L2', 'L3']

interface ParamRow extends Record<string, unknown> {
  key: string
  value: string
}

const paramColumns: NtTableColumn<ParamRow>[] = [
  { key: 'key', title: '键' },
  { key: 'value', title: '值' },
]

function paramRows(): ParamRow[] {
  return Object.entries(risk.params).map(([key, value]) => ({ key, value: String(value) }))
}

async function saveHost() {
  error.value = ''
  hint.value = ''
  try {
    host.value = setHost(host.value)
    restartLink()
    hint.value = '已改为 admin ' + host.value + ' 的 2001 / 2003'
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function switchMode(mode: string) {
  error.value = ''
  if (auth.isViewer()) {
    error.value = '当前是查看角色，不能改模式'
    return
  }
  try {
    await risk.switchMode({ mode, operator: auth.operatorName, reason: '客户端切换运行模式' })
    hint.value = '模式请求已提交'
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function saveParam() {
  error.value = ''
  if (auth.isViewer()) {
    error.value = '当前是查看角色，不能改参数'
    return
  }
  if (!paramKey.value.trim()) {
    error.value = '请填写参数键'
    return
  }
  try {
    await risk.saveParam({ key: paramKey.value.trim(), value: paramValue.value, operator: auth.operatorName })
    hint.value = '参数已提交'
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function toggleAuto() {
  error.value = ''
  try {
    const mod = await import('@tauri-apps/plugin-autostart')
    if (auto.value) await mod.disable()
    else await mod.enable()
    auto.value = await mod.isEnabled()
  } catch (err) {
    error.value = messageOf(err)
  }
}

onMounted(async () => {
  void risk.refresh()
  desktop.value = inTauri()
  try {
    const data = await opsStatus()
    sources.value = data.sources
  } catch {
    sources.value = []
  }
  if (!desktop.value) return
  try {
    const { isEnabled } = await import('@tauri-apps/plugin-autostart')
    auto.value = await isEnabled()
  } catch {
    auto.value = false
  }
})
</script>

<template>
  <div class="nt-page">
    <header>
      <h1 class="nt-title">设置</h1>
      <p class="nt-note">只配置 admin 主机。行情和交易端口不在这里直连。快捷键 Ctrl+Shift+N 把窗口从托盘唤回。</p>
    </header>

    <section class="nt-panel">
      <h2 class="nt-panel__title">数据源</h2>
      <form class="nt-form" @submit.prevent="saveHost">
        <label class="grow"><span>admin 主机</span><NtInput v-model="host" mono /></label>
        <NtButton variant="primary" @click="saveHost">保存</NtButton>
      </form>
      <p class="nt-note">HTTP 固定 2001，WebSocket 固定 2003。</p>
      <ul class="src">
        <li v-for="row in sources" :key="row.name">{{ row.name }} · {{ row.status || '—' }} · {{ row.detail }}</li>
      </ul>
      <p v-if="!sources.length" class="nt-empty">数据源清单未返回。</p>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">运行模式</h2>
      <p class="nt-note">
        当前 {{ risk.mode || '—' }}。L0 只看，L1 模拟，L2 半自动实盘，L3 按 L2 处理。切到 L2/L3 需要准入已通过。
      </p>
      <div class="modes">
        <NtButton
          v-for="mode in modes"
          :key="mode"
          :variant="risk.mode === mode ? 'primary' : 'ghost'"
          :disabled="auth.isViewer()"
          @click="switchMode(mode)"
        >
          {{ mode }}
        </NtButton>
      </div>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">风控参数</h2>
      <NtTable :columns="paramColumns" :rows="paramRows()" row-key="key" empty="参数未返回" />
      <form class="nt-form" @submit.prevent="saveParam">
        <label><span>键</span><NtInput v-model="paramKey" placeholder="risk.max_single_pct" mono /></label>
        <label><span>值</span><NtInput v-model="paramValue" mono /></label>
        <NtButton variant="primary" :disabled="auth.isViewer()" @click="saveParam">提交</NtButton>
      </form>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">桌面</h2>
      <p class="nt-note">关闭窗口会缩进托盘。令牌放在系统凭据库；浏览器开发态只留在本次会话。</p>
      <NtButton v-if="desktop" @click="toggleAuto">开机自启：{{ auto ? '已开' : '已关' }}</NtButton>
      <p v-else class="nt-empty">开机自启和系统通知只在桌面壳里可用。</p>
    </section>

    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="hint" class="nt-note">{{ hint }}</p>
  </div>
</template>

<style scoped>
section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.modes {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.src {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  color: var(--nt-text-2);
  font-size: 12.5px;
}
</style>
