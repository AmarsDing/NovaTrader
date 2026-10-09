<script setup lang="ts">
/**
 * 五档盘口（买卖各五档，带挂单量柱）。
 * 红涨绿跌：卖盘红、买盘绿。
 */
export interface DepthLevel {
  price: number | null
  volume: number | null
}

withDefaults(
  defineProps<{
    asks?: DepthLevel[]
    bids?: DepthLevel[]
  }>(),
  { asks: () => [], bids: () => [] },
)

function widthOf(levels: DepthLevel[], index: number): number {
  const max = Math.max(1, ...levels.map((l) => l.volume ?? 0))
  const v = levels[index]?.volume ?? 0
  return Math.round((v / max) * 100)
}
</script>

<template>
  <div class="nt-depth">
    <div class="nt-depth__side nt-depth__side--ask">
      <div v-for="(level, index) in [...asks].reverse()" :key="`a${index}`" class="nt-depth__row">
        <span class="nt-depth__bar" :style="{ width: `${widthOf(asks, asks.length - 1 - index)}%` }" />
        <span class="nt-depth__price up">{{ level.price ?? '—' }}</span>
        <span class="nt-depth__vol">{{ level.volume ?? '—' }}</span>
      </div>
    </div>
    <div class="nt-depth__mid">—</div>
    <div class="nt-depth__side">
      <div v-for="(level, index) in bids" :key="`b${index}`" class="nt-depth__row">
        <span class="nt-depth__bar nt-depth__bar--bid" :style="{ width: `${widthOf(bids, index)}%` }" />
        <span class="nt-depth__price down">{{ level.price ?? '—' }}</span>
        <span class="nt-depth__vol">{{ level.volume ?? '—' }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.nt-depth {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12px;
  font-family: var(--nt-font-mono);
}

.nt-depth__row {
  position: relative;
  display: flex;
  justify-content: space-between;
  padding: 1px 6px;
}

.nt-depth__bar {
  position: absolute;
  inset: 0 auto 0 0;
  background: rgba(255, 77, 77, 0.08);
  border-right: 1px solid rgba(255, 77, 77, 0.2);
}

.nt-depth__bar--bid {
  background: rgba(0, 208, 132, 0.08);
  border-right: 1px solid rgba(0, 208, 132, 0.2);
}

.nt-depth__price {
  font-weight: 600;
}

.nt-depth__vol {
  color: var(--nt-text-2);
}

.nt-depth__mid {
  text-align: center;
  color: var(--nt-text-3);
  font-size: 10px;
  line-height: 8px;
}
</style>
