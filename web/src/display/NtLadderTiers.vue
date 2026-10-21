<script setup lang="ts">
/**
 * 涨停梯队（M02 涨停生态核心展示）。
 * tiers: 连板高度分组，每组一列股票；sealed/broken 计数。
 * 个股一律走 SymbolName stacked（方案 §6.3 R5）：名称出自 security 名称字典，缺名显「名称未返回」。
 * U4 视觉语言：梯队卡左 inset 1px 轨道（accent）+ hover 底色走 bg 阶层提亮（bg-1→bg-2，
 * 非 glow、零常驻 tint——数据背板稳定底色纪律）；个股行右列 = 板块角标（NtBoardTag，
 * 主板不打标）+ 封单额 compactAmount（null 显「—」，数据纪律）。
 */
import NtBoardTag from '@/display/NtBoardTag.vue'
import SymbolName from '@/display/SymbolName.vue'
import { compactAmount } from '@/utils/format'

export interface LadderStock {
  symbol: string
  /** 旧字段：名称已由 SymbolName 从字典解析，调用方无需再传。 */
  name?: string
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
          <li
            v-for="s in tier.stocks"
            :key="s.symbol"
            class="nt-ladder__stock"
            :class="{ broken: s.status === 'BROKEN' }"
          >
            <SymbolName :code="s.symbol" mode="stacked" />
            <span class="nt-ladder__meta">
              <NtBoardTag :code="s.symbol" />
              <b class="nt-ladder__seal">{{ compactAmount(s.sealAmount) }}</b>
            </span>
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
  /* U4 左轨道：1px accent inset（page.css inset 轨道先例收窄档），零常驻渐变 tint。 */
  box-shadow: inset 1px 0 var(--nt-accent);
}

/* U4 hover 提亮：走 bg 阶层（bg-1→bg-2）一步抬升，禁 glow/禁 tint。 */
.nt-ladder__tier:hover {
  background: var(--nt-bg-2);
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
  padding: 6px 10px 6px 12px; /* 右列等宽：左 padding +2px 抵消轨道内缩，防名称贴轨 */
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

.nt-ladder__meta {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  justify-content: center;
  gap: 3px;
  flex: none;
}

.nt-ladder__seal {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  color: var(--nt-text-2);
}

.nt-ladder__empty {
  margin: 0;
  padding: 14px;
  font-size: 12px;
  color: var(--nt-text-3);
}
</style>
