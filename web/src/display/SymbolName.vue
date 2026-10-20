<script setup lang="ts">
/**
 * SymbolName 证券标识组件（方案 §6.1）：名称 + 代码的全站统一渲染单位。
 * - 四模式：inline「名称 裸六位」一行 / stacked 名称上行代码下行 / compact 只名 / code 只码
 *   （code 为豁免位专用：TAPE 跑马、K 线水印、逐笔队列，§6.6）；
 * - 缺名一律显弱化「名称未返回」（text-3），不留空白、禁拿代码冒充名称（数据纪律）；
 * - 点击（缺省可点；code 模式缺省不可点）：setSymbol(规范全码) + openPanel('quote-sidebar')，
 *   工作台不存在时静默跳过；键盘 Enter/Space 可达；
 * - 名称出自 stores/security 名称字典；挂载与换码 ensure 后台补名（fetcher 未注册时 noop）。
 */
import { computed, onMounted, watch } from 'vue'
import { normalizeCode, useSecurityStore } from '@/stores/security'
import { useSymbolStore } from '@/stores/symbol'
import { getWorkbench } from '@/workspace/inject'

export type SymbolNameMode = 'inline' | 'stacked' | 'compact' | 'code'

const props = withDefaults(
  defineProps<{
    /** 证券代码：裸六位或全码均可，内部 normalizeCode 归一（6→SH，0/3→SZ，4/8/92→BJ）。 */
    code: string
    mode?: SymbolNameMode
    /** 三态：true/false 钉死；缺省（null）时 code 模式不可点、其余模式可点。 */
    clickable?: boolean | null
  }>(),
  { mode: 'inline', clickable: null },
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
  symbolStore.setSymbol(fullCode.value, 'SymbolName')
  getWorkbench()?.openPanel('quote-sidebar', { symbol: fullCode.value })
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    activate()
  }
}
</script>

<template>
  <span
    class="sn"
    :class="[`sn--${mode}`, { 'sn--click': canClick, 'sn--noname': !resolved.hasName }]"
    :role="canClick ? 'button' : undefined"
    :tabindex="canClick ? 0 : undefined"
    @click="activate"
    @keydown="onKeydown"
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
