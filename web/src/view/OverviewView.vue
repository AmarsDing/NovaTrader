<script setup lang="ts">
import { computed, onMounted } from 'vue'
import NtSectorBar from '@/viz/NtSectorBar.vue'
import NtStatCard from '@/display/NtStatCard.vue'
import { useAccountStore } from '@/stores/account'
import { useMarketStore } from '@/stores/market'
import { useRiskStore } from '@/stores/risk'
import { quiet } from '@/utils/async'
import { amount, phaseName, pnlClass, ratioPct, signed } from '@/utils/format'
import { isLive, link } from '@/ws/hub'
import type { Position } from '@/api/trade'

const account = useAccountStore()
const market = useMarketStore()
const risk = useRiskStore()

onMounted(() => {
  void Promise.all([quiet(account.refresh), quiet(market.refresh), quiet(risk.refresh)])
})

const chartRows = computed(() =>
  market.sectors
    .filter((row) => row.avgPct != null)
    .map((row) => ({ name: row.name, avgPct: Number((row.avgPct as number).toFixed(2)) })),
)

const overseas = computed(() => {
  const row = [...market.sectors].sort((a, b) => Math.abs(b.overseas || 0) - Math.abs(a.overseas || 0))[0]
  if (!row || row.overseas == null) return null
  return row
})

function weight(position: Position): string {
  if (!account.equity || !position.lastPrice || !position.quantity) return ''
  return `${(((position.lastPrice * position.quantity) / account.equity) * 100).toFixed(1)}%`
}
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">总览</h1>
        <p class="nt-note">权益、盈亏、情绪、外围和系统状态。没有读到的项保持为「—」。</p>
      </div>
    </header>

    <section class="nt-cards">
      <NtStatCard label="权益" :value="amount(account.equity)" :sub="`可用 ${amount(account.cash)}`" hero />
      <NtStatCard
        label="浮动盈亏"
        :value="signed(account.unrealized)"
        :sub="`已实现 ${signed(account.realized)}`"
        :tone="pnlClass(account.unrealized) || 'neutral'"
        hero
      />
      <NtStatCard
        label="情绪"
        :value="phaseName(market.phase)"
        :sub="`仓位系数 ${market.positionScale ?? '—'} · 上涨 ${market.up ?? '—'} / 下跌 ${market.down ?? '—'}`"
      />
      <NtStatCard
        label="外围"
        :value="overseas ? overseas.name : '—'"
        :sub="overseas ? `脉冲 ${overseas.overseas}` : '板块外围脉冲未返回'"
      />
      <NtStatCard
        label="系统"
        :value="isLive() ? '实时' : '不是实时'"
        :sub="`模式 ${risk.mode || '—'} · 炸板率 ${ratioPct(market.brokenRate)}`"
      />
      <NtStatCard
        label="模拟盘天数"
        :value="account.paperDays != null ? String(account.paperDays) : '—'"
        :sub="account.openHalted ? account.haltReason || '开仓已停' : '开仓未停'"
      />
    </section>

    <div class="nt-split">
      <section class="nt-panel">
        <h2 class="nt-panel__title">持仓分布</h2>
        <p v-if="!account.positions.length" class="nt-empty">暂无持仓。</p>
        <ul v-else class="chips">
          <li v-for="item in account.positions" :key="item.symbol">
            <span>{{ item.symbol }}</span>
            <b>{{ item.quantity }}</b>
            <em>{{ weight(item) }}</em>
          </li>
        </ul>
      </section>
      <section class="nt-panel">
        <h2 class="nt-panel__title">板块涨跌</h2>
        <p v-if="!chartRows.length" class="nt-empty">
          板块热度未返回。通道 {{ link.ws === 'open' ? '已连接' : '未连接' }}。
        </p>
        <NtSectorBar v-else :rows="chartRows" height="280px" />
      </section>
    </div>
  </div>
</template>

<style scoped>
.chips {
  list-style: none;
  margin: 10px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.chips li {
  display: flex;
  gap: 10px;
  align-items: baseline;
  font-size: 12.5px;
}

.chips b {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
}

.chips em {
  color: var(--nt-accent);
  font-style: normal;
  min-width: 52px;
  text-align: right;
  font-family: var(--nt-font-mono);
}
</style>
