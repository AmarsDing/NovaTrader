<script setup lang="ts">
/**
 * IntelRow（W0-3b / FE-I4）：消息行单一事实组件，三处复用——
 *   IntelView 消息流（最全：来源/重要度/kind/原文外链/高亮）、
 *   MarketView 证券消息侧栏（行点击跳情报面板）、
 *   OverviewView 焦点「该股的情报」。
 * 此前三处各裁口径（MarketView 丢来源与弱标、OverviewView 只剩 时间/标题/来源），
 * 本组件收口：时点/来源/重要度（强章+弱标）/kind/原文外链/标题为一份渲染合同，
 * 消费面只喂 IntelItem。「没取到≠空」：时点缺失显「—」，重要度弱标只在真下发时出。
 */
import { type IntelItem } from '@/api/intel'
import NtBadge from '@/ui/NtBadge.vue'
import { clock, pnlClass } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    item: IntelItem
    /** 行可点（MarketView 点消息跳情报面板）；不可点时仅原文按钮可交互。 */
    clickable?: boolean
    /** 面板参数带入的高亮消息（IntelView highlight）：边线标记。 */
    hit?: boolean
  }>(),
  { clickable: false, hit: false },
)

const emit = defineEmits<{ (e: 'open', item: IntelItem): void }>()

/** 公告/研报原文外链：与 F10HoverCard/F10Drawer 同款 window.open noopener 通道。 */
function openUrl(url: string) {
  window.open(url, '_blank', 'noopener')
}

function onOpen() {
  if (props.clickable) emit('open', props.item)
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ' ') onOpen()
}
</script>

<template>
  <li
    class="intel-row"
    :class="{ hit, clickable }"
    :tabindex="clickable ? 0 : -1"
    @click="onOpen"
    @keydown="clickable ? onKey($event) : undefined"
  >
    <div class="head">
      <b :class="pnlClass(item.sentiment)">{{ clock(item.time) || '—' }} {{ item.source }}</b>
      <NtBadge v-if="(item.importance ?? 0) >= 4" tone="warn">重要</NtBadge>
      <span v-else-if="item.importance != null" class="imp">Ⅰ{{ item.importance }}</span>
      <span v-if="item.kind" class="kind">{{ item.kind }}</span>
      <!-- A9 原文外链（proto url 已透传）：全三处同源出位 -->
      <button v-if="item.url" type="button" class="ext" @click.stop="openUrl(item.url)">原文</button>
    </div>
    <span class="title">{{ item.title }}</span>
  </li>
</template>

<style scoped>
.intel-row {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12.5px;
}

.intel-row.hit {
  border-left: 2px solid var(--nt-accent);
  padding-left: 8px;
}

.intel-row.clickable {
  cursor: pointer;
}

.intel-row.clickable:focus-visible {
  outline: 2px solid var(--nt-accent);
  outline-offset: 2px;
}

.intel-row b {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.head {
  display: flex;
  gap: 8px;
  align-items: baseline;
}

.title {
  overflow-wrap: anywhere;
}

.kind {
  font-size: 11px;
  color: var(--nt-text-3);
}

.imp {
  font-size: 11px;
  font-family: var(--nt-font-mono);
  color: var(--nt-text-3);
}

.ext {
  margin-left: auto;
  padding: 0;
  border: none;
  background: none;
  font: inherit;
  font-size: 11px;
  color: var(--nt-accent);
  cursor: pointer;
}

.ext:hover {
  text-decoration: underline;
}
</style>
