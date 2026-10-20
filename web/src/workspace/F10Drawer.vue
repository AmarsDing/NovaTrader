<script setup lang="ts">
/**
 * F10 抽屉（方案 §6.5）：右锚 Teleport 浮层，订阅 stores/f10。
 * - 唤出：宿主经 f10.openFor(code, tab?)；遮罩点击 / Esc（useOverlay 栈顶）→ close()；
 * - 页签 ↔ store.tab 双向同步（NtTabs 既有件）：
 *   · 概况 = security 字典身份（代码/名称/市场/行业 + ST·停牌章）+ 估值（PE(TTM)/PB/流通市值，
 *     缺显「—」，总市值 stock_basic 无该列恒显「—」禁派生），角落标注来源「security 字典」；
 *   · 公告 = intel.timeline 过滤 kind==='announcement' 全列（标题+时间+来源章）；
 *     有 url 新窗口打开，无 url 走内链 intel 面板；
 *   · 财务摘要 = 附录 B-3 端点未接入，显式占位不展示任何指标（避免虚构）；
 *   · AI 研判 = 证据链未接入本抽屉，接入前不呈现结论。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { timeline, type IntelItem } from '@/api/intel'
import SymbolName from '@/display/SymbolName.vue'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtIcon from '@/ui/NtIcon.vue'
import NtTabs from '@/ui/NtTabs.vue'
import { statusOf } from '@/ui/status-map'
import { useOverlay } from '@/ui/useOverlay'
import { useF10Store, type F10Tab } from '@/stores/f10'
import { useSecurityStore } from '@/stores/security'
import { compactAmount } from '@/utils/format'
import { formatDateTime } from '@/utils/time'
import { getWorkbench } from '@/workspace/inject'

const store = useF10Store()
const security = useSecurityStore()
const overlay = useOverlay(() => store.close())

/** 字段无数据源/字典缺行统一显「—」（数据纪律，禁虚构禁派生）。 */
const MISS = '—'

const sheet = ref<HTMLElement | null>(null)
const code = computed(() => store.code)

const TABS = [
  { value: 'overview', label: '概况' },
  { value: 'finance', label: '财务摘要' },
  { value: 'announce', label: '公告' },
  { value: 'ai', label: 'AI 研判' },
]

/** 页签与 store.tab 双向同步。 */
const tab = computed({
  get: () => store.tab,
  set: (value: string | number) => {
    store.tab = value as F10Tab
  },
})

watch(
  () => store.open,
  async (value) => {
    if (value) {
      overlay.open()
      await nextTick()
      sheet.value?.focus()
    } else {
      overlay.close()
    }
  },
  { immediate: true },
)

/* ---------- 概况页：security 字典身份 + 估值 ---------- */

const basic = computed(() => (code.value ? security.get(code.value) : undefined))
const resolved = computed(() =>
  code.value ? security.resolve(code.value) : { code: '', name: '', hasName: false },
)

const identityFields = computed(() => [
  { label: '代码', value: resolved.value.code || MISS },
  { label: '名称', value: resolved.value.hasName ? resolved.value.name : '名称未返回' },
  { label: '市场', value: basic.value?.market || MISS },
  { label: '行业', value: basic.value?.industry || MISS },
])

/** 估值区：全走 security 字典（stock_basic 口径）；negative PE 守卫同 quote-sidebar，
 *  总市值字典未携带恒显「—」（不拿 total_share×现价派生）。 */
const valuationFields = computed(() => {
  const b = basic.value
  return [
    { label: 'PE(TTM)', value: b?.peTtm ? b.peTtm.toFixed(1) : MISS },
    { label: 'PB', value: b?.pb ? b.pb.toFixed(2) : MISS },
    { label: '流通市值', value: b?.circMv ? compactAmount(b.circMv) : MISS },
    { label: '总市值', value: MISS },
  ]
})

/** ST/停牌章：文案/tone 收口 §7.1 status-map 册，缺省不显（quote-sidebar 同口径）。 */
const stBadge = computed(() => (basic.value?.st ? statusOf('security.st') : null))
const suspendBadge = computed(() => (basic.value?.suspended ? statusOf('security.suspended') : null))

/* ---------- 公告页：timeline 过滤 announcement 全列 ---------- */

const news = ref<IntelItem[]>([])
const newsPhase = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle')
let loadSeq = 0

async function loadAnnouncements() {
  const sym = code.value
  if (!sym || !store.open) return
  const seq = ++loadSeq
  newsPhase.value = 'loading'
  try {
    const res = await timeline(sym)
    if (seq !== loadSeq || sym !== code.value) return
    news.value = res.items.filter((it) => it.kind === 'announcement')
    newsPhase.value = 'ready'
  } catch {
    if (seq !== loadSeq) return
    newsPhase.value = 'failed'
  }
}

watch(
  [() => store.open, code],
  ([opened, sym]) => {
    if (opened && sym) {
      security.ensure([sym])
      void loadAnnouncements()
    }
  },
  { immediate: true },
)

/** 打开公告：有 url 新窗口（noopener 防反链）；无 url 内链 intel 面板并收抽屉让位。 */
function openAnnouncement(item: IntelItem) {
  if (item.url) {
    window.open(item.url, '_blank', 'noopener')
    return
  }
  getWorkbench()?.openPanel('intel', { symbol: code.value || undefined, highlight: item.id || undefined })
  store.close()
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="store.open"
      class="f10-mask"
      :style="{ zIndex: overlay.zIndex.value }"
      @mousedown.self="store.close()"
    >
      <aside ref="sheet" class="f10-drawer" role="dialog" aria-modal="true" aria-label="F10 证券资料" tabindex="-1">
        <header class="f10-drawer__head">
          <SymbolName v-if="code" :code="code" mode="stacked" :clickable="false" />
          <div class="f10-drawer__badges">
            <NtBadge v-if="stBadge" :tone="stBadge.tone">{{ stBadge.text }}</NtBadge>
            <NtBadge v-if="suspendBadge" :tone="suspendBadge.tone">{{ suspendBadge.text }}</NtBadge>
          </div>
          <NtButton class="f10-drawer__close" variant="quiet" size="sm" aria-label="关闭" @click="store.close()">
            <NtIcon name="close" :size="13" />
          </NtButton>
        </header>

        <NtTabs v-model="tab" :items="TABS" />

        <div class="f10-drawer__body">
          <section v-if="tab === 'overview'" class="f10-sec">
            <h3 class="f10-sec__title">身份</h3>
            <dl class="pairs">
              <template v-for="f in identityFields" :key="f.label">
                <dt>{{ f.label }}</dt>
                <dd :class="{ miss: f.value === MISS }">{{ f.value }}</dd>
              </template>
            </dl>
            <h3 class="f10-sec__title">估值</h3>
            <dl class="pairs">
              <template v-for="f in valuationFields" :key="f.label">
                <dt>{{ f.label }}</dt>
                <dd :class="{ miss: f.value === MISS }">{{ f.value }}</dd>
              </template>
            </dl>
            <p class="f10-src">来源：security 字典</p>
          </section>

          <p v-else-if="tab === 'finance'" class="f10-empty">
            财务摘要端点（附录 B-3）未接入——暂不展示任何指标，避免虚构。
          </p>

          <section v-else-if="tab === 'announce'" class="f10-sec">
            <p v-if="newsPhase === 'loading' || newsPhase === 'idle'" class="f10-empty">加载中…</p>
            <p v-else-if="newsPhase === 'failed'" class="f10-empty">未返回</p>
            <p v-else-if="!news.length" class="f10-empty">暂无公告消息。</p>
            <ul v-else class="f10-news">
              <li v-for="item in news" :key="item.id || item.time + item.title">
                <button type="button" class="f10-news__row" @click="openAnnouncement(item)">
                  <b class="time">{{ formatDateTime(item.time) || MISS }}</b>
                  <span class="title">{{ item.title }}</span>
                  <NtBadge v-if="item.source" tone="neutral">{{ item.source }}</NtBadge>
                </button>
              </li>
            </ul>
          </section>

          <p v-else class="f10-empty">AI 研判证据链未接入本抽屉——接入前不呈现结论。</p>
        </div>
      </aside>
    </div>
  </Teleport>
</template>

<style scoped>
/* 右锚抽屉浮层：与 NtModal/FunctionIndexOverlay 同一条玻璃弹层语言，无 hero 级效果。 */
.f10-mask {
  position: fixed;
  inset: 0;
  display: flex;
  justify-content: flex-end;
  background: var(--nt-scrim);
  transition: opacity var(--nt-transition);
}

.f10-drawer {
  display: flex;
  flex-direction: column;
  width: min(420px, 100vw);
  height: 100%;
  padding: 14px 16px;
  background: var(--nt-glass-sheen), var(--nt-glass-bg);
  backdrop-filter: var(--nt-glass-filter);
  border-left: 1px solid var(--nt-glass-border);
  box-shadow: var(--nt-shadow-depth);
  outline: none;
  transition: transform var(--nt-transition);
}

.f10-drawer__head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-bottom: 10px;
}

.f10-drawer__badges {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.f10-drawer__close {
  flex: none;
}

.f10-drawer__body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding-top: 10px;
}

.f10-sec__title {
  margin: 0 0 6px;
  font-size: var(--nt-fs-caption);
  font-weight: 600;
  color: var(--nt-text-3);
}

.f10-sec__title + .pairs {
  margin-bottom: 14px;
}

/* 对齐 quote-sidebar .pairs 布局（令牌字号替代裸 px）。 */
.pairs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 18px;
  margin: 0;
  font-size: var(--nt-fs-body);
}

.pairs dt {
  color: var(--nt-text-3);
}

.pairs dd {
  margin: 0;
  text-align: right;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-1);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pairs dd.miss {
  color: var(--nt-text-3);
}

.f10-src {
  margin: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
  text-align: right;
}

.f10-empty {
  margin: 0;
  padding: 10px 12px;
  border: 1px dashed var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  font-size: var(--nt-fs-body);
  color: var(--nt-text-3);
  line-height: 1.6;
}

.f10-news {
  display: flex;
  flex-direction: column;
  gap: 4px;
  list-style: none;
  margin: 0;
  padding: 0;
}

.f10-news__row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  width: 100%;
  padding: 4px 6px;
  border: none;
  border-radius: var(--nt-radius-s);
  background: transparent;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.f10-news__row:hover {
  background: var(--nt-accent-soft);
}

.f10-news__row .time {
  flex: none;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 400;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-2);
}

.f10-news__row .title {
  flex: 1;
  min-width: 0;
  font-size: var(--nt-fs-body);
  color: var(--nt-text-1);
}

/* 即刻显隐兜底：全局 [data-motion='reduced'] 已归零 transition，此处显式对齐抽屉合同。 */
:root[data-motion='reduced'] .f10-mask,
:root[data-motion='reduced'] .f10-drawer {
  transition: none;
}
</style>
