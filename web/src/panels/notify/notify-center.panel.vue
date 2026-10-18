<script setup lang="ts">
/**
 * 通知中心面板（M10）：消息列表 + 级别/类别筛选 + WS alert 推送即时入列。
 * G3：逐条已读态（点条目标记，localStorage 持久化）+ 全部已读；
 * 打开面板仍清零 StatusBar 角标（见 stores/notify.ts），两者语义独立。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { listMessages, type NotifyMessage } from '@/api/notify'
import { useNotifyStore } from '@/stores/notify'
import NtButton from '@/ui/NtButton.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import { clock, messageOf } from '@/utils/format'
import { capNotifReadIds, loadNotifReadIds, saveNotifReadIds } from '@/utils/notify-read'
import { onPush, type WsPushMessage } from '@/ws/hub'

const notify = useNotifyStore()

const items = ref<NotifyMessage[]>([])
const loading = ref(false)
const error = ref('')
const level = ref('')
const kind = ref('')
const readIds = ref<string[]>(loadNotifReadIds())

const readSet = computed(() => new Set(readIds.value))

const levels = computed(() => [...new Set(items.value.map((m) => m.level).filter(Boolean))].sort())
const levelOptions = computed(() => [
  { value: '', label: '全部级别' },
  ...levels.value.map((lv) => ({ value: lv, label: lv })),
])
const kinds = computed(() => [...new Set(items.value.map((m) => m.kind).filter(Boolean))].sort())
const kindOptions = computed(() => [
  { value: '', label: '全部类别' },
  ...kinds.value.map((k) => ({ value: k, label: k })),
])

function isRead(m: NotifyMessage): boolean {
  return m.read || readSet.value.has(m.id)
}

const unreadInList = computed(() => items.value.filter((m) => !isRead(m)).length)

const entries = computed<TimelineEntry[]>(() =>
  items.value
    .filter((m) => (!level.value || m.level === level.value) && (!kind.value || m.kind === kind.value))
    .map((m) => ({
      id: m.id,
      time: clock(m.time),
      title: m.title || '（无标题）',
      body: m.body,
      tag: m.kind,
      tone: toneOf(m.level),
      read: isRead(m),
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
  // 面板开着等于在看，推送到了就顺手清零角标；条目自身仍按未读呈列，点开才算读过。
  notify.markRead()
}

function markOneRead(item: TimelineEntry) {
  const id = String(item.id)
  if (readSet.value.has(id)) return
  readIds.value = capNotifReadIds([id, ...readIds.value])
  saveNotifReadIds(readIds.value)
}

function markAllRead() {
  readIds.value = capNotifReadIds([...items.value.map((m) => m.id), ...readIds.value])
  saveNotifReadIds(readIds.value)
  notify.markRead()
}

// alert 是 BASE_TOPIC，hub 常驻订阅，onPush 即可收到，无需再 subscribe。
let offPush: (() => void) | undefined

onMounted(() => {
  void load()
  notify.start()
  notify.markRead()
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
  offPush?.()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">通知中心</h1>
        <p class="nt-note">{{ items.length }} 条消息 · 未读 {{ unreadInList }} · 点条目标记已读</p>
      </div>
      <div class="filters">
        <NtSelect v-model="level" :options="levelOptions" />
        <NtSelect v-model="kind" :options="kindOptions" />
        <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
        <NtButton size="sm" :disabled="unreadInList === 0" @click="markAllRead">全部已读</NtButton>
      </div>
    </header>

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <NtTimeline :items="entries" :empty="loading ? '加载中…' : '还没有消息'" selectable @select="markOneRead" />
    </section>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 8px;
  align-items: center;
}
</style>

