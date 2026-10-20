<script setup lang="ts">
/**
 * F10 悬浮卡（方案 §6.5）：hover 证券标识时出的身份小卡。
 * - 宿主以 props.rect（触发元素视口坐标，getBoundingClientRect 口径）挂入；
 *   本组件 Teleport 到 body 并登记 useOverlay（Esc 只关栈顶，即本卡）；
 * - 定位：贴触发元素下沿优先，视口底部放不下翻上沿，横向 clamp 进视口；
 * - 内容：身份 4 行（行业/流通市值/PE(TTM)/PB，全部取 security 字典，缺显「—」）
 *   + 最新公告 2 条（intel.timeline 过滤 kind==='announcement'，标题+HH:MM+来源章；
 *   有 url 新窗口打开，无 url 走内链 intel 面板）；加载中显「加载中…」，失败显「未返回」（数据纪律）；
 * - 交互：pointerenter 卡体保活（emit enter），pointerleave 100ms 后 emit close；
 * - [data-motion='reduced'] 全局归零 transition，此处显式兜底，即刻显隐。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { timeline, type IntelItem } from '@/api/intel'
import SymbolName from '@/display/SymbolName.vue'
import NtBadge from '@/ui/NtBadge.vue'
import { useOverlay } from '@/ui/useOverlay'
import { useSecurityStore } from '@/stores/security'
import { compactAmount } from '@/utils/format'
import { formatMinute } from '@/utils/time'
import { getWorkbench } from '@/workspace/inject'

const props = defineProps<{
  /** 证券代码（裸六位或全码均可，字典/接口各自归一）。 */
  code: string
  /** 触发元素的视口坐标。 */
  rect: { x: number; y: number; width: number; height: number }
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'enter'): void
}>()

const GAP = 8
/** 未测得卡宽时的定位初值（约等于样式 width 300px）。 */
const FALLBACK_W = 300
/** 字段无数据源/字典缺行统一显「—」（数据纪律，禁派生）。 */
const MISS = '—'

const security = useSecurityStore()
const overlay = useOverlay(() => emit('close'))

const card = ref<HTMLElement | null>(null)
const x = ref(GAP)
const y = ref(GAP)

/** 视口内定位：贴下沿优先，底部溢出且上沿够位则翻上；横向 clamp。 */
function place() {
  if (typeof window === 'undefined') return
  const r = props.rect
  const vw = window.innerWidth
  const vh = window.innerHeight
  const w = card.value?.offsetWidth ?? FALLBACK_W
  const h = card.value?.offsetHeight ?? 0
  x.value = Math.min(Math.max(GAP, r.x), Math.max(GAP, vw - w - GAP))
  const below = r.y + r.height + GAP
  const flip = below + h > vh - GAP && r.y - GAP - h >= GAP
  y.value = flip ? r.y - GAP - h : below
}

/* ---------- 身份 4 行（security 字典） ---------- */

const basic = computed(() => (props.code ? security.get(props.code) : undefined))
const industryText = computed(() => basic.value?.industry || MISS)
const circMvText = computed(() => (basic.value?.circMv ? compactAmount(basic.value.circMv) : MISS))
const peText = computed(() => (basic.value?.peTtm ? basic.value.peTtm.toFixed(1) : MISS))
const pbText = computed(() => (basic.value?.pb ? basic.value.pb.toFixed(2) : MISS))

/* ---------- 最新公告 2 条 ---------- */

const news = ref<IntelItem[]>([])
const phase = ref<'loading' | 'ready' | 'failed'>('loading')
let disposed = false

async function load() {
  const code = props.code
  if (!code) return
  phase.value = 'loading'
  try {
    const res = await timeline(code)
    if (disposed || code !== props.code) return
    news.value = res.items.filter((it) => it.kind === 'announcement').slice(0, 2)
    phase.value = 'ready'
    // 公告就位后卡高变化，重新定位防压视口底。
    await nextTick()
    place()
  } catch {
    if (disposed || code !== props.code) return
    phase.value = 'failed'
  }
}

/** 打开公告：有 url 新窗口（noopener 防反链）；无 url 内链 intel 面板并关卡。 */
function openAnnouncement(item: IntelItem) {
  if (item.url) {
    window.open(item.url, '_blank', 'noopener')
    return
  }
  getWorkbench()?.openPanel('intel', { symbol: props.code || undefined, highlight: item.id || undefined })
  emit('close')
}

/* ---------- 悬停保活 / 延时关闭 ---------- */

let leaveTimer: ReturnType<typeof setTimeout> | null = null

function onPointerEnter() {
  if (leaveTimer) {
    clearTimeout(leaveTimer)
    leaveTimer = null
  }
  emit('enter')
}

function onPointerLeave() {
  if (leaveTimer) clearTimeout(leaveTimer)
  leaveTimer = setTimeout(() => emit('close'), 100)
}

onMounted(async () => {
  overlay.open()
  await nextTick()
  place()
  void load()
})

watch(
  () => [props.rect.x, props.rect.y, props.rect.width, props.rect.height],
  () => {
    void nextTick(place)
  },
)

watch(
  () => props.code,
  () => void load(),
)

onBeforeUnmount(() => {
  disposed = true
  if (leaveTimer) clearTimeout(leaveTimer)
})
</script>

<template>
  <Teleport to="body">
    <div
      v-if="overlay.visible.value"
      ref="card"
      class="f10-hover"
      role="dialog"
      aria-label="证券速览"
      :style="{ left: `${x}px`, top: `${y}px`, zIndex: overlay.zIndex.value }"
      @pointerenter="onPointerEnter"
      @pointerleave="onPointerLeave"
    >
      <header class="f10-hover__head">
        <SymbolName :code="code" mode="inline" :clickable="false" />
      </header>

      <dl class="f10-hover__rows">
        <div>
          <dt>行业</dt>
          <dd :class="{ miss: industryText === MISS }">{{ industryText }}</dd>
        </div>
        <div>
          <dt>流通市值</dt>
          <dd :class="{ miss: circMvText === MISS }">{{ circMvText }}</dd>
        </div>
        <div>
          <dt>PE(TTM)</dt>
          <dd :class="{ miss: peText === MISS }">{{ peText }}</dd>
        </div>
        <div>
          <dt>PB</dt>
          <dd :class="{ miss: pbText === MISS }">{{ pbText }}</dd>
        </div>
      </dl>

      <section class="f10-hover__news">
        <p v-if="phase === 'loading'" class="f10-hover__hint">加载中…</p>
        <p v-else-if="phase === 'failed'" class="f10-hover__hint">未返回</p>
        <p v-else-if="!news.length" class="f10-hover__hint">暂无公告。</p>
        <ul v-else>
          <li v-for="item in news" :key="item.id || item.time + item.title">
            <button type="button" class="f10-hover__row" @click="openAnnouncement(item)">
              <b class="time">{{ formatMinute(item.time) || MISS }}</b>
              <span class="title">{{ item.title }}</span>
              <NtBadge v-if="item.source" tone="neutral">{{ item.source }}</NtBadge>
            </button>
          </li>
        </ul>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
/* 悬浮小卡：玻璃弹层语言同 NtModal，非 hero 位无 glow。 */
.f10-hover {
  position: fixed;
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 300px;
  max-width: calc(100vw - 16px);
  max-height: calc(100vh - 16px);
  overflow: auto;
  padding: 10px 12px 12px;
  background: var(--nt-bg-glass);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-m);
  box-shadow: var(--nt-shadow-depth);
  backdrop-filter: var(--nt-glass-filter);
  transition: opacity var(--nt-transition);
}

.f10-hover__head {
  display: flex;
  align-items: baseline;
  font-size: var(--nt-fs-body);
}

.f10-hover__rows {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 12px;
  margin: 0;
}

.f10-hover__rows > div {
  min-width: 0;
}

.f10-hover__rows dt {
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

.f10-hover__rows dd {
  margin: 2px 0 0;
  font-size: var(--nt-fs-body);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-1);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.f10-hover__rows dd.miss {
  color: var(--nt-text-3);
}

.f10-hover__news {
  padding-top: 8px;
  border-top: 1px solid var(--nt-border-1);
}

.f10-hover__hint {
  margin: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

.f10-hover__news ul {
  display: flex;
  flex-direction: column;
  gap: 4px;
  list-style: none;
  margin: 0;
  padding: 0;
}

.f10-hover__row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  width: 100%;
  padding: 3px 4px;
  border: none;
  border-radius: var(--nt-radius-s);
  background: transparent;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.f10-hover__row:hover {
  background: var(--nt-accent-soft);
}

.f10-hover__row .time {
  flex: none;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 400;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.f10-hover__row .title {
  flex: 1;
  min-width: 0;
  font-size: var(--nt-fs-body);
  color: var(--nt-text-1);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 即刻显隐兜底：全局 [data-motion='reduced'] 已归零 transition，此处显式对齐悬卡合同。 */
:root[data-motion='reduced'] .f10-hover {
  transition: none;
}
</style>
