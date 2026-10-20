<script setup lang="ts">
/**
 * 通知中心面板（M10）：消息列表 + 级别/类别筛选 + WS alert 推送即时入列。
 * G3：逐条已读态（点条目标记，localStorage 持久化）+ 全部已读；
 * 打开面板仍清零 StatusBar 角标（见 stores/notify.ts），两者语义独立。
 * P2 承接层（方案 §8.6）：stores/notify 的 feed（告警漏斗落账）与 listMessages 合并去重，
 * feed 实时条目在前；每条归一分级章（feed 用其 level，api 用 levelOf 推导）、
 * L1–L4「级别」过滤与既有级别/类别可叠加、单条动作白名单 openPanel、静默条目角落注记。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { listMessages, type NotifyMessage } from '@/api/notify'
import NtTimeline, { type TimelineAction, type TimelineEntry } from '@/display/NtTimeline.vue'
import { useNotifyStore, type FeedEntry } from '@/stores/notify'
import { causeKey, levelOf, renderAlertCopy, type AlertLevel } from '@/ui/alert-levels'
import NtButton from '@/ui/NtButton.vue'
import NtSelect from '@/ui/NtSelect.vue'
import { clock, messageOf } from '@/utils/format'
import { capNotifReadIds, loadNotifReadIds, saveNotifReadIds } from '@/utils/notify-read'
import { getWorkbench } from '@/workspace/inject'
import { onPush, type WsPushMessage } from '@/ws/hub'

const notify = useNotifyStore()

const items = ref<NotifyMessage[]>([])
const loading = ref(false)
const error = ref('')
const level = ref('')
const grade = ref('')
const kind = ref('')
const readIds = ref<string[]>(loadNotifReadIds())

const readSet = computed(() => new Set(readIds.value))

/** 列表行模型：feed 实时条与 api 后端消息归一成同一张表。 */
interface CenterRow {
  id: string
  /** 归一分级 L1–L4：feed 条直接用其 level；api 条 levelOf({kind, level, category, ...}) 推导。 */
  level: AlertLevel
  /** api 原生级别文本（feed 条无此概念，空串）；既有「级别」下拉（原生级别）吃它。 */
  rawLevel: string
  kind: string
  symbol?: string
  title: string
  body?: string
  time: string
  read: boolean
  silenced?: boolean
  causeKey?: string
}

/** feed 行 id 前缀：与后端消息 id 命名空间隔离；落账条无后端已读态，readIds 持久化跳过它。 */
const FEED_ID_PREFIX = 'feed-'

/* 「级别」下拉（§8.1 归一分级）：与既有原生级别/类别过滤可叠加。 */
const gradeOptions = [
  { value: '', label: '全部' },
  { value: 'L1', label: 'L1 提示' },
  { value: 'L2', label: 'L2 关注' },
  { value: 'L3', label: 'L3 预警' },
  { value: 'L4', label: 'L4 风险' },
]

function isRead(m: NotifyMessage): boolean {
  return m.read || readSet.value.has(m.id)
}

function feedRow(f: FeedEntry): CenterRow {
  return {
    id: `${FEED_ID_PREFIX}${f.id}`,
    level: f.level,
    rawLevel: '',
    kind: f.kind || '',
    symbol: f.symbol,
    title: f.title || '（无标题）',
    body: f.body,
    time: clock(f.at),
    /* 落账条非收件箱消息：不做未读加粗/不计未读数，点读也不进 readIds。 */
    read: true,
    silenced: f.silenced,
    causeKey: f.causeKey,
  }
}

function apiRow(m: NotifyMessage): CenterRow {
  /* NotifyMessage 索引签名兜底的扩展字段（WS 即时条带 symbol/category/causeKey，REST 标准化条没有）。 */
  const extra = m as Record<string, unknown>
  const symbol = typeof extra.symbol === 'string' && extra.symbol.trim() ? extra.symbol.trim() : undefined
  const category = typeof extra.category === 'string' ? extra.category : undefined
  const cause = typeof extra.causeKey === 'string' && extra.causeKey ? extra.causeKey : undefined
  return {
    id: m.id,
    level: levelOf({ kind: m.kind, level: m.level, category, symbol }),
    rawLevel: m.level,
    kind: m.kind,
    symbol,
    title: m.title || '（无标题）',
    body: m.body,
    time: clock(m.time),
    read: isRead(m),
    causeKey: cause,
  }
}

/* 合并去重（§8.6）：feed 实时条在前；api 行先按 id 去重，再按 dedupKey/causeKey 撞 feed 时归 feed；上限 200。 */
const merged = computed<CenterRow[]>(() => {
  const feedRows = notify.feed.map(feedRow)
  const feedCauses = new Set(feedRows.map((r) => r.causeKey).filter((c): c is string => !!c))
  const apiRows: CenterRow[] = []
  const seenIds = new Set<string>()
  for (const m of items.value) {
    if (!m.id || seenIds.has(m.id)) continue
    seenIds.add(m.id)
    const row = apiRow(m)
    if (row.causeKey && feedCauses.has(row.causeKey)) continue
    apiRows.push(row)
  }
  return [...feedRows, ...apiRows].slice(0, 200)
})

const levels = computed(() => [...new Set(merged.value.map((r) => r.rawLevel).filter(Boolean))].sort())
const levelOptions = computed(() => [
  { value: '', label: '全部级别' },
  ...levels.value.map((lv) => ({ value: lv, label: lv })),
])
const kinds = computed(() => [...new Set(merged.value.map((r) => r.kind).filter(Boolean))].sort())
const kindOptions = computed(() => [
  { value: '', label: '全部类别' },
  ...kinds.value.map((k) => ({ value: k, label: k })),
])

const filtered = computed(() =>
  merged.value.filter(
    (r) =>
      (!level.value || r.rawLevel === level.value) &&
      (!grade.value || r.level === grade.value) &&
      (!kind.value || r.kind === kind.value),
  ),
)

const unreadInList = computed(() => merged.value.filter((r) => !r.read).length)

const entries = computed<TimelineEntry[]>(() =>
  filtered.value.map((r) => ({
    id: r.id,
    time: r.time,
    title: r.title,
    body: r.body,
    tag: r.kind,
    tone: toneOfRow(r),
    read: r.read,
    level: r.level,
    actions: actionsOf(r),
    note: r.silenced ? '静默' : undefined,
  })),
)

function toneOfRow(r: CenterRow): TimelineEntry['tone'] {
  const key = r.rawLevel.toLowerCase()
  if (key.includes('crit') || key.includes('danger') || key.includes('fatal')) return 'down'
  if (key.includes('warn') || key.includes('alert')) return 'warn'
  if (key.includes('up') || key.includes('success')) return 'up'
  /* 无原生级别文本（feed 条/缺级 WS 条）：按归一分级落点色，L4 红、L3/L2 琥珀、L1 中性。 */
  if (r.level === 'L4') return 'down'
  if (r.level === 'L3' || r.level === 'L2') return 'warn'
  return 'neutral'
}

function actionsOf(r: CenterRow): TimelineAction[] | undefined {
  /* 动作白名单与五段式文案同册（§8.2 renderAlertCopy）：带票→看行情、规则命中→看规则；其余不出钮。 */
  const actions = renderAlertCopy({ kind: r.kind || undefined, symbol: r.symbol }).actions
  return actions.length ? actions : undefined
}

function onRowAction(_item: TimelineEntry, action: TimelineAction) {
  /* openPanel 白名单：quote-sidebar 必带 symbol，alert-rules 无条件；其余 panel 一律不放行。 */
  const bench = getWorkbench()
  if (action.panel === 'quote-sidebar' && action.symbol) bench?.openPanel('quote-sidebar', { symbol: action.symbol })
  else if (action.panel === 'alert-rules') bench?.openPanel('alert-rules')
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
  const kindText = String(d.kind || d.type || 'alert')
  const symbol = typeof d.symbol === 'string' && d.symbol.trim() ? d.symbol.trim() : undefined
  const entry: NotifyMessage = {
    id: `ws-${msg.seq ?? Date.now()}`,
    kind: kindText,
    level: String(d.level || d.severity || ''),
    title,
    body: String(d.body || d.message || ''),
    time: new Date().toISOString(),
    read: false,
    /* 扩展字段：symbol/category 供分级与动作；causeKey 与漏斗（utils/notify）同口径，合并去重吃它。 */
    symbol,
    category: typeof d.category === 'string' ? d.category : undefined,
    causeKey: causeKey({
      kind: kindText,
      symbol,
      dedupKey: typeof d.dedup_key === 'string' ? d.dedup_key : undefined,
    }),
  }
  items.value = [entry, ...items.value].slice(0, 200)
  // 面板开着等于在看，推送到了就顺手清零角标；条目自身仍按未读呈列，点开才算读过。
  notify.markRead()
}

function markOneRead(item: TimelineEntry) {
  const id = String(item.id)
  if (id.startsWith(FEED_ID_PREFIX)) return
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
        <p class="nt-note">{{ merged.length }} 条消息 · 未读 {{ unreadInList }} · 点条目标记已读</p>
      </div>
      <div class="filters">
        <NtSelect v-model="level" :options="levelOptions" />
        <NtSelect v-model="grade" :options="gradeOptions" />
        <NtSelect v-model="kind" :options="kindOptions" />
        <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
        <NtButton size="sm" :disabled="unreadInList === 0" @click="markAllRead">全部已读</NtButton>
      </div>
    </header>

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section class="nt-panel">
      <NtTimeline
        :items="entries"
        :empty="loading ? '加载中…' : '还没有消息'"
        selectable
        @select="markOneRead"
        @action="onRowAction"
      />
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
