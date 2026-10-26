<script setup lang="ts">
import { computed } from 'vue'

/** 骨架占位（B3）：首屏/切换读取中的形状预告，替代空白闪烁（R-UI-35 配套）。
 *  微光流动在减少动态/节能档下停用（§7 同类约束）。 */
const props = withDefaults(
  defineProps<{
    /** 行数（kind=rows/table 时生效）。 */
    rows?: number
    /** rows=多行文本；table=表行（首列窄块）；card=单卡片块。 */
    kind?: 'rows' | 'table' | 'card'
  }>(),
  { rows: 3, kind: 'rows' },
)

const lines = computed(() => Array.from({ length: Math.max(1, Math.min(props.rows, 12)) }, (_, i) => i))
</script>

<template>
  <div class="nt-skel" :class="`nt-skel--${kind}`" role="status" aria-busy="true" aria-label="读取中">
    <template v-if="kind === 'card'">
      <span class="nt-skel__row nt-skel--w60" />
      <span class="nt-skel__row" />
      <span class="nt-skel__row nt-skel--w80" />
    </template>
    <template v-else>
      <span v-for="i in lines" :key="i" class="nt-skel__row" :class="{ 'nt-skel__row--lead': kind === 'table' }" />
    </template>
  </div>
</template>

<style scoped>
.nt-skel {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 2px;
}

.nt-skel__row {
  height: 12px;
  border-radius: var(--nt-radius-s);
  background: linear-gradient(90deg, var(--nt-bg-2) 25%, var(--nt-bg-3, var(--nt-bg-2)) 50%, var(--nt-bg-2) 75%);
  background-size: 200% 100%;
  animation: nt-skel-flow 1.4s ease infinite;
}

.nt-skel--w60 {
  width: 60%;
}

.nt-skel--w80 {
  width: 80%;
}

.nt-skel__row--lead {
  width: 72%;
}

.nt-skel--card {
  padding: 12px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-m);
}

@keyframes nt-skel-flow {
  0% {
    background-position: 100% 0;
  }
  100% {
    background-position: -100% 0;
  }
}

/* §7：减少动态/节能档停用微光，保静态骨架形状。 */
:global(:root[data-motion='reduced']) .nt-skel__row,
:global(:root[data-visual-quality='eco']) .nt-skel__row {
  animation: none;
  background: var(--nt-bg-2);
}
</style>
