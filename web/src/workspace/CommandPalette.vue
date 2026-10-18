<script setup lang="ts">
/**
 * 命令面板：Ctrl+K 面板/预设/命令；Ctrl+Shift+P 直接进命令态（`>` 前缀）。
 * 键盘全通：↑↓ 选择、Enter 执行、Esc 关闭；role=listbox + aria-activedescendant。
 * G2 彭博式助记符：首段大写精确命中面板助记符时置顶动作行——
 * `GP 600519` 开个股行情、`JY buy` 预填买单、`DB` 涨停梯队；
 * ticker 参数输入中列最近代码联想；页脚为聚焦面板的功能卡（描述/用法）。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useLayoutStore } from '@/stores/layout'
import { useRecentStore } from '@/stores/recent'
import { useSettingsStore } from '@/stores/settings'
import { useOverlay } from '@/ui/useOverlay'
import { restartLink } from '@/ws/hub'
import { resolveMnemonic, type MnemonicResolution } from './command-grammar'
import { refreshAll } from './connection-watchdog'
import { getWorkbench } from './inject'
import { getPanel } from './panel-registry'

type PaletteMode = 'panel' | 'command'

interface Item {
  id: string
  label: string
  hint: string
  keywords?: string
  kind: 'panel' | 'command' | 'preset' | 'mnemonic' | 'recent'
  /** 关联面板 id（功能卡页脚取描述用）。 */
  panelId?: string
  /** undefined = 提示行（不可执行，Enter/点击忽略）。 */
  run?: () => void
}

const router = useRouter()
const auth = useAuthStore()
const layout = useLayoutStore()
const settings = useSettingsStore()
const recent = useRecentStore()
const overlay = useOverlay(() => close())
const query = ref('')
const active = ref(0)
const inputEl = ref<HTMLInputElement | null>(null)

const mode = computed<PaletteMode>(() => (query.value.startsWith('>') ? 'command' : 'panel'))
const keyword = computed(() => (mode.value === 'command' ? query.value.slice(1) : query.value).trim())

/** 面板态下的助记符解析（命令态 `>` 不解析助记符）。 */
const mnemonic = computed<MnemonicResolution | null>(() =>
  mode.value === 'panel' ? resolveMnemonic(keyword.value) : null,
)

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

const PINNED_PANELS = ['limit-ladder', 'market', 'signals', 'trade', 'risk-chain']

const allItems = computed<Item[]>(() => {
  const wb = getWorkbench()
  const panels: Item[] = (wb?.panels ?? [])
    .slice()
    .sort((a, b) => {
      const ia = PINNED_PANELS.indexOf(a.id)
      const ib = PINNED_PANELS.indexOf(b.id)
      if (ia === -1 && ib === -1) return 0
      if (ia === -1) return 1
      if (ib === -1) return -1
      return ia - ib
    })
    .map((def) => ({
      id: `panel:${def.id}`,
      label: def.title,
      hint: def.shortcut ? `${def.shortcut.prefix} · 打开面板` : '打开面板',
      keywords: `${def.id} ${def.group} ${def.shortcut?.prefix ?? ''} ${(def.shortcut?.aliases ?? []).join(' ')}`,
      kind: 'panel',
      panelId: def.id,
      run: () => getWorkbench()?.openPanel(def.id),
    }))

  const presets: Item[] = Object.keys(layout.presets).map((name) => ({
    id: `preset:${name}`,
    label: name,
    hint: '套用预设布局',
    kind: 'preset',
    run: () => getWorkbench()?.applyLayout(layout.presets[name]),
  }))

  const commands: Item[] = [
    {
      id: 'cmd:config',
      label: '打开配置',
      hint: '按分类修改参数',
      keywords: 'settings config 设置',
      kind: 'command',
      run: () => window.dispatchEvent(new CustomEvent('nt:open-config')),
    },
    {
      id: 'cmd:refresh',
      label: '刷新账户 / 行情 / 风控',
      hint: '立即拉取',
      keywords: 'reload',
      kind: 'command',
      run: () => void refreshAll(),
    },
    {
      id: 'cmd:monitor',
      label: '打开监控驾驶舱',
      hint: '三维只读',
      keywords: 'monitor 3d 监控',
      kind: 'command',
      run: () => void router.push('/monitor'),
    },
    {
      id: 'cmd:reconnect',
      label: '重连行情通道',
      hint: 'WebSocket 2003',
      keywords: 'ws reconnect',
      kind: 'command',
      run: () => restartLink(),
    },
    {
      id: 'cmd:hotkeys',
      label: '热键：B/S 买卖、Shift+K 急停、Shift+T 交易、Shift+M 涨停、Shift+R 风控、1-9 切面板',
      hint: '参考',
      keywords: 'hotkey shortcut key',
      kind: 'command',
      run: () => getWorkbench()?.openPanel('settings'),
    },
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
    {
      id: 'cmd:reset-layout',
      label: '重置工作台布局',
      hint: '回默认预设',
      keywords: 'layout reset',
      kind: 'command',
      run: () => getWorkbench()?.resetToDefault(),
    },
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

/** 助记符置顶动作行 + 最近代码联想行。 */
const mnemonicItems = computed<Item[]>(() => {
  const m = mnemonic.value
  if (!m) return []
  const { def, shortcut: s, state, ticker, side, arg } = m
  const open = (params?: Record<string, unknown>) => () => getWorkbench()?.openPanel(def.id, params)
  const openTicker = (code: string) => () => {
    recent.pushTicker(code)
    getWorkbench()?.openPanel(def.id, { symbol: code })
  }
  const recentRows = (codes: string[]): Item[] =>
    codes.map((code) => ({
      id: `mn:${def.id}:recent:${code}`,
      label: `${s.prefix} ${code}`,
      hint: '最近使用',
      kind: 'recent',
      panelId: def.id,
      run: openTicker(code),
    }))

  if (s.argKind === 'ticker') {
    if (state === 'valid' && ticker) {
      return [{
        id: `mn:${def.id}:${ticker}`,
        label: `${def.title} · ${ticker}`,
        hint: `${s.prefix} ${ticker}`,
        kind: 'mnemonic',
        panelId: def.id,
        run: openTicker(ticker),
      }]
    }
    if (state === 'partial') {
      return [
        {
          id: `mn:${def.id}:partial`,
          label: `${def.title} · ${arg}…`,
          hint: '继续输满 6 位代码',
          kind: 'mnemonic',
          panelId: def.id,
        },
        ...recentRows(recent.tickers.filter((t) => t.startsWith(arg)).slice(0, 5)),
      ]
    }
    if (state === 'invalid') {
      return [{
        id: `mn:${def.id}:bad`,
        label: def.title,
        hint: `参数无效：代码需为 6 位数字（如 ${s.prefix} ${s.argHint ?? '600519'}）`,
        kind: 'mnemonic',
        panelId: def.id,
      }]
    }
    // empty：无参打开 + 最近代码快捷行
    return [
      {
        id: `mn:${def.id}`,
        label: def.title,
        hint: `${s.description} · 可带代码，如 ${s.prefix} ${s.argHint ?? '600519'}`,
        kind: 'mnemonic',
        panelId: def.id,
        run: open(),
      },
      ...recentRows(recent.tickers.slice(0, 3)),
    ]
  }

  if (s.argKind === 'side') {
    if (state === 'valid' && side) {
      return [{
        id: `mn:${def.id}:${side}`,
        label: `${def.title} · ${side === 'buy' ? '买入预填' : '卖出预填'}`,
        hint: `${s.prefix} ${side}`,
        kind: 'mnemonic',
        panelId: def.id,
        run: open({ side }),
      }]
    }
    if (state === 'invalid') {
      return [{
        id: `mn:${def.id}:bad`,
        label: def.title,
        hint: `参数无效：${s.prefix} buy 或 ${s.prefix} sell`,
        kind: 'mnemonic',
        panelId: def.id,
      }]
    }
    return [{
      id: `mn:${def.id}`,
      label: def.title,
      hint: `${s.description} · ${s.prefix} buy 预填买入 / ${s.prefix} sell 预填卖出`,
      kind: 'mnemonic',
      panelId: def.id,
      run: open(),
    }]
  }

  return [{
    id: `mn:${def.id}`,
    label: def.title,
    hint: s.description,
    kind: 'mnemonic',
    panelId: def.id,
    run: open(),
  }]
})

const results = computed<Item[]>(() => {
  const mnemonicPanelId = mnemonic.value?.def.id ?? null
  let pool = mode.value === 'command' ? allItems.value.filter((i) => i.kind !== 'panel') : allItems.value
  // 助记符命中时其动作行已置顶，去掉同一面板的普通行防双份。
  if (mnemonicPanelId) pool = pool.filter((i) => i.id !== `panel:${mnemonicPanelId}`)
  const term = keyword.value
  const scored = pool
    .map((item) => ({ item, s: score(`${item.label} ${item.keywords ?? ''}`, term) }))
    .filter((row) => row.s >= 0)
    .sort((a, b) => b.s - a.s)
    .map((row) => row.item)
  return [...mnemonicItems.value, ...scored].slice(0, 40)
})

/** 功能卡页脚：聚焦面板行出描述；否则出助记符速查。 */
const footCard = computed(() => {
  const item = results.value[active.value]
  const def = item?.panelId ? getPanel(item.panelId) : undefined
  if (def?.shortcut) {
    const s = def.shortcut
    const argDoc = s.argKind === 'ticker'
      ? `用法：${s.prefix} ${s.argHint ?? '600519'}`
      : s.argKind === 'side'
        ? `用法：${s.prefix} buy ／ ${s.prefix} sell`
        : `用法：${s.prefix}`
    return `${s.prefix} ${def.title} — ${s.description} ${argDoc}`
  }
  return '助记符：GP 600519 开行情 · JY buy 预填买 · DB 涨停梯队 · `>` 命令态'
})

watch(results, () => {
  active.value = 0
})

function run(item: Item | undefined) {
  if (!item?.run) return
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
            placeholder="助记符（如 GP 600519）/ 面板名 / `>` 命令态"
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
            :aria-disabled="!item.run"
            :class="{ on: index === active, dis: !item.run }"
            @mousemove="active = index"
            @click="run(item)"
          >
            <span class="label">{{ item.label }}</span>
            <span class="hint">{{ item.hint }}</span>
          </li>
        </ul>
        <p v-else class="none">没有匹配项</p>
        <div class="foot">{{ footCard }}</div>
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
  background: var(--nt-accent-soft);
}

.row.dis {
  cursor: default;
}

.row.dis .label {
  color: var(--nt-text-2);
}

.label {
  font-size: 13px;
  color: var(--nt-text-1);
}

.hint {
  flex: 0 1 auto;
  min-width: 0;
  font-size: 11.5px;
  color: var(--nt-text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.none {
  margin: 0;
  padding: 16px;
  font-size: 12.5px;
  color: var(--nt-text-3);
}

.foot {
  padding: 8px 14px;
  border-top: 1px solid var(--nt-border-1);
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--nt-text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
