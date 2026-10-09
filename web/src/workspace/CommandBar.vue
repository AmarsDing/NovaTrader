<script setup lang="ts">
/**
 * 顶栏 chrome（不进 dock）：品牌 + 权益/盈亏 + Kill Switch + 命令面板入口 + 布局预设 + 用户。
 * 玻璃拟态允许出现的位置之一（另两处：弹层、AlertCenter）。
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import NovaStatusPulse from '@/component/NovaStatusPulse.vue'
import KillSwitchBar from '@/widgets/KillSwitchBar.vue'
import NtButton from '@/ui/NtButton.vue'
import { useAccountStore } from '@/stores/account'
import { useAuthStore } from '@/stores/auth'
import { useLayoutStore } from '@/stores/layout'
import { amount, pnlClass, signed } from '@/utils/format'
import { isLive } from '@/ws/hub'
import { useWorkbench } from './inject'

const emit = defineEmits<{ (e: 'palette'): void }>()

const router = useRouter()
const auth = useAuthStore()
const account = useAccountStore()
const layout = useLayoutStore()
const workbench = useWorkbench()

const displayName = computed(() => auth.user?.displayName || auth.user?.name || '交易员')
const pnl = computed(() => (account.dayPnl != null ? account.dayPnl : account.unrealized))
const pnlLabel = computed(() => (account.dayPnl != null ? '当日盈亏' : '浮动盈亏'))

const menuOpen = ref(false)
const newName = ref('')
const fileEl = ref<HTMLInputElement | null>(null)
const presetNames = computed(() => Object.keys(layout.presets))

function toggleMenu() {
  menuOpen.value = !menuOpen.value
}

function onDocDown(event: MouseEvent) {
  const target = event.target as HTMLElement | null
  if (target?.closest?.('.preset-wrap')) return
  menuOpen.value = false
}

watch(menuOpen, (open) => {
  if (open) document.addEventListener('mousedown', onDocDown)
  else document.removeEventListener('mousedown', onDocDown)
})

onBeforeUnmount(() => document.removeEventListener('mousedown', onDocDown))

function savePreset() {
  const name = newName.value.trim()
  if (!name || !workbench) return
  layout.savePreset(name, workbench.captureLayout())
  newName.value = ''
}

function applyPreset(name: string) {
  menuOpen.value = false
  workbench?.applyLayout(layout.presets[name])
}

function dropPreset(name: string) {
  layout.removePreset(name)
}

function resetLayout() {
  menuOpen.value = false
  workbench?.resetToDefault()
}

function exportPresets() {
  try {
    const blob = new Blob([JSON.stringify(layout.presets, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'novatrader-layout-presets.json'
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    /* 下载不可用时忽略：预设仍在本地 */
  }
}

function pickImport() {
  fileEl.value?.click()
}

async function onImport(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  try {
    const parsed = JSON.parse(await file.text()) as Record<string, unknown>
    Object.entries(parsed).forEach(([name, value]) => layout.savePreset(name, value))
  } catch {
    /* 非法 JSON 直接忽略 */
  }
}

function logout() {
  void auth.clear()
  void router.push({ name: 'login' })
}
</script>

<template>
  <header class="bar">
    <div class="brand">
      <NovaStatusPulse :active="isLive()" />
      <span class="logo">NovaTrader</span>
      <span class="sub">智脑</span>
    </div>

    <div class="metrics">
      <div class="metric">
        <span class="label">权益 · {{ account.book }}</span>
        <span class="value nt-holo-num">{{ amount(account.equity) }}</span>
      </div>
      <div class="metric" :class="pnlClass(pnl)">
        <span class="label">{{ pnlLabel }}</span>
        <span class="value nt-holo-num">{{ signed(pnl) }}</span>
      </div>
    </div>

    <div class="actions">
      <KillSwitchBar />
      <NtButton size="sm" title="Ctrl+K" @click="emit('palette')">命令面板</NtButton>
      <div class="preset-wrap">
        <NtButton size="sm" @click="toggleMenu">布局预设</NtButton>
        <div v-if="menuOpen" class="menu">
          <p v-if="!presetNames.length" class="menu-empty">还没有保存的预设</p>
          <div v-for="name in presetNames" :key="name" class="menu-row">
            <button type="button" class="menu-name" @click="applyPreset(name)">{{ name }}</button>
            <button type="button" class="menu-del" title="删除预设" @click="dropPreset(name)">×</button>
          </div>
          <div class="menu-new">
            <input v-model="newName" class="menu-input" placeholder="新预设名称" @keydown.enter="savePreset" />
            <NtButton size="sm" :disabled="!newName.trim()" @click="savePreset">保存</NtButton>
          </div>
          <div class="menu-foot">
            <button type="button" class="menu-link" @click="resetLayout">重置为默认</button>
            <button type="button" class="menu-link" @click="exportPresets">导出</button>
            <button type="button" class="menu-link" @click="pickImport">导入</button>
          </div>
          <input ref="fileEl" type="file" accept="application/json" hidden @change="onImport" />
        </div>
      </div>
      <span class="who">{{ displayName }}</span>
      <NtButton size="sm" @click="logout">退出</NtButton>
    </div>
  </header>
</template>

<style scoped>
.bar {
  display: flex;
  align-items: center;
  gap: 12px 20px;
  padding: 8px 14px;
  flex-wrap: wrap;
  background: var(--nt-bg-glass);
  backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--nt-border-1);
}

.brand,
.actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.logo {
  font-weight: 700;
  color: var(--nt-text-1);
}

.sub {
  font-size: 11px;
  color: var(--nt-text-2);
  padding: 2px 6px;
  border-radius: var(--nt-radius-s);
  border: 1px solid var(--nt-border-2);
}

.metrics {
  display: flex;
  flex: 1;
  justify-content: center;
  gap: 32px;
}

.metric {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.metric .label {
  font-size: 10.5px;
  color: var(--nt-text-3);
}

.metric .value {
  font-size: 16px;
  font-weight: 650;
}

.metric.up .value {
  color: var(--nt-up);
}

.metric.down .value {
  color: var(--nt-down);
}

.actions {
  margin-left: auto;
}

.who {
  color: var(--nt-text-2);
  font-size: 12.5px;
}

.preset-wrap {
  position: relative;
}

.menu {
  position: absolute;
  right: 0;
  top: calc(100% + 6px);
  min-width: 230px;
  padding: 8px;
  z-index: 60;
  background: var(--nt-bg-glass);
  backdrop-filter: blur(10px);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-m);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.45);
}

.menu-row {
  display: flex;
  align-items: center;
  gap: 4px;
}

.menu-name {
  flex: 1;
  text-align: left;
  padding: 6px 8px;
  border: none;
  border-radius: var(--nt-radius-s);
  background: transparent;
  color: var(--nt-text-1);
  font-size: 12.5px;
  cursor: pointer;
}

.menu-name:hover {
  background: rgba(0, 229, 255, 0.1);
}

.menu-del,
.menu-link {
  border: none;
  background: transparent;
  color: var(--nt-text-3);
  cursor: pointer;
  font-size: 12px;
}

.menu-del:hover {
  color: var(--nt-danger);
}

.menu-new {
  display: flex;
  gap: 6px;
  margin-top: 6px;
}

.menu-input {
  flex: 1;
  min-width: 0;
  padding: 5px 8px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-0);
  color: var(--nt-text-1);
  font-size: 12.5px;
  outline: none;
}

.menu-foot {
  display: flex;
  gap: 12px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--nt-border-1);
}

.menu-link:hover {
  color: var(--nt-accent);
}

.menu-empty {
  margin: 4px 0 8px;
  font-size: 12px;
  color: var(--nt-text-3);
}
</style>
