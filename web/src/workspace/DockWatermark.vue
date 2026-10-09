<script setup lang="ts">
/** 空工作台水印：脉搏 + 快捷键提示。 */
import NovaStatusPulse from '@/component/NovaStatusPulse.vue'
import { isLive } from '@/ws/hub'
</script>

<template>
  <div class="nt-watermark">
    <NovaStatusPulse :active="isLive()" />
    <p class="hint">按下 <kbd>Ctrl</kbd> + <kbd>K</kbd> 打开面板</p>
    <p class="sub">拖动标签页可拆分、合并、浮窗</p>
  </div>
</template>

<style scoped>
.hint {
  margin: 0;
  font-size: 13px;
  color: var(--nt-text-2);
}

.sub {
  margin: 0;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

kbd {
  padding: 2px 7px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-1);
  font-family: var(--nt-font-mono);
  font-size: 11.5px;
  color: var(--nt-text-2);
}
</style>
