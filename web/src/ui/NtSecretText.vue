<script setup lang="ts">
/**
 * 凭据遮蔽文本（R-UI-24）：「•••」是遮蔽槽，与「—」缺位槽语义分家。
 * - 受控：传 masked + update:masked 自行管理；非受控：内部管理，初始遮蔽。
 * - copyable 开启复制按钮，复制的是原文；遮蔽态切换不影响复制值。
 */
import { computed, ref } from 'vue'

const props = withDefaults(
  defineProps<{
    text: string
    /** 受控遮蔽态；不传则内部管理（初始遮蔽）。 */
    masked?: boolean
    copyable?: boolean
    maskChar?: string
    maskLength?: number
  }>(),
  {
    masked: undefined,
    copyable: false,
    maskChar: '•',
    maskLength: 8,
  },
)

const emit = defineEmits<{ (e: 'update:masked', value: boolean): void }>()

const innerMasked = ref(true)
const displayMasked = computed(() => props.masked ?? innerMasked.value)
const displayText = computed(() => (displayMasked.value ? props.maskChar.repeat(props.maskLength) : props.text))

function toggle() {
  const next = !displayMasked.value
  if (props.masked === undefined) innerMasked.value = next
  emit('update:masked', next)
}

async function copy() {
  try {
    await navigator.clipboard.writeText(props.text)
  } catch {
    // 剪贴板不可用时静默：遮蔽语义不受影响。
  }
}
</script>

<template>
  <span class="nt-secret" :data-state="displayMasked ? 'masked' : 'plain'">
    <code class="nt-secret-text">{{ displayText }}</code>
    <button type="button" class="nt-secret-btn" :aria-label="displayMasked ? '显示原文' : '遮蔽'" @click="toggle">
      {{ displayMasked ? '显示' : '遮蔽' }}
    </button>
    <button v-if="copyable" type="button" class="nt-secret-btn" aria-label="复制原文" @click="copy">复制</button>
  </span>
</template>

<style scoped>
.nt-secret {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.nt-secret-text {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.04em;
  color: var(--nt-text-1);
}
.nt-secret[data-state='masked'] .nt-secret-text {
  color: var(--nt-text-3);
}
.nt-secret-btn {
  font-size: 11px;
  color: var(--nt-text-2);
  background: transparent;
  border: 1px solid var(--nt-border-1);
  border-radius: 4px;
  padding: 1px 6px;
  cursor: pointer;
}
.nt-secret-btn:hover {
  color: var(--nt-text-1);
  border-color: var(--nt-border-2);
}
</style>
