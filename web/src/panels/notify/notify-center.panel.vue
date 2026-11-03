<script setup lang="ts">
/**
 * 通知中心面板（M10）：消息列表 + 级别/类别筛选 + WS alert 推送即时入列。
 * G3：逐条已读态（点条目标记，localStorage 持久化）+ 全部已读；
 * 打开面板仍清零 StatusBar 角标（见 stores/notify.ts），两者语义独立。
 * 批次 A5：已读同时写服务端（MarkRead，跨设备一致）——localStorage 降为乐观层；
 * 服务端未读数经 alerts.refresh 回种角标，清零后若服务端仍有未读会如实回弹。
 * P2 承接层（方案 §8.6）：stores/notify 的 feed（告警漏斗落账）与 listMessages 合并去重，
 * feed 实时条目在前；每条归一分级章（feed 用其 level，api 用 levelOf 推导）、
 * L1–L4「级别」过滤与既有级别/类别可叠加、单条动作白名单 openPanel、静默条目角落注记。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { listMessages, markRead as markReadServer, type NotifyMessage } from '@/api/notify'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtTimeline, { type TimelineAction, type TimelineEntry } from '@/display/NtTimeline.vue'
import { useNotifyStore, type FeedEntry } from '@/stores/notify'
import { causeKey, levelOf, renderAlertCopy, type AlertLevel } from '@/ui/alert-levels'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtSelect from '@/ui/NtSelect.vue'
import { notifyKindEntry, statusOf } from '@/ui/status-map'
import { clock, messageOf } from '@/utils/format'
import { capNotifReadIds, loadNotifReadIds, saveNotifReadIds } from '@/utils/notify-read'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'
import { getWorkbench } from '@/workspace/inject'
import { onPush, type WsPushMessage } from '@/ws/hub'

// W3-2 面板偏好持久化：键版勿改，旧档自然失效。
const PREFS_KEY = 'nt.prefs.notify-center.v1'
/* grade 归一分级白名单；level/kind 选项来自消息数据的动态字段（'*'），就绪后由消费面校验。 */
const PREFS_ALLOW = { grade: ['', 'L1', 'L2', 'L3', 'L4'], level: '*', kind: '*' } as const

const notify = useNotifyStore()

const items = ref<NotifyMessage[]>([])
const loading = ref(false)
const error = ref('')
/* 读档回填在首次数据加载之前；无档回落现有默认 ''（全部）。 */
const prefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)
const level = ref(prefs.level ?? '')
const grade = ref(prefs.grade ?? '')
const kind = ref(prefs.kind ?? '')
const readIds = ref<string[]>(loadNotifReadIds())

/* 任一筛选变化写整档（含未变字段），单字段变化不丢兄弟字段。 */
watch([level, grade, kind], () =>
  savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { grade: grade.value, level: level.value, kind: kind.value }),
)

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
  /** 批次 A5：同因合并计数（feed 条 10s 窗内同因累计；api 条无此概念）。 */
  count?: number
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
    count: f.count,
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

/* 动态字段（'*'）存档校验：选项就绪后现值不在合法集即回默认 ''，防陈旧存档把 select 卡死在不存在选项。 */
watch(levelOptions, (opts) => {
  if (opts.length > 1 && level.value && !opts.some((o) => o.value === level.value)) level.value = ''
})
watch(kindOptions, (opts) => {
  if (opts.length > 1 && kind.value && !opts.some((o) => o.value === kind.value)) kind.value = ''
})

// 筛选态空文案（B3 走查留账销号）：三筛选任一在选时列表为空不再说「还没有消息」——筛选本身可能就是原因。
const hasFilter = computed(() => Boolean(level.value || grade.value || kind.value))

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
    /* W4c 二维分级：类别徽注预取入行模型（模板零函数调用，W4b review F7 纪律）；
       册内 kind 打章（NtBadge），册外 kind 留 undefined 由 tag 裸值兜底回显（不自封文案）。 */
    kindBadge: notifyKindEntry(r.kind) ?? undefined,
    tone: toneOfRow(r),
    read: r.read,
    level: r.level,
    actions: actionsOf(r),
    /* 静默注记走册（第十三批 notify.silenced）：文案唯一出处，面板禁自封「静默」；
       批次 A5：同因计数「×N」并注（同一问题连报几轮一眼可见），册词原样不拆。 */
    note:
      [
        r.silenced ? statusOf('notify.silenced').text : '',
        r.count != null && r.count > 1 ? `同因 ×${r.count}` : '',
      ]
        .filter(Boolean)
        .join(' · ') || undefined,
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
    // rawLevel 三级回退：level → severity → priority（priority 是部分推送端旧档字段，W3-2 收拢时补齐的兜底）。
    level: String(d.level || d.severity || d.priority || ''),
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
  /* 批次 A5：逐条已读同步服务端（跨设备一致）。ws- 前缀是本地推送合成条，不在服务端收件箱，跳过；
     失败静默：本机 localStorage 乐观层仍显已读，他端未读未清，下轮拉取以服务端 read_at 为准。 */
  if (!id.startsWith('ws-')) void markReadServer([id]).catch(() => {})
}

/* W4b 抽屉选中行：select 分流=标已读（G3 语义保真）+ 开抽屉双发；动作钮只跳面板不开抽屉。 */
const drawerOpen = ref(false)
const selectedId = ref('')
const selectedRow = computed(() => merged.value.find((r) => r.id === selectedId.value) ?? null)
/* W4c：抽屉类别章同册——selectedRow 派生重算，模板零函数调用。 */
const selectedKindBadge = computed(() => notifyKindEntry(selectedRow.value?.kind))
function onSelect(item: TimelineEntry) {
  markOneRead(item)
  selectedId.value = String(item.id)
  drawerOpen.value = true
}

async function markAllRead() {
  readIds.value = capNotifReadIds([...items.value.map((m) => m.id), ...readIds.value])
  saveNotifReadIds(readIds.value)
  notify.markRead()
  /* 批次 A5：全部已读落服务端（跨设备一致）。本地合成条（ws-）不在收件箱，只发服务端真 id；
     失败如实告知——本机已读保留，他端未读未清，重试或下轮刷新收敛。 */
  const serverIds = items.value.map((m) => m.id).filter((id) => id && !id.startsWith('ws-'))
  if (!serverIds.length) return
  try {
    await markReadServer(serverIds)
  } catch (err) {
    error.value = `服务端已读未写入：${messageOf(err)}（本机已标读，他端未读未清）`
  }
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
  <!-- W4b 三段式：筛选条=三 Select 平移 → 主体=NtTimeline → 320 抽屉=消息详情（select 分流=标已读+开抽屉）。 -->
  <NtAdminScaffold v-model:drawer-open="drawerOpen" title="通知中心" :drawer-width="320">
    <template #actions>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
      <NtButton size="sm" :disabled="unreadInList === 0" @click="markAllRead">全部已读</NtButton>
    </template>
    <template #filter>
      <NtSelect v-model="level" :options="levelOptions" />
      <NtSelect v-model="grade" :options="gradeOptions" />
      <NtSelect v-model="kind" :options="kindOptions" />
      <p class="nt-note nc-filter-note">{{ merged.length }} 条消息 · 未读 {{ unreadInList }} · 点条目看详情（收件箱条目标已读；漏斗落账条无已读态）</p>
    </template>
    <div class="nc-body">
      <NtErrorGuide :error="error" />
      <section class="nt-panel">
        <NtTimeline
          :items="entries"
          :empty="loading ? '读取中…' : error ? '消息未返回——点上方刷新重试' : hasFilter ? '当前筛选条件下没有消息——可放宽级别/类别筛选再看看' : '还没有消息——系统动作、风控与人工复核建议都会推送在这里'"
          selectable
          @select="onSelect"
          @action="onRowAction"
        />
      </section>
    </div>
    <template #drawer>
      <div v-if="selectedRow" class="nc-drawer">
        <h4>消息详情</h4>
        <dl>
          <div><dt>时间</dt><dd>{{ selectedRow.time }}</dd></div>
          <div><dt>分级</dt><dd>{{ selectedRow.level }}</dd></div>
          <div><dt>级别</dt><dd>{{ selectedRow.rawLevel || '—' }}</dd></div>
          <div>
            <dt>类别</dt>
            <dd>
              <!-- 详情面 = 精度面：章 + 裸码并显，用户才能把章对应回「类别」筛选下拉的原值。 -->
              <NtBadge v-if="selectedKindBadge" :tone="selectedKindBadge.tone">{{ selectedKindBadge.text }}</NtBadge>
              <span v-if="selectedKindBadge && selectedRow.kind" class="kind-raw">（{{ selectedRow.kind }}）</span>
              <template v-if="!selectedKindBadge">{{ selectedRow.kind || '—' }}</template>
            </dd>
          </div>
          <div v-if="selectedRow.symbol"><dt>证券</dt><dd>{{ selectedRow.symbol }}</dd></div>
          <div><dt>已读</dt><dd>{{ selectedRow.read ? '已读' : '未读' }}</dd></div>
          <div v-if="selectedRow.silenced"><dt>注记</dt><dd>{{ statusOf('notify.silenced').text }}</dd></div>
        </dl>
        <h4>标题</h4>
        <p class="detail-full">{{ selectedRow.title }}</p>
        <h4>正文</h4>
        <p class="detail-full">{{ selectedRow.body || '—' }}</p>
        <p class="id-line">id：{{ selectedRow.id }}</p>
      </div>
      <p v-else class="nc-drawer-empty">
        <!-- W4b review F2：live 推送/筛选可把选中行挤出 200 上限——显「被挤出」诚实缺位，不冒充未选。 -->
        {{ selectedId ? '该条已被新推送挤出列表或不在当前筛选内。' : '在左侧点一条消息看全文。' }}
      </p>
    </template>
  </NtAdminScaffold>
</template>

<style scoped>
.nc-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 12px;
}

.nc-filter-note {
  margin: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
  flex: 1 1 100%;
}

.nc-drawer {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
}

.nc-drawer h4 {
  margin: 4px 0 2px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.nc-drawer dl {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: var(--nt-fs-caption);
}

.nc-drawer dl > div {
  display: flex;
  gap: 8px;
}

.nc-drawer dt {
  flex: none;
  width: 44px;
  color: var(--nt-text-3);
}

.nc-drawer dd {
  margin: 0;
  color: var(--nt-text-1);
  word-break: break-all;
}

.detail-full {
  margin: 0;
  font-size: var(--nt-fs-caption);
  line-height: 1.6;
  color: var(--nt-text-1);
  white-space: pre-wrap;
  word-break: break-all;
}

.id-line {
  margin: 0;
  font-family: var(--nt-font-mono);
  font-size: var(--nt-fs-micro);
  color: var(--nt-text-3);
}

.kind-raw {
  color: var(--nt-text-3);
  font-size: var(--nt-fs-micro);
}

.nc-drawer-empty {
  margin: 0;
  padding: 12px;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}
</style>
