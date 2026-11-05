<script setup lang="ts">
/**
 * 通知中心（M10 / 本轮重塑）：三层密度信息架构。
 * 首层每行只留：时间 · L1–L4 · 情绪符号+语义色 · 单行标题 · 单行摘要 · 真实 symbol · ×N · 未读态。
 * 情绪只由明确事件类型/方向字段决定，不解析标题或正文猜股票、猜涨跌。
 * 阅读锚点：读历史时插入新消息不跳动；顶部「N 条新消息」不抢焦点。
 * 已读态不用整行 opacity（会压穿 WCAG AA）：未读=左侧 2px 实色条+圆点+标题 600，已读=✓+标题 500。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { listMessages, markRead as markReadServer, normalizeMessage, normalizeNotifySymbol, type NotifyMessage } from '@/api/notify'
import NtAdminScaffold from '@/display/NtAdminScaffold.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import { causeKey, levelOf, type AlertLevel } from '@/ui/alert-levels'
import { createModalFocus } from '@/ui/modal-focus'
import { useNotifyStore, type FeedEntry } from '@/stores/notify'
import { useSymbolStore } from '@/stores/symbol'
import { notifyKindEntry } from '@/ui/status-map'
import { clock, messageOf } from '@/utils/format'
import { capNotifReadIds, loadNotifReadIds, saveNotifReadIds } from '@/utils/notify-read'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'
import { getWorkbench } from '@/workspace/inject'
import { onPush, type WsPushMessage } from '@/ws/hub'

const PREFS_KEY = 'nt.prefs.notify-center.v1'
const PREFS_ALLOW = { grade: ['', 'L1', 'L2', 'L3', 'L4'], kind: '*' } as const

const notify = useNotifyStore()
const symbolStore = useSymbolStore()

const items = ref<NotifyMessage[]>([])
const loading = ref(false)
const error = ref('')
const prefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)
const grade = ref(prefs.grade ?? '')
const kind = ref(prefs.kind ?? '')
const unreadOnly = ref(false)
const hasSymbolOnly = ref(false)
const search = ref('')
const readIds = ref<string[]>(loadNotifReadIds())

watch([grade, kind], () => savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { grade: grade.value, kind: kind.value }))

const readSet = computed(() => new Set(readIds.value))

interface CenterRow {
  id: string
  level: AlertLevel
  kind: string
  symbol?: string
  title: string
  summary?: string
  time: string
  /** 原始时间（feed 是毫秒时间戳，api 是 ISO 文本），分组排序用。 */
  at: number | string
  read: boolean
  silenced?: boolean
  causeKey?: string
  count?: number
  /** 方向字段：只认来源明确给出的 side/direction，不从正文猜。 */
  side?: string
  direction?: string
}

const FEED_ID_PREFIX = 'feed-'
const WS_ID_PREFIX = 'ws-'

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
    kind: f.kind || '',
    symbol: normalizeNotifySymbol(f.symbol),
    title: f.title || '（无标题）',
    summary: f.body,
    time: clock(f.at),
    at: f.at,
    /* 落账条非收件箱消息：不做未读加粗/不计未读数，点读也不进 readIds。 */
    read: true,
    silenced: f.silenced,
    causeKey: f.causeKey,
    count: f.count,
  }
}

function apiRow(m: NotifyMessage): CenterRow {
  const extra = m as Record<string, unknown>
  const category = typeof extra.category === 'string' ? extra.category : undefined
  const cause = typeof extra.causeKey === 'string' && extra.causeKey ? extra.causeKey : undefined
  return {
    id: m.id,
    level: levelOf({ kind: m.kind, level: m.level, category, symbol: m.symbol }),
    kind: m.kind,
    symbol: normalizeNotifySymbol(m.symbol),
    title: m.title || '（无标题）',
    summary: m.body,
    time: clock(m.time),
    at: m.time,
    read: isRead(m),
    causeKey: cause,
    side: typeof extra.side === 'string' ? extra.side : undefined,
    direction: typeof extra.direction === 'string' ? extra.direction : undefined,
  }
}

/* 合并去重：feed 在前；api 按 id 去重，与 feed 同因的让位 feed；上限 200。 */
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
  const all = [...feedRows, ...apiRows].slice(0, 200)
  const timeOf = (r: CenterRow) => (typeof r.at === 'number' ? r.at : Date.parse(String(r.at)) || 0)
  return all.sort((a, b) => timeOf(b) - timeOf(a))
})

const kinds = computed(() => [...new Set(merged.value.map((r) => r.kind).filter(Boolean))].sort())
const kindOptions = computed(() => kinds.value.map((k) => ({ value: k, label: notifyKindEntry(k)?.text ?? k })))

watch(kindOptions, (opts) => {
  if (opts.length && kind.value && !opts.some((o) => o.value === kind.value)) kind.value = ''
})

const hasFilter = computed(() => Boolean(grade.value || kind.value || unreadOnly.value || hasSymbolOnly.value || search.value.trim()))

const filtered = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  return merged.value.filter((r) => {
    if (grade.value && r.level !== grade.value) return false
    if (kind.value && r.kind !== kind.value) return false
    if (unreadOnly.value && r.read) return false
    if (hasSymbolOnly.value && !r.symbol) return false
    if (keyword && !(`${r.title} ${r.summary ?? ''} ${r.symbol ?? ''} ${r.kind}`.toLowerCase().includes(keyword))) return false
    return true
  })
})

const unreadInFiltered = computed(() => filtered.value.filter((r) => !r.read).length)

/* 初始只渲染 30 条；底部「查看全部已加载（N）」一次性展开，不伪造服务端全量。 */
const visibleLimit = ref(30)
const visibleRows = computed(() => filtered.value.slice(0, visibleLimit.value))

function dayKey(at: number | string): string {
  const date = new Date(typeof at === 'number' ? at : String(at))
  if (Number.isNaN(date.getTime())) return 'unknown'
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

function dayLabel(key: string): string {
  if (key === 'unknown') return '时间未标注'
  const today = dayKey(Date.now())
  if (key === today) return '今天'
  const yesterday = dayKey(Date.now() - 86_400_000)
  if (key === yesterday) return '昨天'
  return key.slice(5)
}

const groups = computed(() => {
  const out: { key: string; label: string; rows: CenterRow[] }[] = []
  for (const row of visibleRows.value) {
    const key = dayKey(row.at)
    let group = out.find((g) => g.key === key)
    if (!group) {
      group = { key, label: dayLabel(key), rows: [] }
      out.push(group)
    }
    group.rows.push(row)
  }
  return out
})

/* 情绪：只由明确事件类型/方向字段决定，不解析正文。 */
function emotionOf(r: CenterRow): 'up' | 'down' | 'danger' | 'warn' | 'neutral' {
  const kindText = r.kind.toLowerCase()
  const side = (r.side ?? '').toLowerCase()
  const direction = (r.direction ?? '').toLowerCase()
  if (side === 'buy' || direction === 'up') return 'up'
  if (side === 'sell' || direction === 'down') return 'down'
  if (kindText === 'risk' || r.level === 'L4') return 'danger'
  if (kindText === 'alert' || kindText === 'warn' || r.level === 'L3') return 'warn'
  return 'neutral'
}

const EMOTION: Record<string, { symbol: string; label: string }> = {
  up: { symbol: '▲', label: '上涨/买入方向' },
  down: { symbol: '▼', label: '下跌/卖出方向' },
  danger: { symbol: '!', label: '严重' },
  warn: { symbol: '△!', label: '警示' },
  neutral: { symbol: '—', label: '中性/未知' },
}

function markOneRead(row: CenterRow) {
  const id = row.id
  if (id.startsWith(FEED_ID_PREFIX) || id.startsWith(WS_ID_PREFIX)) return
  if (readSet.value.has(id)) return
  readIds.value = capNotifReadIds([id, ...readIds.value])
  saveNotifReadIds(readIds.value)
  void markReadServer([id]).catch(() => {})
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

/* ---------- WS 推送：同 id 去重 + 阅读锚点 ---------- */

const listEl = ref<HTMLElement | null>(null)
const newCount = ref(0)
const scrolled = ref(false)

function onListScroll() {
  scrolled.value = (listEl.value?.scrollTop ?? 0) > 8
}

interface Anchor {
  id: string
  offset: number
  at: string
}

function captureAnchor(): Anchor | null {
  const list = listEl.value
  if (!list || list.scrollTop <= 8) return null
  const top = list.getBoundingClientRect().top
  for (const row of list.querySelectorAll<HTMLElement>('[data-row-id]')) {
    const rect = row.getBoundingClientRect()
    if (rect.bottom > top + 1) return { id: row.dataset.rowId ?? '', offset: rect.top - top, at: row.dataset.rowAt ?? '' }
  }
  return null
}

async function restoreAnchor(anchor: Anchor | null) {
  if (!anchor?.id) return
  await nextTick()
  const list = listEl.value
  if (!list) return
  let row = list.querySelector<HTMLElement>(`[data-row-id="${CSS.escape(anchor.id)}"]`)
  if (!row && anchor.at) {
    /* 锚点行被 200 条上限淘汰：退回到比它更旧的最近一行，保持阅读位置不跳。 */
    const atMs = (value: string) => {
      const n = Number(value)
      return Number.isFinite(n) && n > 0 ? n : Date.parse(value) || 0
    }
    const anchorAt = atMs(anchor.at)
    let best: { el: HTMLElement; delta: number } | null = null
    for (const el of list.querySelectorAll<HTMLElement>('[data-row-at]')) {
      const at = atMs(el.dataset.rowAt ?? '')
      if (at >= anchorAt) continue
      const delta = anchorAt - at
      if (!best || delta < best.delta) best = { el, delta }
    }
    row = best?.el ?? null
  }
  if (!row) return
  const delta = row.getBoundingClientRect().top - list.getBoundingClientRect().top - anchor.offset
  if (Math.abs(delta) > 0.5) list.scrollTop += delta
}

function onPushMessage(msg: WsPushMessage) {
  if (msg.topic !== 'alert') return
  const d = (msg.data ?? {}) as Record<string, unknown>
  const title = String(d.title || d.subject || '')
  if (!title) return
  const kindText = String(d.kind || d.type || 'alert')
  const id = d.id != null && String(d.id).trim() ? String(d.id) : `ws-${msg.seq ?? Date.now()}`
  const existing = items.value.findIndex((m) => m.id === id)
  const anchor = captureAnchor()
  const entry = normalizeMessage(d, {
    id,
    time: new Date().toISOString(),
    read: existing >= 0 ? items.value[existing].read : false,
    kind: kindText,
    title,
    body: String(d.body || d.message || ''),
    causeKey: causeKey({ kind: kindText, symbol: normalizeNotifySymbol(d.symbol), dedupKey: typeof d.dedup_key === 'string' ? d.dedup_key : undefined }),
  })
  if (existing >= 0) {
    /* 同 id 连发/重放：原地合并，不新增行，也不让「N 条新消息」多加 1。 */
    items.value = items.value.map((m, i) => (i === existing ? { ...m, ...entry, read: m.read || entry.read } : m))
  } else {
    items.value = [entry, ...items.value].slice(0, 200)
    if (anchor) newCount.value += 1
  }
  void restoreAnchor(anchor)
  notify.markRead()
}

function jumpToTop() {
  listEl.value?.scrollTo({ top: 0, behavior: 'smooth' })
  newCount.value = 0
}

/* ---------- hover / 键盘摘要 ---------- */

const summary = ref<{ row: CenterRow; top: number; left: number } | null>(null)
let openTimer: ReturnType<typeof setTimeout> | undefined
let closeTimer: ReturnType<typeof setTimeout> | undefined

function placeSummary(row: CenterRow, el: HTMLElement) {
  const rect = el.getBoundingClientRect()
  const width = 320
  const height = 180
  let left = rect.right + 8
  if (left + width > window.innerWidth - 8) left = Math.max(8, rect.left - width - 8)
  let top = Math.min(Math.max(8, rect.top), window.innerHeight - height - 8)
  if (top < 8) top = 8
  summary.value = { row, top, left }
}

function scheduleSummary(row: CenterRow, event: Event) {
  const el = event.currentTarget as HTMLElement
  clearTimeout(closeTimer)
  clearTimeout(openTimer)
  openTimer = setTimeout(() => placeSummary(row, el), 160)
}

function scheduleHide() {
  clearTimeout(openTimer)
  clearTimeout(closeTimer)
  closeTimer = setTimeout(() => {
    summary.value = null
  }, 140)
}

function keepSummary() {
  clearTimeout(closeTimer)
  clearTimeout(openTimer)
}

function closeSummary() {
  clearTimeout(openTimer)
  clearTimeout(closeTimer)
  summary.value = null
}

/* ---------- 详情模态 ---------- */

const detailRow = ref<CenterRow | null>(null)
const detailEl = ref<HTMLElement | null>(null)
const detailFocus = createModalFocus()

async function openDetail(row: CenterRow, event?: Event) {
  clearTimeout(openTimer)
  closeSummary()
  markOneRead(row)
  const trigger = (event?.currentTarget as HTMLElement | null) ?? null
  detailRow.value = row
  await nextTick()
  detailFocus.activate(detailEl.value, closeDetail, { fallback: trigger })
}

function closeDetail() {
  detailRow.value = null
  detailFocus.deactivate()
}

function gotoStock() {
  const symbol = detailRow.value?.symbol
  if (!symbol) return
  symbolStore.setSymbol(symbol, 'NOTIFY')
  const code = symbolStore.code || symbol
  const bench = getWorkbench()
  bench?.openPanel('market', { symbol: code })
  bench?.openPanel('trade', { symbol: code })
  closeDetail()
}

async function markAllRead() {
  readIds.value = capNotifReadIds([...items.value.map((m) => m.id), ...readIds.value])
  saveNotifReadIds(readIds.value)
  notify.markRead()
  const serverIds = items.value.map((m) => m.id).filter((id) => id && !id.startsWith('ws-'))
  if (!serverIds.length) return
  try {
    await markReadServer(serverIds)
  } catch (err) {
    error.value = `服务端已读未写入：${messageOf(err)}（本机已标读，他端未读未清）`
  }
}

let offPush: (() => void) | undefined

onMounted(() => {
  void load()
  notify.start()
  notify.markRead()
  offPush = onPush(onPushMessage)
})

onBeforeUnmount(() => {
  offPush?.()
  clearTimeout(openTimer)
  clearTimeout(closeTimer)
})
</script>

<template>
  <NtAdminScaffold title="通知中心">
    <template #actions>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
      <NtButton size="sm" :disabled="unreadInFiltered === 0" @click="markAllRead">全部已读</NtButton>
    </template>

    <template #filter>
      <div class="nc-toolbar" data-scroll="x">
        <div class="nc-chips" role="group" aria-label="类别筛选">
          <button type="button" class="nc-chip" :data-on="kind === ''" @click="kind = ''">全部</button>
          <button
            v-for="option in kindOptions"
            :key="option.value"
            type="button"
            class="nc-chip"
            :data-on="kind === option.value"
            @click="kind = kind === option.value ? '' : option.value"
          >
            {{ option.label }}
          </button>
        </div>
        <div class="nc-chips" role="group" aria-label="重要度筛选">
          <button
            v-for="option in gradeOptions"
            :key="option.value || 'all'"
            type="button"
            class="nc-chip"
            :data-on="grade === option.value"
            :aria-label="option.label"
            @click="grade = option.value"
          >
            {{ option.value || '全部' }}
          </button>
        </div>
        <button type="button" class="nc-chip" :data-on="unreadOnly" @click="unreadOnly = !unreadOnly">未读</button>
        <button type="button" class="nc-chip" :data-on="hasSymbolOnly" @click="hasSymbolOnly = !hasSymbolOnly">有股票</button>
        <input v-model="search" class="nc-search" type="search" placeholder="搜索关键词 / 股票代码" aria-label="搜索关键词或股票代码" />
        <p class="nc-counts">显示 {{ visibleRows.length }} / 筛选后 {{ filtered.length }} · 未读 {{ unreadInFiltered }}</p>
      </div>
    </template>

    <div class="nc-body">
      <NtErrorGuide :error="error" />
      <div v-if="newCount > 0" class="nc-newbanner" role="status">
        <button type="button" @click="jumpToTop">{{ newCount }} 条新消息 · 点击回到顶部</button>
      </div>
      <section ref="listEl" class="nc-list" data-scroll="y" aria-label="通知列表" @scroll.passive="onListScroll">
        <h4 class="nc-title-fallback" tabindex="-1">通知列表</h4>
        <p v-if="!visibleRows.length" class="nc-empty">
          {{ loading ? '读取中…' : error ? '消息未返回——点上方刷新重试' : hasFilter ? '当前筛选条件下没有消息——可放宽类别/重要度筛选再看看' : '还没有消息——系统动作、风控与人工复核建议都会推送在这里' }}
        </p>
        <section v-for="group in groups" :key="group.key" class="nc-group">
          <h5 class="nc-group__title">{{ group.label }}</h5>
          <ul class="nc-rows">
            <li v-for="row in group.rows" :key="row.id">
              <button
                type="button"
                class="nc-row"
                :class="{ unread: !row.read }"
                :data-row-id="row.id"
                :data-row-at="String(row.at)"
                :data-level="row.level"
                @click="openDetail(row, $event)"
                @mouseenter="scheduleSummary(row, $event)"
                @mouseleave="scheduleHide"
                @focus="scheduleSummary(row, $event)"
                @blur="scheduleHide"
                @keydown.enter.prevent="openDetail(row, $event)"
                @keydown.space.prevent="openDetail(row, $event)"
                @keydown.esc="closeSummary"
              >
                <time class="nc-row__time">{{ row.time }}</time>
                <span class="nc-row__level" :data-level="row.level">{{ row.level }}</span>
                <span class="nc-row__emo" :data-tone="emotionOf(row)" :title="EMOTION[emotionOf(row)].label">{{ EMOTION[emotionOf(row)].symbol }}</span>
                <span class="nc-row__main">
                  <span class="nc-row__title">{{ row.title }}</span>
                  <span class="nc-row__summary">{{ row.summary || '—' }}</span>
                </span>
                <span v-if="row.symbol" class="nc-row__symbol">{{ row.symbol }}</span>
                <span v-if="row.count != null && row.count > 1" class="nc-row__count">×{{ row.count }}</span>
                <span v-if="row.silenced" class="nc-row__silent">静默</span>
                <span class="nc-row__state" aria-hidden="true">{{ row.read ? '✓' : '●' }}</span>
              </button>
            </li>
          </ul>
        </section>
        <button v-if="filtered.length > visibleLimit" type="button" class="nc-more" @click="visibleLimit = filtered.length">
          查看全部已加载（{{ filtered.length }}）
        </button>
      </section>
    </div>
  </NtAdminScaffold>

  <!-- hover/键盘摘要：fixed 定位，不参与文档流，打开前后列表 scrollTop 与行 rect 不变。 -->
  <Teleport to="body">
    <div
      v-if="summary"
      class="nc-summary"
      :style="{ top: `${summary.top}px`, left: `${summary.left}px` }"
      role="tooltip"
      @mouseenter="keepSummary"
      @mouseleave="scheduleHide"
    >
      <p class="nc-summary__title">{{ summary.row.title }}</p>
      <p class="nc-summary__body">{{ summary.row.summary || '—' }}</p>
      <p class="nc-summary__meta">
        {{ summary.row.time }} · {{ summary.row.level }} · {{ summary.row.kind || '未分类' }}
        <span v-if="summary.row.symbol"> · {{ summary.row.symbol }}</span>
      </p>
    </div>
  </Teleport>

  <!-- 详情模态：640×560，股票主动作 → setSymbol + market/trade 同上下文；无股票不放假按钮。 -->
  <Teleport to="body">
    <div v-if="detailRow" class="nc-modal" role="dialog" aria-modal="true" aria-labelledby="nc-detail-title" @click.self="closeDetail">
      <section ref="detailEl" class="nc-modal__sheet" tabindex="-1">
        <header>
          <span class="nc-row__level" :data-level="detailRow.level">{{ detailRow.level }}</span>
          <span class="nc-row__emo" :data-tone="emotionOf(detailRow)">{{ EMOTION[emotionOf(detailRow)].symbol }}</span>
          <div class="nc-modal__head">
            <h3 id="nc-detail-title">{{ detailRow.title }}</h3>
            <p>{{ detailRow.time }} · {{ detailRow.kind || '未分类' }}{{ detailRow.silenced ? ' · 静默时段' : '' }}</p>
          </div>
          <button type="button" class="nc-modal__close" @click="closeDetail">关闭</button>
        </header>
        <div class="nc-modal__body" data-scroll="y">
          <p class="nc-modal__text">{{ detailRow.summary || '—' }}</p>
          <dl class="nc-modal__meta">
            <div><dt>时间</dt><dd>{{ detailRow.time }}</dd></div>
            <div><dt>重要度</dt><dd>{{ detailRow.level }}</dd></div>
            <div><dt>类别</dt><dd>{{ notifyKindEntry(detailRow.kind)?.text ?? (detailRow.kind || '—') }}</dd></div>
            <div v-if="detailRow.count != null && detailRow.count > 1"><dt>聚合</dt><dd>同因 ×{{ detailRow.count }}</dd></div>
          </dl>
        </div>
        <footer class="nc-modal__foot">
          <p v-if="!detailRow.symbol" class="nc-no-symbol">无关联股票</p>
          <button v-else type="button" class="nc-stock-action" @click="gotoStock">
            {{ detailRow.symbol }} · 查看行情并打开交易
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
/* 列表自己滚，外层骨架不再二次滚动，阅读锚点只认一个滚动容器。 */
:deep(.nt-admin__body) {
  overflow: hidden;
}

.nc-body {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  container-type: inline-size;
}

/* 容器窄于 260px（工作区在手机宽度下被桌面分栏压窄）时收掉行内辅助列，
 * 保证列表主流程不产生横向溢出；375 全屏面板宽度足够时这些列照常显示。 */
@container (max-width: 260px) {
  .nc-row__symbol,
  .nc-row__count {
    display: none;
  }
}

/* 单行 chips 工具条：横向滚动不换行，不占额外行高、不减少首屏条数。 */
.nc-toolbar {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
  min-width: 0;
  overflow-x: auto;
  white-space: nowrap;
  scrollbar-width: thin;
}

.nc-chips {
  display: flex;
  gap: 4px;
  flex: none;
}

.nc-chip {
  flex: none;
  height: 26px;
  padding: 0 6px;
  border: 1px solid var(--nt-border-2);
  border-radius: 999px;
  background: transparent;
  color: var(--nt-text-2);
  font: inherit;
  font-size: 11.5px;
  cursor: pointer;
}

.nc-chip[data-on='true'] {
  border-color: var(--nt-accent);
  color: var(--nt-accent);
  background: var(--nt-accent-soft);
}

.nc-search {
  flex: 0 1 140px;
  min-width: 72px;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--nt-border-2);
  border-radius: 4px;
  background: transparent;
  color: var(--nt-text-1);
  font: inherit;
  font-size: 12px;
}

.nc-counts {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  margin: 0;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.nc-list {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  overscroll-behavior: contain;
  scrollbar-width: thin;
}

.nc-title-fallback {
  position: absolute;
  left: -9999px;
  width: 1px;
  height: 1px;
  margin: 0;
  overflow: hidden;
}

.nc-newbanner {
  flex: none;
  display: flex;
  justify-content: center;
  padding: 6px 12px 0;
}

.nc-newbanner button {
  border: 1px solid var(--nt-accent);
  border-radius: 999px;
  background: var(--nt-accent-soft);
  color: var(--nt-text-1);
  font: inherit;
  font-size: 12px;
  padding: 4px 12px;
  cursor: pointer;
}

.nc-group__title {
  position: sticky;
  top: 0;
  z-index: 1;
  margin: 0;
  padding: 6px 12px 4px;
  background: var(--nt-bg-0, var(--nt-bg-1));
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.08em;
  color: var(--nt-text-3);
}

.nc-rows {
  margin: 0;
  padding: 0;
  list-style: none;
}

/* 行容器是整行按钮：桌面 ≥60px，紧凑 ≥52px，375 ≥64px；无卡片阴影/渐变。 */
.nc-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-height: 60px;
  padding: 8px 12px 8px 14px;
  border: 0;
  border-bottom: 1px solid var(--nt-border-1);
  border-radius: 0;
  background: transparent;
  color: var(--nt-text-1);
  font: inherit;
  text-align: left;
  cursor: pointer;
  opacity: 1; /* 已读态不允许整行透明：语义色必须保持 AA 对比度 */
}

/* 未读左侧 2px 实色条：绝对定位，不参与 padding/border，未读/已读行高严格相等。 */
.nc-row.unread::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 2px;
  background: var(--nt-accent);
}

.nc-row:hover,
.nc-row:focus-visible {
  background: var(--nt-accent-soft);
  outline: none;
}

.nc-row:focus-visible {
  box-shadow: inset 0 0 0 2px var(--nt-accent);
}

.nc-row__time {
  flex: none;
  width: 52px;
  font-family: var(--nt-font-mono);
  font-size: 11.5px;
  color: var(--nt-text-2);
}

.nc-row__level {
  flex: none;
  min-width: 24px;
  font-family: var(--nt-font-mono);
  font-size: 11px;
  color: var(--nt-text-2);
}

.nc-row__level[data-level='L3'] {
  color: var(--nt-warn);
}

.nc-row__level[data-level='L4'] {
  color: var(--nt-danger);
}

.nc-row__emo {
  flex: none;
  width: 20px;
  text-align: center;
  font-family: var(--nt-font-mono);
  font-size: 14px;
  font-weight: 700;
}

.nc-row__emo[data-tone='up'] {
  color: var(--nt-up);
}

.nc-row__emo[data-tone='down'] {
  color: var(--nt-down);
}

.nc-row__emo[data-tone='danger'] {
  color: var(--nt-danger);
}

.nc-row__emo[data-tone='warn'] {
  color: var(--nt-warn);
}

.nc-row__emo[data-tone='neutral'] {
  color: var(--nt-text-3);
}

.nc-row__main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1 1 auto;
  min-width: 0;
}

.nc-row__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  line-height: 18px;
  font-weight: 600;
}

.nc-row:not(.unread) .nc-row__title {
  font-weight: 500;
}

.nc-row__summary {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  line-height: 18px;
  color: var(--nt-text-2);
}

.nc-row__symbol {
  flex: none;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--nt-font-mono);
  font-size: 11.5px;
  color: var(--nt-text-1);
}

.nc-row__count,
.nc-row__silent {
  flex: none;
  font-size: 11px;
  color: var(--nt-text-2);
}

.nc-row__state {
  flex: none;
  width: 14px;
  text-align: center;
  font-size: 11px;
  color: var(--nt-text-3);
}

.nc-row.unread .nc-row__state {
  color: var(--nt-accent);
}

.nc-empty {
  margin: 0;
  padding: 16px 12px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--nt-text-3);
}

.nc-more {
  display: block;
  margin: 10px 12px;
  padding: 6px 12px;
  border: 1px solid var(--nt-border-2);
  border-radius: 999px;
  background: transparent;
  color: var(--nt-text-2);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

/* hover/摘要卡：宽 320、高 ≤180，视口内翻转。 */
.nc-summary {
  position: fixed;
  z-index: 2000;
  width: 320px;
  max-height: 180px;
  overflow: hidden;
  padding: 10px 12px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-m);
  background: var(--nt-bg-glass, var(--nt-bg-1));
  color: var(--nt-text-1);
  box-shadow: var(--nt-shadow-depth);
}

.nc-summary__title {
  margin: 0 0 4px;
  font-size: 12.5px;
  line-height: 18px;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.nc-summary__body {
  margin: 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--nt-text-2);
  overflow-wrap: anywhere;
}

.nc-summary__meta {
  margin: 6px 0 0;
  font-size: 11px;
  line-height: 16px;
  color: var(--nt-text-3);
}

/* 详情模态：640×560，<640px 全屏。 */
.nc-modal {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: grid;
  place-items: center;
  padding: 24px;
  overflow: hidden;
  background: var(--nt-scrim);
}

.nc-modal__sheet {
  display: flex;
  flex-direction: column;
  width: min(640px, calc(100vw - 48px));
  height: min(560px, calc(100vh - 48px));
  min-width: 0;
  overflow: hidden;
  padding: 16px 18px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-l);
  background: var(--nt-bg-glass, var(--nt-bg-1));
  color: var(--nt-text-1);
  outline: none;
  box-shadow: var(--nt-shadow-depth);
}

.nc-modal__sheet header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--nt-border-1);
}

.nc-modal__head {
  flex: 1 1 auto;
  min-width: 0;
}

.nc-modal__head h3 {
  margin: 0;
  font-size: 15px;
  line-height: 20px;
  overflow-wrap: anywhere;
}

.nc-modal__head p {
  margin: 2px 0 0;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.nc-modal__close {
  flex: none;
  border: 1px solid var(--nt-border-2);
  border-radius: 999px;
  background: transparent;
  color: var(--nt-text-1);
  font: inherit;
  font-size: 12px;
  padding: 4px 12px;
  cursor: pointer;
}

.nc-modal__body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 12px 0;
}

.nc-modal__text {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.7;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.nc-modal__meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin: 0;
}

.nc-modal__meta > div {
  min-width: 0;
}

.nc-modal__meta dt {
  font-size: 11px;
  color: var(--nt-text-3);
}

.nc-modal__meta dd {
  margin: 2px 0 0;
  font-size: 12px;
  overflow-wrap: anywhere;
}

.nc-modal__foot {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  min-height: 40px;
  padding-top: 10px;
  border-top: 1px solid var(--nt-border-1);
}

.nc-no-symbol {
  margin: 0;
  font-size: 12px;
  color: var(--nt-text-3);
}

.nc-stock-action {
  border: 1px solid var(--nt-accent);
  border-radius: 999px;
  background: var(--nt-accent-soft);
  color: var(--nt-text-1);
  font: inherit;
  font-size: 12px;
  padding: 6px 14px;
  cursor: pointer;
}

@media (max-width: 1200px) {
  .nc-row {
    min-height: 52px;
  }

  .nc-row__title {
    font-size: 12.5px;
  }

  .nc-row__summary {
    font-size: 12px;
    line-height: 16px;
  }
}

@media (max-width: 640px) {
  .nc-modal {
    padding: 0;
  }

  .nc-modal__sheet {
    width: 100vw;
    height: 100vh;
    border-radius: 0;
    border-left: 0;
    border-right: 0;
  }

  .nc-row {
    min-height: 64px;
  }

  .nc-row__title {
    font-size: 14px;
    line-height: 20px;
  }

  .nc-row__summary {
    font-size: 12.5px;
    line-height: 18px;
  }

  .nc-row__time {
    width: 46px;
  }

  .nc-row__symbol {
    max-width: 82px;
  }
}
</style>
