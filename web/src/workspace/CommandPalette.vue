<script setup lang="ts">
/**
 * 命令面板：Ctrl+K 面板/预设/命令；Ctrl+Shift+P 直接进命令态（`>` 前缀）。
 * 键盘全通：↑↓ 选择、Enter 执行、Esc 关闭；role=listbox + aria-activedescendant。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useLayoutStore } from '@/stores/layout'
import { useSettingsStore } from '@/stores/settings'
import { useOverlay } from '@/ui/useOverlay'
import { restartLink } from '@/ws/hub'
import { refreshAll } from './connection-watchdog'
import { useWorkbench } from './inject'

type PaletteMode = 'panel' | 'command'

interface Item {
  id: string
  label: string
  hint: string
  keywords?: string
  kind: 'panel' | 'command' | 'preset'
  run: () => void
}

const router = useRouter()
const auth = useAuthStore()
const layout = useLayoutStore()
const settings = useSettingsStore()
const workbench = useWorkbench()

const overlay = useOverlay(() => close())
const query = ref('')
const active = ref(0)
const inputEl = ref<HTMLInputElement | null>(null)

const mode = computed<PaletteMode>(() => (query.value.startsWith('>') ? 'command' : 'panel'))
const keyword = computed(() => (mode.value === 'command' ? query.value.slice(1) : query.value).trim())

/** 极简打分：完全等 > 前缀 > 子串（越靠前越高）> 子序列；-1 表示不匹配。 */
function score(target: string, term: string): number {
  if (!term) return 40
  const t = target.toLowerCase()
  const q = term.toLowerCase()
  if (t === q) return 100
  if (t.startsWith(q)) return 80
  const at = t.indexOf(q)
  if (at >= 0) return 60 - Math.min(at, 20)
  let i = 0
  for (const ch of t) {
    if (ch === q[i]) i += 1
    if (i === q.length) return 20
  }
  return -1
}

const allItems = computed<Item[]>(() => {
  const panels: Item[] = (workbench?.panels ?? []).map((def) => ({
    id: `panel:${def.id}`,
    label: def.title,
    hint: '打开面板',
    keywords: `${def.id} ${def.group}`,
    kind: 'panel',
    run: () => workbench?.openPanel(def.id),
  }))

  const presets: Item[] = Object.keys(layout.presets).map((name) => ({
    id: `preset:${name}`,
    label: name,
    hint: '套用预设布局',
    kind: 'preset',
    run: () => workbench?.applyLayout(layout.presets[name]),
  }))

  const commands: Item[] = [
    { id: 'cmd:refresh', label: '刷新账户 / 行情 / 风控', hint: '立即拉取', keywords: 'reload', kind: 'command', run: () => void refreshAll() },
    { id: 'cmd:reconnect', label: '重连行情通道', hint: 'WebSocket 2003', keywords: 'ws reconnect', kind: 'command', run: () => restartLink() },
    {
      id: 'cmd:theme',
      label: settings.theme === 'dark' ? '切换为亮色主题' : '切换为暗色主题',
      hint: '外观',
      keywords: 'theme light dark',
      kind: 'command',
      run: () => settings.setTheme(settings.theme === 'dark' ? 'light' : 'dark'),
    },
    {
      id: 'cmd:density',
      label: settings.density === 'compact' ? '切换为标准密度' : '切换为紧凑密度',
      hint: '外观',
      keywords: 'density compact',
      kind: 'command',
      run: () => settings.setDensity(settings.density === 'compact' ? 'normal' : 'compact'),
    },
    { id: 'cmd:reset-layout', label: '重置工作台布局', hint: '回默认预设', keywords: 'layout reset', kind: 'command', run: () => workbench?.resetToDefault() },
    {
      id: 'cmd:logout',
      label: '退出登录',
      hint: auth.operatorName,
      keywords: 'logout signout',
      kind: 'command',
      run: () => {
        void auth.clear()
        void router.push({ name: 'login' })
      },
    },
  ]

  return [...panels, ...presets, ...commands]
})

const results = computed<Item[]>(() => {
  const pool = mode.value === 'command' ? allItems.value.filter((i) => i.kind !== 'panel') : allItems.value
  const term = keyword.value
  return pool
    .map((item) => ({ item, s: score(`${item.label} ${item.keywords ?? ''}`, term) }))
    .filter((row) => row.s >= 0)
    .sort((a, b) => b.s - a.s)
    .slice(0, 40)
    .map((row) => row.item)
})

watch(results, () => {
  active.value = 0
})

function run(item: Item | undefined) {
  if (!item) return
  close()
  item.run()
}

function onKey(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    active.value = results.value.length ? (active.value + 1) % results.value.length : 0
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    active.value = results.value.length ? (active.value - 1 + results.value.length) % results.value.length : 0
  } else if (event.key === 'Enter') {
    event.preventDefault()
    run(results.value[active.value])
  }
}

function open(source: PaletteMode = 'panel') {
  query.value = source === 'command' ? '>' : ''
  active.value = 0
  overlay.open()
  void nextTick(() => inputEl.value?.focus())
}

function close() {
  overlay.close()
  query.value = ''
}

defineExpose({ open, close })
</script>

<template>
  <Teleport to="body">
    <div v-if="overlay.visible.value" class="mask" :style="{ zIndex: overlay.zIndex.value }" @click.self="close">
      <div class="sheet" role="dialog" aria-modal="true" aria-label="命令面板">
        <div class="bar">
          <span class="badge">{{ mode === 'command' ? '命令' : '面板' }}</span>
          <input
            ref="inputEl"
            v-model="query"
            class="input"
            role="combobox"
            aria-expanded="true"
            aria-controls="nt-palette-list"
            :aria-activedescendant="results.length ? `nt-cmd-${active}` : undefined"
            placeholder="输入面板名 / 命令，`>` 前缀进入命令态"
            @keydown="onKey"
          />
        </div>
        <ul v-if="results.length" id="nt-palette-list" class="list" role="listbox">
          <li
            v-for="(item, index) in results"
            :id="`nt-cmd-${index}`"
            :key="item.id"
            class="row"
            role="option"
            :aria-selected="index === active"
            :class="{ on: index === active }"
            @mousemove="active = index"
            @click="run(item)"
          >
            <span class="label">{{ item.label }}</span>
            <span class="hint">{{ item.hint }}</span>
          </li>
        </ul>
        <p v-else class="none">没有匹配项</p>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.mask {
  position: fixed;
  inset: 0;
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 12vh 16px 16px;
  background: rgba(4, 6, 12, 0.5);
  backdrop-filter: blur(3px);
}

.sheet {
  width: min(600px, 100%);
  max-height: 62vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--nt-bg-glass);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-l);
  box-shadow: 0 18px 60px rgba(0, 0, 0, 0.55);
}

.bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--nt-border-1);
}

.badge {
  flex: none;
  padding: 2px 7px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  font-size: 11px;
  color: var(--nt-text-2);
}

.input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  background: transparent;
  color: var(--nt-text-1);
  font-size: 14px;
  font-family: var(--nt-font-sans);
}

.input::placeholder {
  color: var(--nt-text-3);
}

.list {
  margin: 0;
  padding: 6px;
  list-style: none;
  overflow: auto;
}

.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 10px;
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.row.on {
  background: rgba(0, 229, 255, 0.1);
}

.label {
  font-size: 13px;
  color: var(--nt-text-1);
}

.hint {
  flex: none;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.none {
  margin: 0;
  padding: 16px;
  font-size: 12.5px;
  color: var(--nt-text-3);
}
</style>
