<script setup lang="ts">
/**
 * 通知中心面板（M10）：消息列表 + 级别筛选 + WS alert 推送即时入列。
 * 读取走 /api/notify/messages，实时增量走 alert 主题。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { listMessages, type NotifyMessage } from '@/api/notify'
import NtButton from '@/ui/NtButton.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import { clock, messageOf } from '@/utils/format'
import { onPush, subscribe, type WsPushMessage } from '@/ws/hub'

const items = ref<NotifyMessage[]>([])
const loading = ref(false)
const error = ref('')
const level = ref('')

const levels = computed(() => [...new Set(items.value.map((m) => m.level).filter(Boolean))].sort())

const entries = computed<TimelineEntry[]>(() =>
  items.value
    .filter((m) => !level.value || m.level === level.value)
    .map((m) => ({
      id: m.id,
      time: clock(m.time),
      title: m.title || '（无标题）',
      body: m.body,
      tag: m.kind,
      tone: toneOf(m.level),
    })),
)

function toneOf(lv: string): TimelineEntry['tone'] {
  const key = lv.toLowerCase()
  if (key.includes('crit') || key.includes('danger') || key.includes('fatal')) return 'down'
  if (key.includes('warn') || key.includes('alert')) return 'warn'
  if (key.includes('up') || key.includes('success')) return 'up'
  return 'neutral'
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    items.value = await listMessages({ limit: 100 })
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'alert') return
  const d = (msg.data ?? {}) as Record<string, unknown>
  const title = String(d.title || d.subject || '')
  if (!title) return
  const entry: NotifyMessage = {
    id: `ws-${msg.seq ?? Date.now()}`,
    kind: String(d.kind || d.type || 'alert'),
    level: String(d.level || d.severity || ''),
    title,
    body: String(d.body || d.message || ''),
    time: new Date().toISOString(),
    read: false,
  }
  items.value = [entry, ...items.value].slice(0, 200)
}

let offPush: (() => void) | undefined
let unsubscribe: (() => void) | undefined

onMounted(() => {
  void load()
  unsubscribe = subscribe('alert')
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
  offPush?.()
  unsubscribe?.()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">通知中心</h1>
        <p class="nt-note">{{ items.length }} 条消息 · WS alert 实时入列</p>
      </div>
      <div class="filters">
        <select v-model="level" class="level">
          <option value="">全部级别</option>
          <option v-for="lv in levels" :key="lv" :value="lv">{{ lv }}</option>
        </select>
        <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
      </div>
    </header>

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <NtTimeline :items="entries" :empty="loading ? '加载中…' : '还没有消息'" />
    </section>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 8px;
  align-items: center;
}

.level {
  padding: 4px 8px;
  font-size: 12.5px;
  color: var(--nt-text-1);
  background: var(--nt-bg-2);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}
</style>
