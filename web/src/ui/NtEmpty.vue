<script setup lang="ts">
import NtButton from './NtButton.vue'

/** 三分态统一空态件（B3）：「读取中 / 未返回 / 真空」同一底座。
 *  纪律（R-UI-24 系）：loading 不冒充真空、未返回必带下一步（重试引导），真空给行为引导——
 *  「没取到≠空」，空列表永远解释「为什么空、下一步去哪」。 */
withDefaults(
  defineProps<{
    /** loading=读取中骨架行；missing=未返回（warn 档，建议配 action=重试）；empty=真空（中性）。 */
    state?: 'loading' | 'missing' | 'empty'
    /** 一句话主题：「还没有候选股」「消息未返回」……不写「无数据」这种机器词。 */
    title: string
    /** 引导句：告诉用户下一步去哪/等什么，如「经策略扫描出入池后在这里展开」。 */
    hint?: string
    /** 行动钮文案（如「重试」）；给了才渲染，点击 emit('action')。 */
    action?: string
    actionLoading?: boolean
  }>(),
  { state: 'empty', hint: '', action: '', actionLoading: false },
)
const emit = defineEmits<{ (e: 'action'): void }>()
</script>

<template>
  <div class="nt-empty-box" :class="`nt-empty-box--${state}`">
    <i v-if="state === 'loading'" class="nt-empty-box__spin" aria-hidden="true" />
    <i v-else class="nt-empty-box__dot" aria-hidden="true" />
    <p class="nt-empty-box__title">{{ title }}</p>
    <p v-if="hint || $slots.default" class="nt-empty-box__hint"><slot>{{ hint }}</slot></p>
    <NtButton v-if="action" size="sm" variant="quiet" :loading="actionLoading" @click="emit('action')">{{ action }}</NtButton>
  </div>
</template>

<style scoped>
.nt-empty-box {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding: 12px 2px;
  font-size: var(--nt-fs-body);
  color: var(--nt-text-3);
}

.nt-empty-box__title {
  margin: 0;
  font-size: var(--nt-fs-body);
  font-weight: 600;
  color: var(--nt-text-2);
}

.nt-empty-box__hint {
  margin: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

/* 未返回 = warn 档引导（数据纪律显式缺失），不冒充真空也不染 danger。 */
.nt-empty-box--missing .nt-empty-box__title {
  color: color-mix(in srgb, var(--nt-warn) 80%, var(--nt-text-1));
}

.nt-empty-box__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--nt-text-3);
}

.nt-empty-box--missing .nt-empty-box__dot {
  background: var(--nt-warn);
}

.nt-empty-box__spin {
  width: 12px;
  height: 12px;
  border: 2px solid var(--nt-text-3);
  border-top-color: transparent;
  border-radius: 50%;
  animation: nt-empty-spin 0.7s linear infinite;
}

@keyframes nt-empty-spin {
  to {
    transform: rotate(360deg);
  }
}

/* 减少动态/节能档：停转（§7 禁常驻呼吸同类约束）。 */
:global(:root[data-motion='reduced']) .nt-empty-box__spin,
:global(:root[data-visual-quality='eco']) .nt-empty-box__spin {
  animation: none;
}
</style>
