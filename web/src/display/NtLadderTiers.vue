<script setup lang="ts">
/**
 * 涨停梯队（M02 涨停生态核心展示）。
 * tiers: 连板高度分组，每组一列股票；sealed/broken 计数。
 */
export interface LadderStock {
  symbol: string
  name: string
  sealAmount?: number | null
  openCount?: number | null
  status?: string
}

export interface LadderTier {
  height: number
  stocks: LadderStock[]
}

defineProps<{ tiers: LadderTier[]; empty?: string }>()
</script>

<template>
  <div class="nt-ladder">
    <p v-if="!tiers.length" class="nt-ladder__empty">{{ empty || '今日无涨停梯队' }}</p>
    <div v-else class="nt-ladder__cols">
      <section v-for="tier in tiers" :key="tier.height" class="nt-ladder__tier">
        <header class="nt-ladder__head">
          <b>{{ tier.height }} 板</b>
          <span>{{ tier.stocks.length }} 只</span>
        </header>
        <ul class="nt-ladder__stocks">
          <li v-for="s in tier.stocks" :key="s.symbol" class="nt-ladder__stock" :class="{ broken: s.status === 'BROKEN' }">
            <span class="nt-ladder__name">{{ s.name || s.symbol }}</span>
            <span class="nt-ladder__symbol">{{ s.symbol }}</span>
          </li>
        </ul>
      </section>
    </div>
  </div>
</template>

<style scoped>
.nt-ladder__cols {
  display: flex;
  gap: 10px;
  overflow-x: auto;
  padding-bottom: 4px;
}

.nt-ladder__tier {
  min-width: 132px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.nt-ladder__head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  padding: 7px 10px;
  border-bottom: 1px solid var(--nt-border-1);
}

.nt-ladder__head b {
  font-size: 13px;
  color: var(--nt-up);
}

.nt-ladder__head span {
  font-size: 11px;
  color: var(--nt-text-3);
}

.nt-ladder__stocks {
  list-style: none;
  margin: 0;
  padding: 6px 10px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.nt-ladder__stock {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  color: var(--nt-text-1);
}

.nt-ladder__stock.broken {
  color: var(--nt-text-3);
  text-decoration: line-through;
}

.nt-ladder__symbol {
  font-family: var(--nt-font-mono);
  color: var(--nt-text-3);
  font-size: 11px;
}

.nt-ladder__empty {
  margin: 0;
  padding: 14px;
  font-size: 12px;
  color: var(--nt-text-3);
}
</style>
