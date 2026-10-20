<script setup lang="ts">
/**
 * NtConfigField —— 配置字段卡五件套（§9.1，53 会话收窄版：只做组件，不接 ConfigCenter）。
 * 五件套：标题 + 一句人话（hint, text-3）/ 控件（按 kind）/ 默认值徽注「已修改」accent 点 / 生效范围章。
 * 独立组件：不 import config-catalog / ConfigCenter（53 会话禁件）；默认值、生效范围等元数据
 * 旁挂在 @/view/config-field-meta.ts，由调用方组装成 props 传入，未来由主会话合并接入。
 * mono 合同（已拍板）：number / int / json / secret 恒 mono；text / select / bool 恒非 mono。
 * 数据纪律：密文一律掩码「••••••」+「已设置 / 未设置」章，不回显明文（§9.1）。
 */
import { computed } from 'vue'
import NtBadge, { type BadgeTone } from './NtBadge.vue'
import NtInput from './NtInput.vue'
import NtSelect from './NtSelect.vue'
import NtSwitch from './NtSwitch.vue'

export interface ConfigFieldLite {
  key: string
  label: string
  kind: 'text' | 'number' | 'int' | 'bool' | 'secret' | 'json' | 'select'
  hint?: string
  options?: { value: string; label: string }[]
  unit?: string
  placeholder?: string
}

export type SecretState = 'unset' | 'set' | 'clearing'
export type EffectiveScope = 'immediate' | 'on-save' | 'on-restart'

const props = defineProps<{
  field: ConfigFieldLite
  modelValue?: unknown
  /** 保存值基线：旁挂预留（53 会话接 ConfigCenter 时用于区分「未保存」），本批不渲染。 */
  baseline?: unknown
  defaultValue?: unknown
  disabled?: boolean
  secretState?: SecretState
  effective?: EffectiveScope
}>()

const emit = defineEmits<{ (e: 'update:modelValue', value: unknown): void; (e: 'clear'): void }>()

const kind = computed(() => props.field.kind)

/** 文本域受控值：string 原样、number / boolean 转串、对象 JSON 化，null / undefined 显空串（不拿 0 冒充）。 */
const textValue = computed(() => {
  const v = props.modelValue
  if (v == null) return ''
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return ''
  }
})

/** bool：兼容后端字符串态 'true' / 'false'（strategy_config 存字串）；emit 恒回布尔。 */
const boolValue = computed(() => props.modelValue === true || props.modelValue === 'true')

const selectValue = computed(() => {
  const v = props.modelValue
  return typeof v === 'string' || typeof v === 'number' ? v : ''
})

/** 默认值徽注：defaultValue 存在且与当前值 JSON 序列化不同 → 「已修改」accent 小圆点。 */
const showModified = computed(() => props.defaultValue !== undefined && JSON.stringify(props.modelValue) !== JSON.stringify(props.defaultValue))
const defaultTitle = computed(() => {
  const v = props.defaultValue
  const raw = typeof v === 'string' ? v : JSON.stringify(v)
  return `默认值：${raw ?? '未知'}`
})

/** 生效范围章（§9.1 第三件）：立即=success / 保存后=neutral / 重启=warn。 */
const EFFECTIVE_META: Record<EffectiveScope, { text: string; tone: BadgeTone }> = {
  immediate: { text: '立即生效', tone: 'success' },
  'on-save': { text: '保存后生效', tone: 'neutral' },
  'on-restart': { text: '重启后生效', tone: 'warn' },
}
const effectiveMeta = computed(() => (props.effective ? EFFECTIVE_META[props.effective] : null))

/** secret：掩码恒显（不回显明文）；章反映 secretState；清除钮仅 secretState==='set' 且未禁用时可点。 */
const secretSet = computed(() => props.secretState === 'set')
const secretClearDisabled = computed(() => props.disabled || !secretSet.value)

function onUpdate(value: unknown) {
  emit('update:modelValue', value)
}
</script>

<template>
  <div class="nt-config-field">
    <div class="nt-config-field__head">
      <span class="nt-config-field__label">{{ field.label }}</span>
      <span v-if="field.hint" class="nt-config-field__hint">{{ field.hint }}</span>
    </div>
    <div class="nt-config-field__row">
      <div class="nt-config-field__control">
        <!-- text：恒非 mono -->
        <NtInput
          v-if="kind === 'text'"
          type="text"
          :model-value="textValue"
          :placeholder="field.placeholder"
          :disabled="disabled"
          @update:model-value="onUpdate"
        />
        <!-- number / int：恒 mono（NtInput number 步进恒为 any，int 的整数约束由上层 coerce 校验） -->
        <NtInput
          v-else-if="kind === 'number' || kind === 'int'"
          type="number"
          mono
          :model-value="textValue"
          :placeholder="field.placeholder"
          :disabled="disabled"
          @update:model-value="onUpdate"
        />
        <!-- bool：开关（NtSwitch），恒非 mono -->
        <NtSwitch v-else-if="kind === 'bool'" :model-value="boolValue" :disabled="disabled" @update:model-value="onUpdate" />
        <!-- select：恒非 mono -->
        <NtSelect
          v-else-if="kind === 'select'"
          :model-value="selectValue"
          :options="field.options ?? []"
          :disabled="disabled"
          @update:model-value="onUpdate"
        />
        <!-- json：多行 textarea，恒 mono；编辑的是文本，解析与回退由上层负责 -->
        <textarea
          v-else-if="kind === 'json'"
          class="nt-config-field__json"
          :value="textValue"
          :placeholder="field.placeholder"
          :disabled="disabled"
          :aria-label="field.label"
          @input="onUpdate(($event.target as HTMLTextAreaElement).value)"
        />
        <!-- secret：只读掩码 + 已设置/未设置章 + 清除钮，恒 mono；不回显明文 -->
        <div v-else class="nt-config-field__secret">
          <span class="nt-config-field__mask" aria-label="密文不回显明文">••••••</span>
          <NtBadge :tone="secretSet ? 'accent' : 'neutral'" dot>{{ secretSet ? '已设置' : '未设置' }}</NtBadge>
          <button type="button" class="nt-config-field__clear" :disabled="secretClearDisabled" @click="emit('clear')">清除</button>
        </div>
      </div>
      <span v-if="field.unit && (kind === 'number' || kind === 'int')" class="nt-config-field__unit">{{ field.unit }}</span>
      <NtBadge v-if="showModified" tone="accent" dot :title="defaultTitle">已修改</NtBadge>
      <NtBadge v-if="effectiveMeta" :tone="effectiveMeta.tone">{{ effectiveMeta.text }}</NtBadge>
    </div>
  </div>
</template>

<style scoped>
.nt-config-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.nt-config-field__head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}

.nt-config-field__label {
  flex: 0 0 auto;
  font-size: var(--nt-fs-body);
  color: var(--nt-text-2);
}

.nt-config-field__hint {
  min-width: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

.nt-config-field__row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
}

.nt-config-field__control {
  flex: 1 1 200px;
  min-width: 0;
}

.nt-config-field__unit {
  flex: 0 0 auto;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

/* json 多行编辑：边框肤感沿用全局 controls.css 的 textarea 规则，这里只补 mono 与行高。 */
.nt-config-field__json {
  font-family: var(--nt-font-mono);
  font-size: var(--nt-fs-body);
  min-height: 96px;
  resize: vertical;
}

.nt-config-field__secret {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.nt-config-field__mask {
  font-family: var(--nt-font-mono);
  font-size: var(--nt-fs-body);
  letter-spacing: 0.1em;
  color: var(--nt-text-3);
}
</style>
