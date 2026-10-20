<script setup lang="ts">
/**
 * SymbolName 证券标识组件（方案 §6.1）：名称 + 代码的全站统一渲染单位。
 * - 四模式：inline「名称 裸六位」一行 / stacked 名称上行代码下行 / compact 只名 / code 只码
 *   （code 为豁免位专用：TAPE 跑马、K 线水印、逐笔队列，§6.6）；
 * - 缺名一律显弱化「名称未返回」（text-3），不留空白、禁拿代码冒充名称（数据纪律）；
 * - 点击（缺省可点；code 模式缺省不可点）：setSymbol(规范全码) + openPanel('quote-sidebar')，
 *   工作台不存在时静默跳过；键盘 Enter/Space 可达；
 * - f10（opt-in 试点，§6.5）：true 且码有效时 hover 600ms 出 F10HoverCard 速览（离开 100ms 宽限、
 *   卡 enter 保活、Esc 由卡的 useOverlay 关栈顶），点击/Enter/Space 改走 f10.openFor 进抽屉；
 *   false（缺省）时对外行为与现网全等；
 * - 名称出自 stores/security 名称字典；挂载与换码 ensure 后台补名（fetcher 未注册时 noop）。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { normalizeCode, useSecurityStore } from '@/stores/security'
import { useSymbolStore } from '@/stores/symbol'
import { useF10Store } from '@/stores/f10'
import { getWorkbench } from '@/workspace/inject'
import F10HoverCard from './F10HoverCard.vue'

export type SymbolNameMode = 'inline' | 'stacked' | 'compact' | 'code'

const props = withDefaults(
  defineProps<{
    /** 证券代码：裸六位或全码均可，内部 normalizeCode 归一（6→SH，0/3→SZ，4/8/92→BJ）。 */
    code: string
    mode?: SymbolNameMode
    /** 三态：true/false 钉死；缺省（null）时 code 模式不可点、其余模式可点。 */
    clickable?: boolean | null
    /** F10 试点开关：true 时 hover 出速览卡、点击进 F10 抽屉；false（缺省）与现网全等。 */
    f10?: boolean
  }>(),
  { mode: 'inline', clickable: null, f10: false },
)

const security = useSecurityStore()
const symbolStore = useSymbolStore()

const fullCode = computed(() => normalizeCode(props.code ?? ''))

/** 展示用裸码：A 股全码取六位；认不出的原文透传（判错闸门归后端 parseSymbol）。 */
const shortCode = computed(() => /^(\d{6})\.(?:SH|SZ|BJ)$/.exec(fullCode.value)?.[1] ?? fullCode.value)

const resolved = computed(() =>
  fullCode.value ? security.resolve(fullCode.value) : { code: '', name: '', hasName: false },
)
const displayName = computed(() => (resolved.value.hasName ? resolved.value.name : '名称未返回'))

const canClick = computed(() => props.clickable ?? props.mode !== 'code')

function ensureName() {
  if (fullCode.value) security.ensure([fullCode.value])
}
onMounted(ensureName)
watch(() => props.code, ensureName)

function activate() {
  if (!canClick.value || !fullCode.value) return
  if (props.f10) {
    closeHover()
    useF10Store().openFor(fullCode.value)
    return
  }
  symbolStore.setSymbol(fullCode.value, 'SymbolName')
  getWorkbench()?.openPanel('quote-sidebar', { symbol: fullCode.value })
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    activate()
  }
}

/* ---------- F10 悬浮卡宿主（opt-in；延时结构同 NtTooltip） ---------- */

const root = ref<HTMLElement | null>(null)
const hoverRect = ref<{ x: number; y: number; width: number; height: number } | null>(null)
const hoverOpen = ref(false)
let hoverTimer: ReturnType<typeof setTimeout> | null = null
let leaveTimer: ReturnType<typeof setTimeout> | null = null

function onPointerEnter() {
  if (!props.f10 || !fullCode.value) return
  if (leaveTimer) {
    clearTimeout(leaveTimer)
    leaveTimer = null
  }
  if (hoverOpen.value || hoverTimer) return
  hoverTimer = setTimeout(() => {
    hoverTimer = null
    const el = root.value
    if (!el) return
    const r = el.getBoundingClientRect()
    hoverRect.value = { x: r.x, y: r.y, width: r.width, height: r.height }
    hoverOpen.value = true
  }, 600)
}

function onPointerLeave() {
  if (!props.f10) return
  if (hoverTimer) {
    clearTimeout(hoverTimer)
    hoverTimer = null
  }
  // 100ms 宽限：指针移向卡体时卡的 enter 事件会撤掉本定时保活。
  if (!hoverOpen.value || leaveTimer) return
  leaveTimer = setTimeout(closeHover, 100)
}

/** 卡 pointerenter 保活：撤掉离开宽限。 */
function onCardEnter() {
  if (leaveTimer) {
    clearTimeout(leaveTimer)
    leaveTimer = null
  }
}

function closeHover() {
  if (hoverTimer) {
    clearTimeout(hoverTimer)
    hoverTimer = null
  }
  if (leaveTimer) {
    clearTimeout(leaveTimer)
    leaveTimer = null
  }
  hoverOpen.value = false
  hoverRect.value = null
}

onBeforeUnmount(closeHover)
</script>

<template>
  <span
    ref="root"
    class="sn"
    :class="[`sn--${mode}`, { 'sn--click': canClick, 'sn--noname': !resolved.hasName }]"
    :role="canClick ? 'button' : undefined"
    :tabindex="canClick ? 0 : undefined"
    @click="activate"
    @keydown="onKeydown"
    @pointerenter="onPointerEnter"
    @pointerleave="onPointerLeave"
  >
    <template v-if="mode === 'code'">
      <span class="sn__code">{{ shortCode }}</span>
    </template>
    <template v-else-if="mode === 'compact'">
      <span class="sn__name">{{ displayName }}</span>
    </template>
    <template v-else>
      <span class="sn__name">{{ displayName }}</span>
      <span class="sn__code">{{ shortCode }}</span>
    </template>
    <F10HoverCard
      v-if="hoverOpen && hoverRect"
      :code="fullCode"
      :rect="hoverRect"
      @close="closeHover"
      @enter="onCardEnter"
    />
  </span>
</template>

<style scoped>
.sn {
  display: inline-flex;
  align-items: baseline;
  gap: 0.45em;
  max-width: 100%;
  vertical-align: baseline;
  border-radius: var(--nt-radius-s);
}

.sn--stacked {
  flex-direction: column;
  align-items: flex-start;
  gap: 0.12em;
}

.sn--click {
  cursor: pointer;
}

/* hover 反馈对齐 Nt* 语言：1px 同色下划，禁 glow。 */
.sn--click:hover .sn__name {
  text-decoration: underline;
  text-decoration-thickness: 1px;
  text-underline-offset: 0.18em;
}

.sn--click:hover .sn__code {
  text-decoration: underline;
  text-decoration-thickness: 1px;
  text-underline-offset: 0.18em;
}

.sn--click:focus-visible {
  outline: 1px solid var(--nt-accent);
  outline-offset: 2px;
}

.sn__name {
  font-family: var(--nt-font-sans);
  color: var(--nt-text-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sn--stacked .sn__name {
  font-weight: 500;
}

.sn--noname .sn__name {
  color: var(--nt-text-3);
}

.sn__code {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 0.92em;
  color: var(--nt-text-3);
  white-space: nowrap;
}

/* code 模式（豁免位）：代码是唯一内容，回到整倍字号、前进一档色。 */
.sn--code .sn__code {
  font-size: 1em;
  color: var(--nt-text-2);
}
</style>
