<script setup lang="ts">
import NtBadge, { type BadgeTone } from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import type { AlertLevel } from '@/ui/alert-levels'

/** 单条建议动作：本组件只上抛不跳转，由宿面板白名单裁决（panel + symbol）。 */
export interface TimelineAction {
  id: string
  label: string
  symbol?: string
  panel?: string
}

export interface TimelineEntry {
  id: string | number
  time: string
  title: string
  body?: string
  tone?: 'neutral' | 'up' | 'down' | 'accent' | 'warn'
  tag?: string
  /** G3 逐条已读态：仅显式传 false 时按未读渲染（加粗+全长明度），缺省视为已读。 */
  read?: boolean
  /** P2 告警分级章（方案 §8）：L1 提示/L2 关注/L3 预警/L4 风险；缺省不渲染。 */
  level?: AlertLevel
  /** P2 单条建议动作：条目内 ghost 小按钮行，点击 emit('action')；缺省不渲染。 */
  actions?: TimelineAction[]
  /** 条目角落注记（次要色小字，如「静默」）；缺省不渲染。 */
  note?: string
}

/** 分级章文案与色调（§8.1 一字锁定）：L1 neutral 提示 / L2 info 关注 / L3 warn 预警 / L4 danger 风险。 */
const LEVEL_BADGE: Record<AlertLevel, { tone: BadgeTone; text: string }> = {
  L1: { tone: 'neutral', text: '提示' },
  L2: { tone: 'info', text: '关注' },
  L3: { tone: 'warn', text: '预警' },
  L4: { tone: 'danger', text: '风险' },
}

withDefaults(defineProps<{ items: TimelineEntry[]; empty?: string; selectable?: boolean }>(), { empty: '', selectable: false })
const emit = defineEmits<{
  (e: 'select', item: TimelineEntry): void
  /** 单条动作点击：entry 与 action 原样上抛，宿面板走白名单 openPanel。 */
  (e: 'action', item: TimelineEntry, action: TimelineAction): void
}>()
</script>

<template>
  <ul class="nt-tl">
    <li v-if="!items.length" class="nt-tl__empty">{{ empty || '暂无记录' }}</li>
    <li
      v-for="item in items"
      :key="String(item.id)"
      class="nt-tl__item"
      :class="{ read: item.read !== false, clickable: selectable }"
      @click="selectable && emit('select', item)"
    >
      <span class="nt-tl__dot" :class="item.tone || 'neutral'" />
      <span v-if="item.note" class="nt-tl__note">{{ item.note }}</span>
      <div class="nt-tl__body">
        <div class="nt-tl__meta">
          <time class="nt-tl__time">{{ item.time }}</time>
          <NtBadge v-if="item.level" :tone="LEVEL_BADGE[item.level].tone">{{ LEVEL_BADGE[item.level].text }}</NtBadge>
          <span v-if="item.tag" class="nt-tl__tag">{{ item.tag }}</span>
          <span v-if="item.read === false" class="nt-tl__unread">未读</span>
        </div>
        <p class="nt-tl__title">{{ item.title }}</p>
        <p v-if="item.body" class="nt-tl__text">{{ item.body }}</p>
        <div v-if="item.actions?.length" class="nt-tl__actions" @click.stop>
          <NtButton v-for="action in item.actions" :key="action.id" size="sm" @click="emit('action', item, action)">
            {{ action.label }}
          </NtButton>
        </div>
      </div>
    </li>
  </ul>
</template>

<style scoped>
.nt-tl {
  list-style: none;
  margin: 0;
  padding: 0;
}

.nt-tl__item {
  position: relative;
  display: flex;
  gap: 10px;
  padding: 8px 0 8px 14px;
  border-left: 1px solid var(--nt-border-1);
}

.nt-tl__item.read {
  opacity: 0.72;
}

.nt-tl__item.read .nt-tl__title {
  font-weight: 400;
}

.nt-tl__item.clickable {
  cursor: pointer;
}

.nt-tl__item.clickable:hover {
  background: var(--nt-accent-soft);
}

.nt-tl__dot {
  position: absolute;
  left: -4px;
  top: 13px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--nt-text-3);
}

.nt-tl__dot.up {
  background: var(--nt-up);
}

.nt-tl__dot.down {
  background: var(--nt-down);
}

.nt-tl__dot.accent {
  background: var(--nt-accent);
}

.nt-tl__dot.warn {
  background: var(--nt-warn);
}

.nt-tl__meta {
  display: flex;
  gap: 8px;
  align-items: center;
}

.nt-tl__time {
  font-family: var(--nt-font-mono);
  font-size: 11px;
  color: var(--nt-text-3);
}

.nt-tl__tag {
  font-size: 10.5px;
  color: var(--nt-accent);
}

.nt-tl__unread {
  font-size: 10.5px;
  line-height: 1.5;
  padding: 0 5px;
  color: var(--nt-accent);
  border: 1px solid var(--nt-accent);
  border-radius: var(--nt-radius-s);
}

.nt-tl__title {
  margin: 3px 0 0;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-tl__text {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--nt-text-2);
}

.nt-tl__empty {
  padding: 12px 0;
  font-size: 12px;
  color: var(--nt-text-3);
}

/* P2 append-only：分级章/角落注记/单条动作行。无 level/note/actions 时不渲染，布局零变化。 */
.nt-tl__note {
  position: absolute;
  top: 8px;
  right: 0;
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}

.nt-tl__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 6px;
}
</style>
