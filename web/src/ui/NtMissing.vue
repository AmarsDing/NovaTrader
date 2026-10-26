<script setup lang="ts">
/** 缺席占位符（B3）：数据纪律「没取到≠0」的最小单元——字段级缺失不画 0 不画假数，
 *  画一枚中性占位，reason 说了就带上（「没取到·源未连接」）。 */
withDefaults(
  defineProps<{
    /** 缺失原因（可选）：源未连接 / 未返回 / 字典缺名 …… */
    reason?: string
    /** 占位词，默认「没取到」。 */
    label?: string
  }>(),
  { reason: '', label: '没取到' },
)
</script>

<template>
  <span class="nt-missing-inline" :title="reason || undefined">
    {{ label }}<template v-if="reason">·{{ reason }}</template>
  </span>
</template>

<style scoped>
.nt-missing-inline {
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
  border-bottom: 1px dashed var(--nt-border-2);
  cursor: default;
  white-space: nowrap;
}
</style>
