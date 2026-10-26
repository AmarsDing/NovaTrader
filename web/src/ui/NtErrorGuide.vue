<script setup lang="ts">
import { computed } from 'vue'
import NtButton from './NtButton.vue'
import { messageOf } from '@/utils/format'

/** 错误导引行（B3）：代替散落的裸 `nt-err`——错误不止报错，给「哪里坏了 + 下一步做什么」。
 *  error 为空不落任何 DOM（调用面免 v-if 包装）；原因文本经 messageOf 归一。 */
const props = withDefaults(
  defineProps<{
    error?: unknown
    /** 业务语境前缀，如「数据源中心读面」；给了渲染「{context}失败：」引导。 */
    context?: string
    /** 重试钮文案（如「刷新重试」）；给了才渲染，点击 emit('retry')。 */
    retry?: string
    retryLoading?: boolean
  }>(),
  { error: undefined, context: '', retry: '', retryLoading: false },
)
const emit = defineEmits<{ (e: 'retry'): void }>()

const text = computed(() => (props.error ? messageOf(props.error) : ''))
</script>

<template>
  <p v-if="text" class="nt-err-guide" role="alert">
    <span class="nt-err-guide__text"><template v-if="context">{{ context }}：</template>{{ text }}</span>
    <NtButton v-if="retry" size="sm" variant="quiet" :loading="retryLoading" @click="emit('retry')">{{ retry }}</NtButton>
  </p>
</template>

<style scoped>
.nt-err-guide {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin: 0;
  padding: 6px 10px;
  font-size: var(--nt-fs-body);
  color: color-mix(in srgb, var(--nt-danger) 80%, var(--nt-text-1));
  background: color-mix(in srgb, var(--nt-danger) 7%, transparent);
  border: 1px solid color-mix(in srgb, var(--nt-danger) 30%, transparent);
  border-radius: var(--nt-radius-s);
}
</style>
