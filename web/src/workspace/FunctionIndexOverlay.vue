<script setup lang="ts">
/**
 * G2 F1 独立帮助浮层：彭博式「当前功能卡 + 全目录 + 全局热键」。
 * - 打开瞬间按聚焦面板定格当前卡（截 later 切换不追踪）；无卡面板明示，不杜撰；
 * - 目录行点击 = 打开该面板并关浮层（与助记符同一条 openPanel 通道）；
 * - Esc 走 useOverlay 栈（嵌在配置层之上时也先关自己）。
 */
import { computed, nextTick, ref } from 'vue'
import { useOverlay } from '@/ui/useOverlay'
import { getWorkbench } from './inject'
import { listPanels } from './panel-registry'
import { cardOf, HOTKEY_ROWS, usageOf } from './function-index'
import { functionIndex, type FunctionCard } from './command-grammar'
import NtInput from '@/ui/NtInput.vue'

const overlay = useOverlay(() => overlay.close())

/** 当前卡快照：只在 open 时取一次（聚焦面板可能随后切换，卡随操作定格）。 */
const currentCard = ref<FunctionCard | null>(null)
const filter = ref('')
const dialog = ref<HTMLElement | null>(null)

function open() {
  currentCard.value = cardOf(getWorkbench()?.activePanelId())
  filter.value = ''
  overlay.open()
  // 与同栈 NtModal/CommandPalette 同纪律：打开即把焦点送入对话框（直达过滤框）。
  void nextTick(() => dialog.value?.querySelector('input')?.focus())
}
const visible = overlay.visible

const cards = computed(() => functionIndex())

const filtered = computed<FunctionCard[]>(() => {
  const kw = filter.value.trim().toUpperCase()
  if (!kw) return cards.value
  return cards.value.filter(
    (c) =>
      c.prefix.includes(kw) ||
      c.title.includes(kw) ||
      c.aliases.some((a) => a.toUpperCase().includes(kw)),
  )
})

function openPanelAndClose(card: FunctionCard) {
  getWorkbench()?.openPanel(card.id)
  overlay.close()
}

// 面板数量行放回顶层呈现（含未挂助记符面板名，目录不影响其可达性）。
const panelCount = computed(() => listPanels().length)

defineExpose({ open })
</script>

<template>
  <div v-if="visible" class="help-mask" :style="{ zIndex: overlay.zIndex.value }" @mousedown.self="overlay.close()">
    <div ref="dialog" class="help" role="dialog" aria-modal="true" aria-label="功能卡索引">
      <header class="help__head">
        <h2 class="nt-title">功能卡索引</h2>
        <span class="help__meta">{{ panelCount }} 面板 · F1 随聚焦面板定格 · Esc 关闭</span>
      </header>

      <section v-if="currentCard" class="help__current">
        <p class="help__label">当前面板</p>
        <div class="card">
          <div class="card__titleline">
            <span class="prefix">{{ currentCard.prefix }}</span>
            <span v-for="alias in currentCard.aliases" :key="alias" class="prefix alias">{{ alias }}</span>
            <b class="card__title">{{ currentCard.title }}</b>
            <span class="usage">{{ usageOf(currentCard) }}</span>
          </div>
          <p class="card__desc">{{ currentCard.description || '（注册表未填写描述）' }}</p>
        </div>
      </section>

      <section v-else class="help__current">
        <p class="help__label">当前面板</p>
        <p class="help__nocard">本面板未挂助记符，从下方目录开启任意功能。</p>
      </section>

      <section class="help__hotkeys">
        <p class="help__label">全局热键</p>
        <ul class="hotkeys">
          <li v-for="row in HOTKEY_ROWS" :key="row.keys">
            <span class="keys">{{ row.keys }}</span>
            <span class="action">{{ row.action }}</span>
          </li>
        </ul>
      </section>

      <section class="help__index">
        <p class="help__label">功能目录</p>
        <NtInput v-model="filter" placeholder="按助记符 / 标题过滤" type="search" />
        <ul class="index">
          <li v-for="card in filtered" :key="card.id">
            <button type="button" class="index__row" @click="openPanelAndClose(card)">
              <span class="prefix">{{ card.prefix }}</span>
              <span class="index__title">{{ card.title }}</span>
              <span class="index__usage">{{ usageOf(card) }}</span>
              <span class="index__desc">{{ card.description }}</span>
            </button>
          </li>
          <li v-if="!filtered.length" class="index__empty">无匹配助记符</li>
        </ul>
      </section>
    </div>
  </div>
</template>

<style scoped>
.help-mask {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: var(--nt-scrim);
}

.help {
  width: min(680px, 92vw);
  max-height: 82vh;
  overflow: auto;
  padding: 14px 16px 16px;
  border: 1px solid var(--nt-glass-border);
  border-radius: var(--nt-radius-m);
  background: var(--nt-glass-sheen), var(--nt-glass-bg);
  backdrop-filter: var(--nt-glass-filter);
  box-shadow: var(--nt-edge-light), var(--nt-shadow-depth), var(--nt-glow-soft);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.help__nocard {
  margin: 0;
  padding: 10px 12px;
  border: 1px dashed var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  color: var(--nt-text-3);
  font-size: 12.5px;
}

.help__head {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.help__meta {
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.help__label {
  margin: 0 0 6px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--nt-text-3);
}

.card {
  padding: 10px 12px;
  border: 1px solid var(--nt-accent-border);
  border-radius: var(--nt-radius-s);
  background: var(--nt-accent-soft);
}

.card__titleline {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}

.prefix {
  font-family: var(--nt-font-mono);
  font-size: 12px;
  font-weight: 700;
  color: var(--nt-accent);
  padding: 0 6px;
  border: 1px solid var(--nt-accent-border);
  border-radius: var(--nt-radius-s);
}

.prefix.alias {
  color: var(--nt-text-2);
  border-color: var(--nt-border-1);
}

.card__title {
  font-size: 13px;
  color: var(--nt-text-1);
}

.usage {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-size: 11.5px;
  color: var(--nt-text-2);
}

.card__desc {
  margin: 6px 0 0;
  font-size: 12.5px;
  color: var(--nt-text-2);
  line-height: 1.6;
}

.hotkeys {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px 14px;
}

.hotkeys li {
  display: flex;
  gap: 8px;
  align-items: baseline;
  font-size: 11.5px;
}

.hotkeys .keys {
  flex: none;
  min-width: 118px;
  font-family: var(--nt-font-mono);
  color: var(--nt-accent);
}

.hotkeys .action {
  color: var(--nt-text-2);
}

.index {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  max-height: 34vh;
  overflow: auto;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}

.index__row {
  display: grid;
  grid-template-columns: 64px 92px 150px 1fr;
  gap: 8px;
  align-items: baseline;
  width: 100%;
  padding: 6px 10px;
  border: none;
  border-bottom: 1px solid var(--nt-border-1);
  background: none;
  font: inherit;
  font-size: 12px;
  text-align: left;
  cursor: pointer;
}

.index li:last-child .index__row {
  border-bottom: none;
}

.index__row:hover {
  background: var(--nt-accent-soft);
}

.index__title {
  color: var(--nt-text-1);
}

.index__usage {
  font-family: var(--nt-font-mono);
  font-size: 11.5px;
  color: var(--nt-text-2);
}

.index__desc {
  color: var(--nt-text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.index__empty {
  padding: 10px;
  font-size: 12px;
  color: var(--nt-text-3);
}
</style>
