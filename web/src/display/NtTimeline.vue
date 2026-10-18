<script setup lang="ts">
export interface TimelineEntry {
  id: string | number
  time: string
  title: string
  body?: string
  tone?: 'neutral' | 'up' | 'down' | 'accent' | 'warn'
  tag?: string
  /** G3 逐条已读态：仅显式传 false 时按未读渲染（加粗+全长明度），缺省视为已读。 */
  read?: boolean
}

withDefaults(defineProps<{ items: TimelineEntry[]; empty?: string; selectable?: boolean }>(), { empty: '', selectable: false })
const emit = defineEmits<{ (e: 'select', item: TimelineEntry): void }>()
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
      <div class="nt-tl__body">
        <div class="nt-tl__meta">
          <time class="nt-tl__time">{{ item.time }}</time>
          <span v-if="item.tag" class="nt-tl__tag">{{ item.tag }}</span>
          <span v-if="item.read === false" class="nt-tl__unread">未读</span>
        </div>
        <p class="nt-tl__title">{{ item.title }}</p>
        <p v-if="item.body" class="nt-tl__text">{{ item.body }}</p>
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
</style>
