<script setup lang="ts">
/**
 * 顶栏 chrome（不进 dock）：品牌 + 权益/盈亏 + Kill Switch + 命令面板入口 + 布局预设 + 用户。
 * 玻璃拟态允许出现的位置之一（另两处：弹层、AlertCenter）。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import NovaStatusPulse from '@/component/NovaStatusPulse.vue'
import KillSwitchBar from '@/widgets/KillSwitchBar.vue'
import NtButton from '@/ui/NtButton.vue'
import { useAccountStore } from '@/stores/account'
import { useAuthStore } from '@/stores/auth'
import { useLayoutStore } from '@/stores/layout'
import { useSignalsStore } from '@/stores/signals'
import { amount, pnlClass, signed } from '@/utils/format'
import { isLive } from '@/ws/hub'
import { isPendingStatus } from '@/view/decision-desk'
import { getWorkbench } from './inject'

const emit = defineEmits<{ (e: 'palette'): void; (e: 'config'): void }>()

const router = useRouter()
const auth = useAuthStore()
const account = useAccountStore()
const layout = useLayoutStore()

const displayName = computed(() => auth.user?.displayName || auth.user?.name || '交易员')
const pnl = computed(() => (account.dayPnl != null ? account.dayPnl : account.unrealized))
const pnlLabel = computed(() => (account.dayPnl != null ? '当日盈亏' : '浮动盈亏'))

/* P3：「待执行 N」与决策台左栏同一口径，30 秒一拍；点击聚焦决策台面板 */
const signals = useSignalsStore()
const pendingCount = computed(() => signals.items.filter((item) => isPendingStatus(item.status)).length)
let signalTimer: ReturnType<typeof setInterval> | null = null

function focusDesk() {
  getWorkbench()?.openPanel('overview')
}

const menuOpen = ref(false)
const newName = ref('')
const nameInput = ref<HTMLInputElement | null>(null)
const savedHint = ref('')
let hintTimer: ReturnType<typeof setTimeout> | null = null
const renaming = ref('')
const renameText = ref('')
const fileEl = ref<HTMLInputElement | null>(null)
const presetNames = computed(() => Object.keys(layout.presets))

function toggleMenu() {
  menuOpen.value = !menuOpen.value
}

function onDocDown(event: MouseEvent) {
  const target = event.target as HTMLElement | null
  if (target?.closest?.('.preset-wrap') || target?.closest?.('.preset-menu')) return
  menuOpen.value = false
}

function onDocKey(event: KeyboardEvent) {
  if (event.key === 'Escape') menuOpen.value = false
}

watch(menuOpen, (open) => {
  if (open) {
    document.addEventListener('mousedown', onDocDown)
    document.addEventListener('keydown', onDocKey)
  } else {
    document.removeEventListener('mousedown', onDocDown)
    document.removeEventListener('keydown', onDocKey)
    cancelRename()
  }
})

onMounted(() => {
  void signals.refresh()
  signalTimer = setInterval(() => void signals.refresh(), 30_000)
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocDown)
  document.removeEventListener('keydown', onDocKey)
  if (hintTimer) clearTimeout(hintTimer)
  if (signalTimer) clearInterval(signalTimer)
})

function openSave() {
  menuOpen.value = true
  savedHint.value = ''
  requestAnimationFrame(() => nameInput.value?.focus())
}

function savePreset() {
  const name = newName.value.trim()
  const wb = getWorkbench()
  if (!name || !wb) return
  layout.savePreset(name, wb.captureLayout())
  newName.value = ''
  savedHint.value = `已保存「${name}」。点名称即可切回这个模板。`
  if (hintTimer) clearTimeout(hintTimer)
  hintTimer = setTimeout(() => {
    savedHint.value = ''
    hintTimer = null
  }, 2500)
}

function applyPreset(name: string) {
  menuOpen.value = false
  getWorkbench()?.applyLayout(layout.presets[name])
}

function dropPreset(name: string) {
  layout.removePreset(name)
}

/** 重命名 = 删旧键 + 存新键（预设值原样迁移）。 */
function startRename(name: string) {
  renaming.value = name
  renameText.value = name
}

function cancelRename() {
  renaming.value = ''
  renameText.value = ''
}

function commitRename(name: string) {
  const next = renameText.value.trim()
  if (renaming.value !== name) return
  if (!next || next === name) {
    cancelRename()
    return
  }
  const value = layout.presets[name]
  layout.removePreset(name)
  layout.savePreset(next, value)
  cancelRename()
}

function resetLayout() {
  menuOpen.value = false
  getWorkbench()?.resetToDefault()
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
      <button type="button" class="metric pending" :class="{ hot: pendingCount > 0 }" title="待执行信号，点击聚焦决策台" @click="focusDesk">
        <span class="label">待执行</span>
        <span class="value nt-holo-num">{{ pendingCount }}</span>
      </button>
    </div>

    <div class="actions">
      <KillSwitchBar />
      <NtButton size="sm" title="打开三维监控驾驶舱" @click="router.push('/monitor')">监控</NtButton>
      <NtButton size="sm" title="回到决策台默认排列，已保存的模板还在" @click="resetLayout">恢复盘中</NtButton>
      <NtButton size="sm" title="给当前面板排列起名，存成可切换的模板" @click="openSave">保存布局</NtButton>
      <NtButton size="sm" title="按分类配置选股、风控、情绪和外观" @click="emit('config')">配置</NtButton>
      <NtButton size="sm" title="Ctrl+K" @click="emit('palette')">命令面板</NtButton>
      <div class="preset-wrap">
        <NtButton size="sm" :aria-expanded="menuOpen" @click="toggleMenu">布局预设</NtButton>
        <div v-if="menuOpen" class="preset-menu" role="dialog" aria-label="布局预设">
          <p class="preset-menu__label">已保存</p>
          <p v-if="!presetNames.length" class="preset-menu__empty">还没有保存的预设</p>
          <div v-for="name in presetNames" :key="name" class="preset-menu__row">
            <template v-if="renaming === name">
              <input
                v-model="renameText"
                class="preset-menu__input"
                @keydown.enter="commitRename(name)"
                @keydown.esc="cancelRename"
                @blur="commitRename(name)"
              />
            </template>
            <template v-else>
              <button type="button" class="preset-menu__name" @click="applyPreset(name)">{{ name }}</button>
              <button type="button" class="preset-menu__icon" title="重命名" @click="startRename(name)">✎</button>
              <button type="button" class="preset-menu__icon" title="删除预设" @click="dropPreset(name)">×</button>
            </template>
          </div>
          <p class="preset-menu__empty">拖好面板后起个名字。没起名的当前排列会自动记住；起名后才是可切换的模板。</p>
          <p v-if="savedHint" class="preset-menu__hint">{{ savedHint }}</p>
          <div class="preset-menu__save">
            <input
              ref="nameInput"
              v-model="newName"
              class="preset-menu__input"
              placeholder="模板名称"
              @keydown.enter="savePreset"
            />
            <button type="button" class="preset-menu__savebtn" :disabled="!newName.trim()" @click="savePreset">
              保存为模板
            </button>
          </div>
          <div class="preset-menu__foot">
            <button type="button" @click="resetLayout">重置为默认</button>
            <button type="button" @click="exportPresets">导出</button>
            <button type="button" @click="pickImport">导入</button>
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
  position: relative;
  z-index: 40;
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

/* 「待执行 N」是可点的 metric：平时与权益同款，有待执行时数值进 accent */
button.metric {
  border: none;
  background: none;
  padding: 0;
  font: inherit;
  color: inherit;
  cursor: pointer;
  text-align: left;
}

.metric.pending .value {
  color: var(--nt-text-2);
}

.metric.pending.hot .value {
  color: var(--nt-accent);
}

.metric.pending:hover .label {
  color: var(--nt-accent);
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

.preset-menu {
  position: absolute;
  right: 0;
  left: auto;
  top: calc(100% + 8px);
  width: 280px;
  padding: 10px;
  z-index: 80;
  background: var(--nt-bg-2);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-m);
  box-shadow: 0 16px 40px rgba(0, 0, 0, 0.55);
}

.preset-menu button {
  min-height: 28px;
  height: 28px;
  padding: 0 8px;
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0;
  line-height: 28px;
  color: var(--nt-text-2);
  background: transparent;
  border: none;
  border-radius: var(--nt-radius-s);
  white-space: nowrap;
}

.preset-menu__label,
.preset-menu__empty {
  margin: 0;
  font-size: 12px;
  line-height: 1.4;
}

.preset-menu__label {
  color: var(--nt-text-3);
}

.preset-menu__empty,
.preset-menu__hint {
  margin-top: 4px;
  color: var(--nt-text-2);
}

.preset-menu__hint {
  color: var(--nt-accent);
}

.preset-menu__row,
.preset-menu__save,
.preset-menu__foot {
  display: flex;
  align-items: center;
  gap: 4px;
}

.preset-menu__row {
  margin-top: 4px;
}

.preset-menu__name {
  flex: 1;
  justify-content: flex-start;
  text-align: left;
  color: var(--nt-text-1);
}

.preset-menu__name:hover,
.preset-menu__foot button:hover {
  color: var(--nt-accent);
  background: rgba(0, 229, 255, 0.08);
}

.preset-menu__icon {
  width: 28px;
  padding: 0;
  color: var(--nt-text-3);
}

.preset-menu__icon:hover {
  color: var(--nt-danger);
}

.preset-menu__save {
  margin-top: 8px;
}

.preset-menu__input {
  flex: 1;
  min-width: 0;
  height: 28px;
  padding: 0 8px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-0);
  color: var(--nt-text-1);
  font-size: 12px;
  outline: none;
}

.preset-menu__savebtn {
  color: var(--nt-bg-0);
  background: var(--nt-accent);
}

.preset-menu__savebtn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.preset-menu__foot {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--nt-border-1);
}

.preset-menu__foot button:first-child {
  margin-right: auto;
}
</style>
